// Command example-eino-talkie-agent talks with an eino agent through
// talkie-core: say the wake word and ask something, and an LLM answers out
// loud, a sentence at a time as it streams in. Interrupt it and its next turn
// knows how much of the reply you heard.
//
//	go install github.com/androiddrew/example-eino-talkie-agent@latest
//	example-eino-talkie-agent -install-skills
//	example-eino-talkie-agent
//
// It finds ONNX Runtime and the models in its data directory, settings in
// config.yaml and skills in its config directory, following the XDG Base
// Directory spec (see config.go); flags override each.
//
// -text runs the same agent without audio: each line of stdin is a turn,
// and the reply is printed as it would be spoken.
//
// The talkie flags are talkie-demo's. Train talkie to the room with
// talkie-demo -train; this command uses the same room profile.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Defaults for the LLM: Ollama on this machine, and the model that measured
// fastest and most reliable with skills (see the README's Models).
const (
	defaultBaseURL = "http://localhost:11434/v1"
	defaultModel   = "gemma4:e4b"
)

// voicePrompt is the default system prompt: replies are spoken.
const voicePrompt = "You are a voice assistant. Your replies are spoken aloud, so answer in one to three short, plain sentences. " +
	"Never use markdown, lists, headings, code, emoji or URLs. If the user says goodbye, say goodbye briefly."

func main() {
	var (
		configFile   = flag.String("config", defaultConfigFile(), "settings file: flag names as YAML keys")
		data         = flag.String("data", dataDir(), "directory with ONNX Runtime and the models, as talkie-core's get-assets.sh -d writes them")
		installSkill = flag.Bool("install-skills", false, "install the bundled skills into -skills, keeping any already there, and exit")
		train        = flag.Bool("train", false, "train talkie to this room and these devices, save the profile, and exit")
		baseURL      = flag.String("base-url", defaultBaseURL, "OpenAI-compatible API base URL")
		modelID      = flag.String("model", defaultModel, "model name")
		apiKey       = flag.String("api-key", "ollama", "API key (Ollama ignores it)")
		reasoning    = flag.String("reasoning", "none", `reasoning effort: "none" turns thinking off; "low", "medium" and "high" are too slow for voice`)
		temp         = flag.Float64("temperature", 0.2, "sampling temperature; low keeps skill use reliable; negative for the server's default")
		instruction  = flag.String("instruction", voicePrompt, "system prompt")
		textMode     = flag.Bool("text", false, "no audio: read turns from stdin and print the replies")
		skillsDir    = flag.String("skills", defaultSkillsDir(), `directory of skills, each a directory with a SKILL.md; "" for none`)

		ort       = flag.String("ort", "", "ONNX Runtime library, a path or loader name (default: in -data, else libonnxruntime.so)")
		cuda      = flag.Bool("cuda", false, "run speech-to-text and text-to-speech on the GPU")
		parakeet  = flag.String("parakeet", "", "directory with the Parakeet TDT 0.6B v3 ONNX files (default: parakeet in -data)")
		int8      = flag.Bool("int8", false, "use Parakeet's INT8 files, faster on CPU; the default when the FP32 files aren't there")
		kokoro    = flag.String("kokoro", "", "directory with kokoro-v1.0.onnx and voices-v1.0.bin (default: kokoro in -data)")
		voice     = flag.String("voice", "af_heart", "Kokoro voice")
		wake      = flag.String("wake", "hey computer", `wake word; "" to use Enter only`)
		input     = flag.String("input", "", "microphone: part of its name; default device if empty")
		output    = flag.String("output", "", "speaker: part of its name; default device if empty")
		noAEC     = flag.Bool("no-echo-cancel", false, "turn off echo cancellation (headset, or an echo-cancelled mic)")
		barge     = flag.String("barge-in", "wake", `what interrupts the agent by voice: "voice", "wake" or "off"`)
		key       = flag.String("key", "", "keyboard event device whose Pause key interrupts, e.g. /dev/input/by-id/...-event-kbd")
		noChime   = flag.Bool("no-chime", false, "turn off the ready chime that measures each session's audio latency")
		cueListen = flag.String("cue-listening", "", `sound when it starts listening: "default" for the built-in one, a WAV file, or "" for none`)
		cueThink  = flag.String("cue-thinking", "", `sound looped while it thinks: "default", a WAV file, or "" for none`)
		cueIdle   = flag.String("cue-idle", "", `sound when the session ends: "default", a WAV file, or "" for none`)
		latency   = flag.Duration("latency", 0, "speaker-to-microphone round trip (default 100ms)")
		verbose   = flag.Bool("v", false, "log every state change")
	)
	flag.Usage = usage
	flag.Parse()
	set, err := applyEnv(flag.CommandLine)
	if err != nil {
		fail(err)
	}
	if err := expandHome(flag.CommandLine); err != nil {
		fail(err)
	}
	if *configFile != "" {
		if err := applyConfig(flag.CommandLine, *configFile, set["config"], set); err != nil {
			fail(err)
		}
	}
	if err := expandHome(flag.CommandLine); err != nil {
		fail(err)
	}
	if *installSkill {
		if *skillsDir == "" {
			fail(fmt.Errorf("-install-skills needs -skills"))
		}
		if err := installSkills(*skillsDir, os.Stdout); err != nil {
			fail(err)
		}
		return
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// docker stop and systemd send SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *train {
		a, err := resolveAssets(*data, assets{ORT: *ort, Parakeet: *parakeet, Kokoro: *kokoro, INT8: *int8})
		if err != nil {
			fail(err)
		}
		cfg := talkie.Config{ORTLibrary: a.ORT, CUDA: *cuda, WakeWord: *wake, InputDevice: *input, OutputDevice: *output,
			Kokoro: talkie.KokoroConfig{
				ModelPath:  filepath.Join(a.Kokoro, "kokoro-v1.0.onnx"),
				VoicesPath: filepath.Join(a.Kokoro, "voices-v1.0.bin"),
				Voice:      *voice,
			}}
		if err := trainRoom(ctx, cfg, os.Stdin, os.Stdout); err != nil {
			fail(err)
		}
		return
	}
	cm, err := newChatModel(ctx, *baseURL, *modelID, *apiKey, *reasoning, *temp)
	if err != nil {
		fail(err)
	}
	if *skillsDir != "" && !exists(*skillsDir) {
		log.Info("no skills yet: run with -install-skills to install the bundled ones", "dir", *skillsDir)
	}
	runner, err := newRunner(ctx, cm, *instruction, *skillsDir, log)
	if err != nil {
		fail(err)
	}
	agent := NewAgent(runner, log)
	warmUp(ctx, cm, *modelID, log)

	if *textMode {
		agent.timed = func(t agentTiming) {
			log.Info("reply", "first_token", sinceOrZero(t.Respond, t.FirstToken),
				"first_sentence", sinceOrZero(t.Respond, t.FirstSentence), "total", t.Done.Sub(t.Respond).Round(time.Millisecond), "chars", t.Chars)
		}
		if err := runText(ctx, agent); err != nil {
			fail(err)
		}
		return
	}

	a, err := resolveAssets(*data, assets{ORT: *ort, Parakeet: *parakeet, Kokoro: *kokoro, INT8: *int8})
	if err != nil {
		fail(fmt.Errorf("%w; or use -text", err))
	}
	log.Info("models", "ort", a.ORT, "parakeet", a.Parakeet, "int8", a.INT8, "kokoro", a.Kokoro)
	cues, err := loadCues(*cueListen, *cueThink, *cueIdle)
	if err != nil {
		fail(err)
	}
	bargeIn, ok := map[string]talkie.BargeIn{"voice": talkie.BargeInVoice, "wake": talkie.BargeInWakeWord, "off": talkie.BargeInOff}[*barge]
	if !ok {
		fail(fmt.Errorf(`-barge-in is "voice", "wake" or "off", not %q`, *barge))
	}
	lat := newLatency(log, os.Stdout)
	agent.timed = lat.Timed
	go lat.Run(ctx)

	log.Info("loading models")
	t, err := talkie.New(ctx, talkie.Config{
		ORTLibrary: a.ORT,
		CUDA:       *cuda,
		Parakeet:   talkie.ParakeetConfig{Dir: a.Parakeet, INT8: a.INT8},
		Kokoro: talkie.KokoroConfig{
			ModelPath:  filepath.Join(a.Kokoro, "kokoro-v1.0.onnx"),
			VoicesPath: filepath.Join(a.Kokoro, "voices-v1.0.bin"),
			Voice:      *voice,
		},
		WakeWord:                *wake,
		InputDevice:             *input,
		OutputDevice:            *output,
		DisableEchoCancellation: *noAEC,
		DisableReadyChime:       *noChime,
		Cues:                    cues,
		BargeIn:                 bargeIn,
		AudioLatency:            *latency,
		Logger:                  log,
		OnEvent:                 lat.OnEvent,
	}, agent)
	if err != nil {
		fail(err)
	}
	defer t.Close()

	go func() { // Enter interrupts, or talks without the wake word
		in := bufio.NewScanner(os.Stdin)
		for in.Scan() {
			t.Interrupt()
		}
	}()
	if *key != "" {
		keys, err := os.Open(*key)
		if err != nil {
			fail(fmt.Errorf("-key: %w (reading a keyboard needs the input group: sudo usermod -aG input $USER, then log in again)", err))
		}
		defer func() { _ = keys.Close() }()
		go func() {
			if err := onPause(keys, t.Interrupt); ctx.Err() == nil {
				log.Warn("the Pause key stopped working", "err", err)
			}
		}()
	}
	if *wake != "" {
		log.Info(fmt.Sprintf("listening: say %q, or press Enter to talk or interrupt; Ctrl-C quits", *wake))
	} else {
		log.Info("listening: press Enter to talk or interrupt; Ctrl-C quits")
	}
	if err := t.Run(ctx); err != nil {
		fail(err)
	}
}

// newChatModel connects to an OpenAI-compatible server such as Ollama.
// reasoning is sent as reasoning_effort; Ollama takes "none" to turn a
// model's thinking off. A negative temperature leaves it to the server.
func newChatModel(ctx context.Context, baseURL, modelID, apiKey, reasoning string, temp float64) (model.ToolCallingChatModel, error) {
	var temperature *float32
	if temp >= 0 {
		t := float32(temp)
		temperature = &t
	}
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:         baseURL,
		Model:           modelID,
		APIKey:          apiKey,
		ReasoningEffort: openai.ReasoningEffortLevel(reasoning),
		Temperature:     temperature,
	})
}

// newRunner wraps the model in a streaming eino ChatModelAgent with the
// skills in skillsDir, read before each turn; "" gives it none.
func newRunner(ctx context.Context, cm model.ToolCallingChatModel, instruction, skillsDir string, log *slog.Logger) (*adk.Runner, error) {
	cfg := &adk.ChatModelAgentConfig{
		Name:        "TalkieVoiceAgent",
		Description: "A voice assistant whose replies are spoken by talkie.",
		Instruction: instruction,
		Model:       cm,
	}
	if skillsDir != "" {
		skills := skillDir{dir: skillsDir, log: log}
		found, err := skills.load()
		if err != nil {
			return nil, fmt.Errorf("skills: %w", err)
		}
		names := make([]string, len(found))
		for i, s := range found {
			names[i] = s.Name
		}
		log.Info("skills", "dir", skillsDir, "names", strings.Join(names, ","))
		cfg.Handlers = []adk.ChatModelAgentMiddleware{skillsHandler{skills: skills}, toolErrors{log: log}}
		// The handler replaces the tools before each turn, but the agent
		// starts with some: eino v0.8.6 drops UnknownToolsHandler when a
		// handler adds an agent's first tool.
		cfg.ToolsConfig.Tools = []tool.BaseTool{&skillTool{skill: agentSkill{Name: "no_skills", Description: "No skills."}}}
		cfg.ToolsConfig.UnknownToolsHandler = func(_ context.Context, name, args string) (string, error) {
			log.Warn("call to an unknown tool", "tool", name, "args", args)
			return fmt.Sprintf("error: there is no tool named %q", name), nil
		}
	}
	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true}), nil
}

// warmUp asks the model for one token, so the server loads it before the
// first question rather than during it.
func warmUp(ctx context.Context, cm model.BaseChatModel, modelID string, log *slog.Logger) {
	log.Info("warming up the model", "model", modelID)
	start := time.Now()
	if _, err := cm.Generate(ctx, []*schema.Message{schema.UserMessage("hi")}, model.WithMaxTokens(1)); err != nil {
		log.Warn("model warm-up failed", "model", modelID, "err", err, "took", time.Since(start).Round(time.Millisecond))
		return
	}
	log.Info("model ready", "model", modelID, "took", time.Since(start).Round(time.Millisecond))
}

// runText runs each line of stdin as a turn of one session, printing the
// reply as it would be spoken. A goodbye starts a new session.
func runText(ctx context.Context, agent *Agent) error {
	in := bufio.NewScanner(os.Stdin)
	session, sessions, replies := "", 0, 0
	for {
		if session == "" {
			sessions++
			session = fmt.Sprintf("text_%d", sessions)
		}
		fmt.Print("you> ")
		if !in.Scan() {
			fmt.Println()
			return in.Err()
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		replies++
		out := &printer{}
		fmt.Print("agent> ")
		err := agent.respond(ctx, talkie.Turn{Session: session, Reply: fmt.Sprintf("reply_%d", replies), Text: line, Trigger: talkie.TriggerCommand}, out)
		fmt.Println()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		if out.then == talkie.ThenEnd {
			fmt.Println("[session ended]")
			session = ""
		}
	}
}

// printer prints a reply as it is written.
type printer struct{ then talkie.Then }

func (p *printer) WriteString(s string) (int, error) { return os.Stdout.WriteString(s) }
func (p *printer) Then(then talkie.Then)             { p.then = then }

func sinceOrZero(from, to time.Time) time.Duration {
	if to.IsZero() {
		return 0
	}
	return to.Sub(from).Round(time.Millisecond)
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return def
}

// usage prints the flags, and where else settings come from.
func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "Usage: %s [flags]\n\n", filepath.Base(os.Args[0]))
	flag.PrintDefaults()
	fmt.Fprintf(out, `
Every flag can also be set by an environment variable, EINO_TALKIE_ and its
name in upper case with _ for -, such as EINO_TALKIE_CUDA=true, or in
config.yaml (-config) with its name as the key. A flag wins over its
variable, which wins over config.yaml. OPENAI_BASE_URL, OPENAI_API_MODEL,
OPENAI_API_KEY and TALKIE_ORT still work.
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "example-eino-talkie-agent:", err)
	os.Exit(1)
}

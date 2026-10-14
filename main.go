// Command example-eino-talkie-agent talks with an eino agent through
// talkie-core: say the wake word and ask something, and an LLM answers out
// loud, a sentence at a time as it streams in. Interrupt it and its next turn
// knows how much of the reply you heard.
//
//	go run . -cuda -ort /path/to/libonnxruntime.so \
//	    -parakeet /path/to/parakeet -kokoro /path/to/kokoro
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
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// voicePrompt is the default system prompt: replies are spoken.
const voicePrompt = "You are a voice assistant. Your replies are spoken aloud, so answer in one to three short, plain sentences. " +
	"Never use markdown, lists, headings, code, emoji or URLs. If the user says goodbye, say goodbye briefly."

func main() {
	var (
		baseURL     = flag.String("base-url", env("OPENAI_BASE_URL", "http://orin.runcible.io:11434/v1"), "OpenAI-compatible API base URL")
		modelID     = flag.String("model", env("OPENAI_API_MODEL", "qwen3.5:9b"), "model name")
		apiKey      = flag.String("api-key", env("OPENAI_API_KEY", "ollama"), "API key (Ollama ignores it)")
		reasoning   = flag.String("reasoning", "none", `reasoning effort: "none" turns thinking off; "low", "medium" and "high" are too slow for voice`)
		instruction = flag.String("instruction", voicePrompt, "system prompt")
		textMode    = flag.Bool("text", false, "no audio: read turns from stdin and print the replies")

		ort      = flag.String("ort", os.Getenv("TALKIE_ORT"), "ONNX Runtime library, a path or loader name")
		cuda     = flag.Bool("cuda", false, "run speech-to-text and text-to-speech on the GPU")
		parakeet = flag.String("parakeet", "", "directory with the Parakeet TDT 0.6B v3 ONNX files")
		int8     = flag.Bool("int8", false, "use Parakeet's INT8 files, faster on CPU")
		kokoro   = flag.String("kokoro", "", "directory with kokoro-v1.0.onnx and voices-v1.0.bin")
		voice    = flag.String("voice", "af_heart", "Kokoro voice")
		wake     = flag.String("wake", "hey computer", `wake word; "" to use Enter only`)
		input    = flag.String("input", "", "microphone: part of its name; default device if empty")
		output   = flag.String("output", "", "speaker: part of its name; default device if empty")
		noAEC    = flag.Bool("no-echo-cancel", false, "turn off echo cancellation (headset, or an echo-cancelled mic)")
		barge    = flag.String("barge-in", "wake", `what interrupts the agent by voice: "voice", "wake" or "off"`)
		key      = flag.String("key", "", "keyboard event device whose Pause key interrupts, e.g. /dev/input/by-id/...-event-kbd")
		noChime  = flag.Bool("no-chime", false, "turn off the ready chime that measures each session's audio latency")
		latency  = flag.Duration("latency", 0, "speaker-to-microphone round trip (default 100ms)")
		verbose  = flag.Bool("v", false, "log every state change")
	)
	flag.Parse()
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cm, err := newChatModel(ctx, *baseURL, *modelID, *apiKey, *reasoning)
	if err != nil {
		fail(err)
	}
	runner, err := newRunner(ctx, cm, *instruction)
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

	if *ort == "" || *parakeet == "" || *kokoro == "" {
		fail(fmt.Errorf("-ort, -parakeet and -kokoro are required, or use -text (see -h)"))
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
		ORTLibrary: *ort,
		CUDA:       *cuda,
		Parakeet:   talkie.ParakeetConfig{Dir: *parakeet, INT8: *int8},
		Kokoro: talkie.KokoroConfig{
			ModelPath:  filepath.Join(*kokoro, "kokoro-v1.0.onnx"),
			VoicesPath: filepath.Join(*kokoro, "voices-v1.0.bin"),
			Voice:      *voice,
		},
		WakeWord:                *wake,
		InputDevice:             *input,
		OutputDevice:            *output,
		DisableEchoCancellation: *noAEC,
		DisableReadyChime:       *noChime,
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
			if err := onPause(keys, t.Interrupt); err != nil && ctx.Err() == nil {
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
// model's thinking off.
func newChatModel(ctx context.Context, baseURL, modelID, apiKey, reasoning string) (model.ToolCallingChatModel, error) {
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:         baseURL,
		Model:           modelID,
		APIKey:          apiKey,
		ReasoningEffort: openai.ReasoningEffortLevel(reasoning),
	})
}

// newRunner wraps the model in a streaming eino ChatModelAgent with no tools.
func newRunner(ctx context.Context, cm model.ToolCallingChatModel, instruction string) (*adk.Runner, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "TalkieVoiceAgent",
		Description: "A voice assistant whose replies are spoken by talkie.",
		Instruction: instruction,
		Model:       cm,
	})
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "example-eino-talkie-agent:", err)
	os.Exit(1)
}

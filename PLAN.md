# Plan: example-eino-talkie-agent

## Goal
Hear an eino agent talk and listen through talkie-core.
- You say the wake word and ask something. talkie transcribes it.
- An eino `ChatModelAgent` backed by an LLM answers. talkie speaks the answer sentence by sentence while it streams in.
- You can interrupt. The agent's next turn knows how much of its reply you actually heard.

This is an example and a test of talkie-core's public API (`talkie.New`, `Agent`, `Turn`, `Reply`, `Interruption`, `Run`) with a real agent framework. It is not the Phase 2 network service.

## Hand-off notes for the implementing agent
- **Read first:**
  - `../talkie-core/agent.go` (the Agent contract) and `../talkie-core/talkie.go` (`Config`, `BargeIn`, `New`);
  - `../talkie-core/cmd/talkie-demo/main.go`, the template for every talkie flag, `talkie.New`, `Run`, the Enter and Pause key handling, and Ctrl-C. Copy its structure rather than reinventing it;
  - `~/experiments/golang-agents/eino/example/chapter2/main.go`, the template for the eino model, agent, runner and stream iteration.
- **Don't modify talkie-core.** If the example seems to need a talkie-core change, stop and report what and why instead of editing it.
- **What the implementing agent can verify itself:**
  - `go vet ./...` and `go test -race ./...`;
  - `live_test.go` against Ollama;
  - the `-text` mode end to end.
  - It can also start the audio program to check that it loads models and plays the ready chime. Keep such runs short (`timeout 15`), since they play sound in the owner's office.
- **What it can't verify:** whether the conversation sounds right. The manual check at the end is the owner's. Report when it's ready for that.
- **Commits:** authored by the owner only. No `Co-Authored-By`, no "Generated with", and no mention of Claude or Anthropic anywhere in commits. Don't push and don't create remotes.

## Facts checked (2026-10-14)
- **Ollama:** `http://orin.runcible.io:11434/v1` is reachable. It serves `qwen3.5:9b` and `lukey03/qwen3.5-9b-abliterated:latest`.
- **qwen3.5:9b** reports these capabilities: completion, vision, tools, thinking. It is Q4_K_M.
- **Thinking must be off for voice.**
  - With `"reasoning_effort": "none"`, a short request finished in 0.66 s, with no reasoning text.
  - With the default (thinking on), the same request didn't finish within 120 s. That may have included a cold model load, so step 6 measures it again.
- **eino** (the same pins as `golang-agents/eino/example/chapter2`): `github.com/cloudwego/eino v0.8.6`, plus `github.com/cloudwego/eino-ext/components/model/openai v0.1.11`.
  - `ChatModelConfig.ReasoningEffort` is a string type. Its constants are only low, medium and high, but `openai.ReasoningEffortLevel("none")` sends "none".
  - If that fails, the fallback is `ExtraFields: {"reasoning_effort": "none"}`.
- **talkie-core** isn't published yet. Use the local path: `replace github.com/androiddrew/talkie-core => ../talkie-core`.
- **talkie's Agent contract** (`talkie-core/agent.go`):
  - `Respond(ctx, Turn, *Reply)` is called once per transcript, on talkie's goroutine. Whatever is written to `reply` is spoken as it is written.
  - `ctx` is cancelled on an interruption or when the session ends.
  - The next `Turn.Interrupted` gives `Heard`, `Cut`, `Unspoken` and `Played`.
  - `reply.Then(ThenListen|ThenEnd|ThenWait)` chooses what happens after the reply.
  - `Turn.Reply` is the reply's ID, matching `Event.Reply`.
- **talkie events:** `talkie.Config.OnEvent func(talkie.Event)` receives the conversation's events (talkie-core `events.go`, commit `6d9175d`):
  - state changes (idle, listening, hearing, transcribing, thinking, speaking);
  - the wake word, transcripts (with STT time), and replies started, finished and stopped;
  - interruptions (by speech, the wake word or the app), latency, room changed, and errors.
  - It is called on Run's goroutine, which also runs the audio, so it must return quickly.

## Module
- Directory `./example-eino-talkie-agent`, module `github.com/androiddrew/example-eino-talkie-agent`, Go 1.25, CGO (through talkie-core).
- Dependencies: talkie-core (local `replace`), eino v0.8.6 and eino-ext openai v0.1.11.
- Layout:
  ```
  main.go          flags, talkie.New, Run, signal handling
  agent.go         the talkie.Agent adapter around an eino runner
  history.go       per-session history, with interruption fix-ups
  speech.go        filters model text into something Kokoro should read
  latency.go       joins talkie events and agent timings into per-reply latency
  agent_test.go    adapter tests against a fake eino ChatModel
  live_test.go     one real turn against Ollama, gated by EINO_TALKIE_TEST_LLM
  README.md
  ```
- `git init` locally. Commits are authored by the owner only, with no tool attribution, and the owner pushes.

## Configuration (flags, with env fallbacks as in chapter2)
| Flag | Env | Default |
|---|---|---|
| `-base-url` | `OPENAI_BASE_URL` | `http://orin.runcible.io:11434/v1` |
| `-model` | `OPENAI_API_MODEL` | `qwen3.5:9b` |
| `-api-key` | `OPENAI_API_KEY` | `ollama` (Ollama ignores it) |
| `-reasoning` | | `none` (`low`/`medium`/`high` turn thinking on, too slow for voice) |
| `-instruction` | | the voice system prompt below |
| `-text` | | off. On: no audio and no ORT; read lines from stdin, run each as a `talkie.Turn` (one session) through the same adapter, and print what would be spoken |

The talkie flags mirror `talkie-demo`:
- `-ort`, `-cuda`, `-parakeet`, `-kokoro`, `-voice`, `-wake` (default "hey computer");
- `-input`, `-output`;
- `-barge-in` (default **`wake`**, the owner's preferred mode), `-key` (Pause key), `-no-chime`, `-latency`, `-v`.

Room training stays in `talkie-demo -train`. Profiles are keyed by device names in `~/.config/talkie/rooms`, so this example uses the same profile.

**Voice system prompt (default):** "You are a voice assistant. Your replies are spoken aloud, so answer in one to three short, plain sentences. Never use markdown, lists, headings, code, emoji or URLs. If the user says goodbye, say goodbye briefly."

## The adapter (`agent.go`)
```go
type Agent struct {
    runner  *adk.Runner
    history *history
    log     *slog.Logger
}
func (a *Agent) Respond(ctx context.Context, turn talkie.Turn, reply *talkie.Reply) error
```
Per turn:
1. **History:** take the session's history.
   - A new `turn.Session` starts an empty one. The conversation lasts as long as talkie's session: until a goodbye or the follow-up timeout.
   - If `turn.Interrupted` is set, replace the previous assistant message with what was heard (see below).
   - Then append `schema.UserMessage(turn.Text)`.
2. **Run:** `runner.Run(ctx, msgs)` with `EnableStreaming: true`. Iterate the events as chapter2 does:
   - Skip anything that isn't an assistant message.
   - Forward each frame's `Content`, after the speech filter, with `reply.WriteString`.
   - Ignore `ReasoningContent` and tool calls.
3. **Interruption:** when `ctx` is done (barge-in, a key, or the session ending):
   - `Close()` the `MessageStream` so the HTTP stream to Ollama stops generating;
   - stop iterating and return `ctx.Err()`.
   - A `Write` after cancellation fails too; treat that error as the same stop.
4. **Record:** append the assistant message, with everything written, as *provisional*. If the next turn arrives with `Interrupted`, the fix-up replaces it with what was heard:
   - `Heard`;
   - then, if `Cut` is non-empty, `" [cut off while saying: " + Cut + "]"`;
   - then `" [interrupted]"`.
   - If the user interrupted while the agent was still thinking (`Played == 0`), the message is dropped instead.
5. **Goodbye:** if the user's text is a goodbye ("goodbye", "bye", "that's all", "stop listening"), call `reply.Then(talkie.ThenEnd)` so the session closes after the reply. A tool the model can call is a possible later step; this example keeps no tools.

**Concurrency:** talkie can start a new turn while the cancelled one is still unwinding (a continuation, or a barge-in). `history` holds a mutex, and each `Respond` first waits for the previous turn's `done` channel, so the fix-up sees the provisional message.

**Errors:** when `Respond` returns an error, talkie emits an `error` event, says nothing and goes back to listening (`TestAgentError`). So when the model call fails (Ollama unreachable, unknown model), the adapter writes "Sorry, I couldn't reach the language model." to the reply first, then returns the error so it is logged.

**Warm-up:** at startup, before `Run`, call the chat model directly: `cm.Generate(ctx, []*schema.Message{schema.UserMessage("hi")}, model.WithMaxTokens(1))`. Ollama then loads the model before the first question. Log how long it took. A cold load on the Orin can take tens of seconds.

## Speech filter (`speech.go`)
Even when told not to, models emit markdown. Filter the stream deltas, with state carried across chunk boundaries:
- drop `*`, `_`, `#` and backticks used as markup;
- turn `- ` and `1. ` list starts into sentence breaks;
- drop URLs;
- collapse whitespace.

First check what go-kokoro's text normalizer already handles, and filter only what it reads aloud badly.

## Latency logging (`latency.go`)
Join talkie's events with the agent's own timings, keyed by reply ID, and log one `turn_latency` line per reply at Info.
- **From `Config.OnEvent`:**
  - `EventSpeechStarted` and `EventTranscript` (with `STT` and `Speech`); the end of speech is about `Time - STT`;
  - `EventReplyStarted` (`Reply`, `Time`): the first audio;
  - `EventReplyFinished`, `EventReplyStopped` and `EventInterrupted`.
- **From the adapter:** the time `Respond` was called (`turn.Reply`), the first content delta, and the first delta containing a sentence end.
- **Logged:**
  - `stt`;
  - `first_token` (Respond to the first delta);
  - `first_sentence`;
  - `first_audio` (the first delta to `EventReplyStarted`);
  - `end_to_end` (the end of speech to `EventReplyStarted`);
  - `chars`, `total`, and `interrupted_by`, if the reply was interrupted.
- **Concurrency:** OnEvent runs on talkie's audio goroutine and `Respond` on another, so the tracker sends both kinds of record over a buffered channel to one goroutine that joins them. OnEvent never blocks: if the channel is full, the record is dropped and counted.
- **Display:** OnEvent also prints state changes as a one-line status in the terminal (`[listening]`, `[thinking]`, `[speaking]`), so the manual check shows what talkie thinks is happening.

The budget for a short answer is about 1 s from the end of speech to the first audio: 0.3 s STT (CUDA), 0.5 s first token, 0.2 s Kokoro's first chunk.

## Tests
- **`agent_test.go`** (always runs, no network, no ORT): a fake eino `model.ToolCallingChatModel` that streams scripted chunks (content and reasoning), with a delay per chunk and the ability to block.
  - `talkie.Reply` can't be built outside talkie-core (its `turn` field is unexported). So the adapter's core is `respond(ctx, turn, out speaker)`, where `speaker` is a small interface: `WriteString(string) (int, error)` and `Then(talkie.Then)`. `*talkie.Reply` satisfies it.
  - `Respond` is a one-line wrapper. Tests pass a recording fake that can fail writes after a cancel, as `Reply` does.
  - talkie-core needs no change.
  - Content deltas arrive in order; reasoning is never written.
  - Cancelling ctx mid-stream closes the fake's stream and returns quickly (under 100 ms).
  - Interruption fix-up: the history holds `Heard` + `[cut off …]` + `[interrupted]`; with `Played == 0` the assistant message is dropped.
  - A new session starts an empty history.
  - A turn that starts before the cancelled one returns waits for it, under `-race`.
  - "Goodbye" sets ThenEnd.
  - The speech filter: table cases, including markup split across deltas.
  - The latency tracker, fed scripted events and agent timings, including out of order and with a full channel. It logs the right durations and never blocks the OnEvent side.
- **`live_test.go`** (gated by `EINO_TALKIE_TEST_LLM=http://orin.runcible.io:11434/v1`): one real turn through the adapter with no audio.
  - It asserts a non-empty answer with no reasoning text, and logs the time to first token.
  - A second case cancels after the first delta and asserts that `Respond` returns within 1 s.

## Steps
1. Scaffold the module: go.mod with the `replace`, flags, and `talkie.New` with a stub agent that says "I heard you say …". It should run on the desktop exactly like talkie-demo.
2. The eino model and runner with the voice prompt and `reasoning_effort: none`, plus the warm-up and `-text` mode. Check the model path without audio: `printf 'what is the capital of France\nand of Spain\n' | go run . -text`.
3. The adapter with streaming, history, interruption fix-ups and goodbye, plus `agent_test.go`.
4. The speech filter and its tests.
5. Latency logging and the status line, both from `OnEvent`.
6. `live_test.go` against Ollama. Also measure the thinking-on first-token time once, to record why it's off.
7. README: prerequisites (ORT, models, Ollama), the run command, the flags, and the room-training note.

## Manual check (the goal)
```
go run . -cuda -ort /opt/onnxruntime/1.22.0-cuda12/lib/libonnxruntime.so \
  -parakeet /home/toor/experiments/go-parakeet/runtime-assets/parakeet \
  -kokoro /home/toor/experiments/go-kokoro/tmp/kokoro -v
```
1. The ready chime plays and the log shows the model warm-up.
2. "Hey computer, what's the tallest mountain in Africa?" gets a spoken answer of one to three sentences, with first audio about 1 s after you stop.
3. "Tell me about the history of Rome" gets a long answer. Interrupt it with "hey computer, stop" (wake-word barge-in), or with Enter or Pause. The audio stops at once, and Ollama stops generating (check the Ollama log or the token count).
4. Ask "what were you just saying?" The model should only know what you heard, with the cut-off marker.
5. "Goodbye" gets a short goodbye and ends the session; the next question needs the wake word.
6. `-model lukey03/qwen3.5-9b-abliterated:latest` works the same way.

## Later
- Tools (time, weather, lights) through `adk` tool calling, with a spoken "let me check" and `ThenWait`.
- The same agent over the Phase 2 network service (`-mode ws`) once `walkie-talkie` exists.
- Running Ollama and talkie together on the Orin, measuring memory.

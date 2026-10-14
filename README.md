# example-eino-talkie-agent

Talk with an [eino](https://github.com/cloudwego/eino) agent through
[talkie-core](../talkie-core). Say the wake word and ask something. talkie
transcribes it, an eino `ChatModelAgent` answers through an OpenAI-compatible
LLM (Ollama by default), and talkie speaks the answer a sentence at a time as
it streams in. You can interrupt it, and on the next turn the agent knows how
much of its reply you actually heard.

It is an example, and a test of talkie-core's public API (`talkie.New`,
`Agent`, `Turn`, `Reply`, `Interruption`, `Run`, `Config.OnEvent`) with a real
agent framework.

## Prerequisites

- **Go 1.25** and a C toolchain (talkie-core uses CGO).
- **talkie-core** checked out next to this directory. It isn't published yet,
  so `go.mod` replaces it with `../talkie-core`.
- **ONNX Runtime 1.22**. Use a CUDA build for `-cuda`.
- **Models.** You need Parakeet TDT 0.6B v3 (`nemo128.onnx`,
  `encoder-model.onnx` with its `.onnx.data`, `decoder_joint-model.onnx`) and
  Kokoro v1.0 (`kokoro-v1.0.onnx`, `voices-v1.0.bin`). The wake word and VAD
  models are embedded in their Go modules.
- **An OpenAI-compatible LLM server.** The default is Ollama at
  `http://orin.runcible.io:11434/v1` with `qwen3.5:9b`.

## Run it

```
go run . -cuda -ort /opt/onnxruntime/1.22.0-cuda12/lib/libonnxruntime.so \
  -parakeet /home/toor/experiments/go-parakeet/runtime-assets/parakeet \
  -kokoro /home/toor/experiments/go-kokoro/tmp/kokoro -v
```

At start-up it asks the model for one token, so Ollama loads the model before
your first question. Then talkie loads its models and plays the ready chime.

- Say "hey computer, what's the tallest mountain in Africa?" to get a short
  spoken answer.
- To interrupt a long answer, say "hey computer" again, press Enter, or press
  Pause with `-key`. Ask "what were you just saying?" and the model knows only
  what you heard.
- "Goodbye" (or "bye", "that's all", "stop listening") gets a short goodbye
  and ends the session. The next question needs the wake word.

Each reply logs a `turn_latency` line:

| Field | Meaning |
|---|---|
| `stt` | speech-to-text time |
| `first_token` | from the agent being called to the model's first content |
| `first_sentence` | from the agent being called to the first complete sentence |
| `first_audio` | from the first content to the first audio |
| `end_to_end` | from the end of your speech to the first audio |
| `chars`, `total` | the reply's length and duration |
| `interrupted_by` | `speech`, `wake_word` or `app`, if the reply was interrupted |

talkie's states (`[listening]`, `[thinking]`, `[speaking]`, ...) print to
stdout as they change.

### Without audio

`-text` runs the same agent with no audio and no ONNX Runtime. Each line of
stdin is a turn in one session, and the reply is printed as it would be
spoken:

```
printf 'what is the capital of France\nand of Spain\n' | go run . -text
```

## Flags

| Flag | Env | Default | |
|---|---|---|---|
| `-base-url` | `OPENAI_BASE_URL` | `http://orin.runcible.io:11434/v1` | OpenAI-compatible API |
| `-model` | `OPENAI_API_MODEL` | `qwen3.5:9b` | e.g. `lukey03/qwen3.5-9b-abliterated:latest` |
| `-api-key` | `OPENAI_API_KEY` | `ollama` | Ollama ignores it |
| `-reasoning` | | `none` | sent as `reasoning_effort`. `low`, `medium` and `high` turn thinking on (see below) |
| `-instruction` | | a voice prompt | the system prompt |
| `-text` | | off | no audio: stdin turns, printed replies |

The talkie flags are the same as `talkie-demo`'s: `-ort` (or `TALKIE_ORT`),
`-cuda`, `-parakeet`, `-int8`, `-kokoro`, `-voice`, `-wake` (default "hey
computer"), `-input`, `-output`, `-no-echo-cancel`, `-barge-in` (default
`wake`, so only the wake word interrupts by voice; `voice` also lets you talk
over the agent, and `off` leaves only Enter or Pause), `-key`, `-no-chime`,
`-latency` and `-v`.

## Room training

Train talkie to the room with talkie-demo:

```
cd ../talkie-core && go run ./cmd/talkie-demo -train -ort ... -kokoro ...
```

This example uses the same profile, because profiles are keyed by device
names in `~/.config/talkie/rooms`. To list the devices, run
`talkie-demo -list`.

## Why thinking is off

`qwen3.5:9b` is a thinking model. Measured against Ollama on the Orin, with
the model warm (`TestLiveThinkingFirstToken`):

| | First answer token | Total |
|---|---|---|
| `-reasoning none` | 0.39 s | 2.6 s |
| thinking on (the default) | 59 s, after 7,000 characters of reasoning | 60 s |

Thinking on is far too slow for voice. With thinking off, the first sentence
was complete about 1 s after the agent was called.

## How it works

- `agent.go` is the `talkie.Agent` adapter. Each turn appends the user's text
  to the session's history and runs the eino runner with streaming on. It
  forwards each assistant content delta, filtered, to the reply, and ignores
  reasoning and tool calls. On an interruption, the context is cancelled: the
  adapter closes the model's stream, which cancels the HTTP request so Ollama
  stops generating, and returns. If the model can't be reached, it says
  "Sorry, I couldn't reach the language model."
- `history.go` keeps the history for one talkie session and starts it empty
  when the session changes. A reply is recorded provisionally. If the next
  turn arrives with `Turn.Interrupted`, the reply is replaced by what was
  heard, `[cut off while saying: …]` and `[interrupted]`. If the user spoke
  before any of it played, the reply is dropped. Turns run in order: a turn
  that starts while the cancelled one is still returning waits for it.
- `speech.go` filters the model's text. go-kokoro already reads markdown as
  prose, so the filter only drops what it reads badly: URLs (spelled out
  letter by letter), fenced code, list numbers at the start of a line (talkie
  would speak "1." as a sentence) and `<think>` blocks.
- `latency.go` joins talkie's events from `Config.OnEvent` with the agent's
  timings over a buffered channel. OnEvent never blocks: when the channel is
  full, the record is dropped and counted.

## Tests

```
go vet ./... && go test -race ./...
EINO_TALKIE_TEST_LLM=http://orin.runcible.io:11434/v1 go test -run Live -v ./...
EINO_TALKIE_TEST_LLM=... EINO_TALKIE_TEST_THINKING=1 go test -run LiveThinking -v ./...
```

The unit tests use a fake eino chat model and need neither a network nor ONNX
Runtime. The live tests run one real turn, and a turn cancelled after its
first delta, against the server.

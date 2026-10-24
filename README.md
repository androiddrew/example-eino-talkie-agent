# example-eino-talkie-agent

Talk with an [eino](https://github.com/cloudwego/eino) agent through
[talkie-core](https://github.com/androiddrew/talkie-core). Say the wake word and ask something. talkie
transcribes it, an eino `ChatModelAgent` answers through an OpenAI-compatible
LLM (Ollama by default), and talkie speaks the answer a sentence at a time as
it streams in. You can interrupt it, and on the next turn the agent knows how
much of its reply you actually heard.

The agent can use **skills**: it turns the office lights on, and searches the
web to answer questions about facts instead of relying on what the model
remembers. Ask it "what skills do you have?", "turn the lights on" or "who
won the 2026 World Cup?".

It is an example, and a test of talkie-core's public API (`talkie.New`,
`Agent`, `Turn`, `Reply`, `Interruption`, `Run`, `Config.OnEvent`) with a real
agent framework.

## Prerequisites

- **Go 1.25** and a C toolchain (talkie-core uses CGO).
- **talkie-core** v0.1.0, which `go` downloads.
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

### The full voice setup

Everything together: speech in and out on the GPU, `gemma4:e4b` on the Orin,
the wake word, wake-word barge-in, and both skills.

Before you start, check:
- **Ollama** serves the model:
  `curl -s http://orin.runcible.io:11434/api/tags | grep gemma4:e4b`
- **SearXNG** is running, for web search:
  `curl -s http://127.0.0.1:8888/healthz` prints `OK`
- **`HASSIO_TOKEN`** is set in this shell, for the office lights:
  `test -n "$HASSIO_TOKEN" && echo set`
- **The skills** are installed: `ls ~/.config/eino-talkie-agent/skills`
  lists `office-lights` and `web-search`
- **The room** is trained, which only needs doing once per room and devices:
  `go run github.com/androiddrew/talkie-core/cmd/talkie-demo@v0.1.0 -train -ort ... -kokoro ...`

Then launch:

```
go run . \
  -model gemma4:e4b \
  -cuda -ort /opt/onnxruntime/1.22.0-cuda12/lib/libonnxruntime.so \
  -parakeet /home/toor/experiments/go-parakeet/runtime-assets/parakeet \
  -kokoro /home/toor/experiments/go-kokoro/tmp/kokoro \
  -voice af_heart \
  -wake "hey computer" \
  -barge-in wake \
  -key /dev/input/by-id/usb-Logitech_G413_Carbon_Mechanical_Gaming_Keyboard_186631473431-event-kbd
```

`-key` lets the Pause key interrupt even when the terminal isn't focused. It
needs read access to the keyboard: run `sudo usermod -aG input $USER` and log
in again, or leave the flag out and press Enter instead. Add `-v` to log
every state change, `-input` and `-output` to pick devices by part of their
names, and `-skills ""` to run without skills.

When the chime plays, try:
1. "Hey computer, what skills do you have?"
2. "Hey computer, turn the lights on." Within the follow-up window you can
   go on without the wake word: "Make them warm."
3. "Hey computer, who won the 2026 World Cup?" It searches the web, then
   answers.
4. "Hey computer, tell me about the history of Rome." Interrupt it with "hey
   computer" or Pause, then ask "what were you just saying?"
5. "Goodbye" ends the session.

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
| `-temperature` | | `0.2` | sampling temperature (see [Models](#models)); negative leaves it to the server |
| `-skills` | | `$XDG_CONFIG_HOME/eino-talkie-agent/skills` | the skills directory; `""` for none |
| `-instruction` | | a voice prompt | the system prompt |
| `-text` | | off | no audio: stdin turns, printed replies |

The talkie flags are the same as `talkie-demo`'s: `-ort` (or `TALKIE_ORT`),
`-cuda`, `-parakeet`, `-int8`, `-kokoro`, `-voice`, `-wake` (default "hey
computer"), `-input`, `-output`, `-no-echo-cancel`, `-barge-in` (default
`wake`, so only the wake word interrupts by voice; `voice` also lets you talk
over the agent, and `off` leaves only Enter or Pause), `-key`, `-no-chime`,
`-latency` and `-v`.

## Skills

A skill is a directory in `~/.config/eino-talkie-agent/skills` (or under
`$XDG_CONFIG_HOME`) containing a `SKILL.md` in the
[Agent Skills](https://agentskills.io) format, plus its executables in
`scripts/`:

```
~/.config/eino-talkie-agent/skills/office-lights/
├── SKILL.md
└── scripts/
    └── office-lights.sh -> ~/.scripts/office-lights.sh
```

`SKILL.md` has YAML front matter with a `name` (letters, digits, `-` and
`_`; it defaults to the directory's name) and a `description`, then
instructions for the model. The repository's [`skills`](skills) directory
has three skills:
- `office-lights` controls the office lights;
- `web-search` searches the web;
- `kids-story` tells a children's story of the kind asked for.

To install `office-lights`:

```
mkdir -p ~/.config/eino-talkie-agent/skills/office-lights/scripts
cp skills/office-lights/SKILL.md ~/.config/eino-talkie-agent/skills/office-lights/
ln -s ~/.scripts/office-lights.sh ~/.config/eino-talkie-agent/skills/office-lights/scripts/
```

### Web search

`web-search` searches through a [SearXNG](https://docs.searxng.org)
instance, a self-hosted metasearch engine that needs no API key. Its script,
[`skills/web-search/scripts/web-search`](skills/web-search/scripts/web-search),
is Python with only the standard library. It prints the top five results,
each with a title, a snippet and its source, for the model to answer from.
`SEARXNG_URL` selects the instance; the default is `http://127.0.0.1:8888`.

To run SearXNG on this machine, listening only locally, with the JSON API
on, write `~/.config/searxng/settings.yml`:

```yaml
use_default_settings: true
server:
  secret_key: "..."         # openssl rand -hex 32
  limiter: false
  image_proxy: false
search:
  safe_search: 1
  formats: [html, json]
outgoing:
  request_timeout: 1.5      # a voice answer waits on the search
  max_request_timeout: 2.0
engines:
  - name: brave             # rate-limits SearXNG
    disabled: true
```

Then start it:

```
docker run -d --name searxng --restart unless-stopped -p 127.0.0.1:8888:8080 \
  -v ~/.config/searxng:/etc/searxng searxng/searxng:latest
```

The container takes ownership of `settings.yml`. To edit it later, run
`docker exec -it searxng vi /etc/searxng/settings.yml`, then
`docker restart searxng`. Searches take 0.5 to 1.5 s. Some engines refuse
SearXNG (DuckDuckGo sometimes shows a CAPTCHA), but Google's results come
through.

To install the skill:

```
mkdir -p ~/.config/eino-talkie-agent/skills
cp -r skills/web-search ~/.config/eino-talkie-agent/skills/
```

The skill tells the model to search before answering any question about
facts in the world, especially anything recent, and not to search for
chit-chat. Every turn's system prompt includes today's date, so "latest"
and "today" mean something. With `gemma4:e4b`, a searched answer starts
speaking about 2 s after the question, against about 0.3 s for an answer from
memory.

### Kids' stories

`kids-story` tells a children's story. The model calls it with the request
in a few words, such as `["short bedtime story for Mia"]`, and its script,
[`skills/kids-story/scripts/kids-story`](skills/kids-story/scripts/kids-story),
prints a brief for the model to tell the story from. The request's words
pick the kind of story (bedtime, funny, spooky, adventure, fairy tale,
animal, space, or one with a lesson, up to two at once), its length ("short",
"long") and the child's age. The brief adds a random hero, place and object
for anything the request leaves open, so stories differ even at temperature
0.2.

To install it:

```
cp -r skills/kids-story ~/.config/eino-talkie-agent/skills/
```

### How skills work

Before each turn the agent reads the skills, so a new or edited skill applies
on the next question without a restart. Each skill's description and
instructions go in the system prompt, and each skill becomes a tool named
after it. The tool runs one of the skill's scripts with the arguments the
model gives it. The script runs directly, without a shell, from the skill's
directory, for at most 30 seconds. A skill with one script takes only `args`;
a skill with several also takes the script's name. The model has no other way
to run anything.

The scripts inherit the agent's environment. `office-lights.sh` needs
`HASSIO_TOKEN`, which `~/.bashrc` sets. Note that it exits 0 even when the
request to Home Assistant fails, so the agent can't tell when it didn't work.

A tool error, such as a missing script, goes back to the model as the tool's
result, so it can tell you what went wrong instead of the turn failing. Each
turn's tool calls and results stay in the session's history.

### Why not eino's skill middleware

eino v0.8.6 has a skill middleware (`adk/middlewares/skill`). It loads a
skill's instructions only when the model asks for them with a tool call, and
it leaves running scripts to another tool. Tested with `qwen3.5:9b`, it was
slower and less reliable for voice:

| | First word after "turn the lights on" | Live test |
|---|---|---|
| eino's middleware + a `run_skill_script` tool | about 5 s | 3 of 4 runs passed |
| instructions in the prompt + `run_skill_script` | about 3.2 s | 10 of 11 runs passed |
| instructions in the prompt + a tool per skill, temperature 0.2 | about 1.9 s | every run after the final SKILL.md, over 100 requests |

With the middleware, the model sometimes loaded the skill and then said the
lights were on without running the script. Its tool description also uses
"pdf" and "xlsx" as example skill names, which the model then claimed to
have. A voice assistant's skills are few and short, so they all go in the
prompt.

## Models

The live tests ran against three models on Ollama on the Orin:

| Model | Plain answer, first token | Light request, first word | Skills |
|---|---|---|---|
| `gemma4:e4b` | 0.35 s | 0.5 s | every request, at 0.2 and 1.0 |
| `gemma4:12b` | 2 s warm | 1.4 s | every request, at 0.2 and 1.0; ran out of memory (below) |
| `qwen3.5:9b` | 0.4 s | 1.9 to 2.6 s | every request at 0.2 only |

- **`gemma4:e4b`** is the best fit for voice: the fastest, and reliable with
  skills. Use `-model gemma4:e4b`. Told not to use markdown or code, it wrote
  its tool calls as text, such as `office-lights{args:["on"]}`, which talkie
  would have spoken. The skills prompt now says that rules about speaking
  don't apply to tool calls, and since then it has made real calls every time.
- **`qwen3.5:9b`** answers well. With skills, at the server's default
  temperature of 1.0, it sometimes skipped the tool and said the lights were
  on anyway. Once that happened, the history taught it to keep doing so for
  the rest of the session. At temperature 0.2 it called the tool for every
  request in the live test. It needs `-temperature 0.2`, the default.
- **`gemma4:12b`** called the tool for every request, at both 0.2 and 1.0,
  and started speaking about 0.5 s sooner than qwen after a light request
  (about 1.4 s).
  Use `-model gemma4:12b`. Thinking is on by default in this model, so it
  needs `-reasoning none` (the default) too. With Ollama's settings on the
  Orin (a 98,304-token context, 3 parallel slots), the kernel's out-of-memory
  killer stopped its `llama-server` after about eight turns of one session.
  The request then failed, and the reload took about 15 s. Freeing memory by
  stopping another container didn't help: it was killed in three runs out of
  three, after two turns. A smaller context and one slot
  (`OLLAMA_CONTEXT_LENGTH=8192`, `OLLAMA_NUM_PARALLEL=1`, and perhaps
  `OLLAMA_MAX_LOADED_MODELS=1` on the container) should avoid that.

All three models rely on the fix-ups in the code, which are not specific to
any one model:
- the turn's tool calls stay in the history;
- what a model says after a tool call in the same message isn't spoken,
  because it can't know the result yet;
- tool errors go back to the model.

## Room training

Train talkie to the room with talkie-demo:

```
go run github.com/androiddrew/talkie-core/cmd/talkie-demo@v0.1.0 -train -ort ... -kokoro ...
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
  reasoning. Tool calls and their results are kept for the history, and text
  after a tool call in the same message isn't spoken. On an interruption, the context is cancelled: the
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
- `skills.go` reads the skills, builds the system prompt and the tool for
  each skill, and turns tool errors into results.
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
Runtime. The live tests run one real turn, a turn cancelled after its first
delta, and ten light requests in different words. The light requests use the
repository's office-lights skill with a script that only records its
arguments, so no real lights change. `TestLiveWebSearch` uses the real
web-search skill, so it needs SearXNG running. It checks that the model
searches for facts, doesn't search for chit-chat, and still uses the lights
skill. Set `OPENAI_API_MODEL` to test another
model, and `EINO_TALKIE_TEST_TEMP` to test another temperature.

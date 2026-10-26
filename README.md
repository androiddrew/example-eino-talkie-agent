# example-eino-talkie-agent

Talk with an [eino](https://github.com/cloudwego/eino) agent through
[talkie-core](https://github.com/androiddrew/talkie-core). Say the wake word
and ask something. talkie transcribes it, an eino `ChatModelAgent` answers through an OpenAI-compatible
LLM (Ollama by default), and talkie speaks the answer a sentence at a time as
it streams in. You can interrupt it, and on the next turn the agent knows how
much of its reply you actually heard.

The agent can use **skills**: it turns the office lights on through Home
Assistant, searches the web to answer questions about facts instead of
relying on what the model remembers, and tells children's stories. Ask it
"what skills do you have?", "turn the lights on" or "who won the 2026 World
Cup?".

It is an example, and a test of talkie-core's public API (`talkie.New`,
`Agent`, `Turn`, `Reply`, `Interruption`, `Run`, `Config.OnEvent`) with a real
agent framework.

You need **Linux**, a microphone and a speaker, and an **OpenAI-compatible
LLM server**. The default is Ollama at `http://localhost:11434/v1` with
`gemma4:e4b` (`ollama pull gemma4:e4b`). Set `OLLAMA_KEEP_ALIVE=-1` on the
Ollama server, so it keeps the model loaded: by default it unloads it after
5 minutes idle, and the next question waits about 30 s for it to load again.
The agent asks for one token when it starts, which loads it.

Run the agent in Docker, or install it on the machine; both keep the same
files in the same places, so you can switch between them.

| On the host | What |
|---|---|
| `~/.config/eino-talkie-agent/config.yaml` | settings (see [Files and directories](#files-and-directories)) |
| `~/.config/eino-talkie-agent/env` | the skills' secrets, such as `HASSIO_TOKEN`, as `KEY=value` lines |
| `~/.config/eino-talkie-agent/skills/` | the skills |
| `~/.config/talkie/rooms/` | the room profiles from training |
| `~/.local/share/eino-talkie-agent/` | the models, about 1 GB; anywhere you like, such as `/data` |

## Run it in Docker

[`compose.yaml`](compose.yaml) runs the agent and the
[SearXNG](https://docs.searxng.org) its web-search skill uses. Clone this
repository; the commands below run in it.

### 1. Choose an image and write `.env`

| Image | For | `.env` |
|---|---|---|
| `eino-talkie-agent:cpu` | any x86-64 or arm64 machine | as it comes |
| `eino-talkie-agent:cuda` | x86-64 with an NVIDIA GPU, CUDA 12 | `COMPOSE_FILE=compose.yaml:docker/compose.cuda.yaml` |
| `eino-talkie-agent:jetson-xavier` | Jetson Xavier NX or AGX, JetPack 5 | `COMPOSE_FILE=compose.yaml:docker/compose.jetson.yaml` |
| `eino-talkie-agent:jetson-orin` | Jetson Orin, JetPack 7.2 | the same, and `TALKIE_JETSON=jetson-orin` |

Each image holds the agent, a matching ONNX Runtime, Python for the skills
and talkie-core's `get-assets.sh`; the models stay on the host. The CUDA and
Jetson images run speech-to-text and text-to-speech on the GPU.

```
cp .env.example .env
sed -i "s/^SEARXNG_SECRET=.*/SEARXNG_SECRET=$(openssl rand -hex 32)/" .env
```

Then uncomment the lines your machine needs in `.env`: `COMPOSE_FILE` for a
GPU, `TALKIE_UID` and `TALKIE_GID` if `id -u` and `id -g` aren't 1000, and
`TALKIE_DATA` to keep the models elsewhere, such as `/data/talkie`. Build the
image:

```
docker compose build agent
```

- The CUDA image needs the
  [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/).
- The Jetson images build on the Jetson itself, on
  [kokoro-run](https://github.com/androiddrew/kokoro-run)'s ONNX Runtime base
  images, which compile ONNX Runtime for the Jetson's GPU. The Xavier's is on
  Docker Hub,
  [`androiddrew/onnxruntime-jetson-xavier`](https://hub.docker.com/r/androiddrew/onnxruntime-jetson-xavier),
  and the build pulls it. The Orin's isn't published: build it once, on the
  Orin, in a kokoro-run checkout with `make image-jetson-orin-onnxruntime`
  (about 4 hours).
- `make image-cpu` builds the CPU image for both amd64 and arm64, and `make
  image-cuda`, `image-jetson-xavier` and `image-jetson-orin` the others, if you
  prefer make.

### 2. Make the host directories

Make them as yourself before the first run: Docker would otherwise create
missing ones as root.

```
mkdir -p ~/.config/eino-talkie-agent ~/.config/talkie ~/.config/pulse ~/.local/share/eino-talkie-agent
```

With `TALKIE_DATA`, make that directory instead of the last one.

### 3. Download the models

The image's `get-assets.sh` downloads Parakeet and Kokoro into the data
directory, each file pinned and checked against its SHA256:

```
docker compose run --rm --entrypoint get-assets.sh agent -d /data/eino-talkie-agent --no-ort          # CPU
docker compose run --rm --entrypoint get-assets.sh agent -d /data/eino-talkie-agent --no-ort --fp32   # GPU
```

`--no-ort` skips ONNX Runtime, which the image has. On a GPU, `--fp32` adds
Parakeet's FP32 files (2.5 GB): its INT8 files, the default, are meant for
the CPU. The agent warns if a GPU image finds only INT8.

### 4. Install the skills and settings

```
docker compose run --rm agent -install-skills
```

Settings go in `~/.config/eino-talkie-agent/config.yaml`, such as the
devices and the [sounds](#sounds-instead-of-a-screen):

```yaml
input: P610              # part of the microphone's name
output: P610
cue-listening: default
cue-thinking: default
cue-idle: default
```

The skills' secrets, such as `HASSIO_TOKEN` (see
[Office lights](#office-lights)), go in `~/.config/eino-talkie-agent/env` as
`KEY=value` lines; `chmod 600` it.

### 5. Train talkie to the room

Once per microphone and speaker. Stay quiet while it measures the room, then
say the wake word ("hey computer") five times and read a sentence aloud,
pressing Enter between the steps:

```
docker compose run --rm agent -train
```

### 6. Talk to it

```
docker compose up -d          # start the agent and SearXNG
docker compose logs -f agent  # follow it
docker compose down           # stop both
```

A short chime means it's listening. After changing `config.yaml` or `env`,
run `docker compose restart agent`; new or edited skills apply on the next
question. To try a flag once, run `docker compose run --rm agent -v`, for
example.

`docker compose up -d` doesn't start the agent again after a reboot: at boot,
Docker would start it before your sound server. The systemd user unit in
[`systemd/eino-talkie-agent-docker.service`](systemd/eino-talkie-agent-docker.service)
runs `docker compose up` in your session instead. Set its `WorkingDirectory`
to your checkout, then install it as in [Run it as a service](#run-it-as-a-service).

### Sound

The agent connects to the host's PipeWire or PulseAudio, the usual setup on
a desktop and on a Jetson with a desktop: it sees the same microphones and
speakers as your desktop, by the same names. Pick them with `input` and
`output`, by part of their names; a USB headset's name picks its microphone,
not the monitor of its speaker.

On a machine with no sound server, such as a headless Jetson, use ALSA
directly: add `:docker/compose.alsa.yaml` to `COMPOSE_FILE`, and set
`TALKIE_AUDIO_GID` to `getent group audio | cut -d: -f3`. ALSA names the
devices differently (for example `USB Audio`), and a device that a sound
server is using can't be opened.

For the Pause key, pass the keyboard in with a `compose.local.yaml` next to
`compose.yaml` (ignored by git), add `:compose.local.yaml` to `COMPOSE_FILE`,
and set `key` in `config.yaml` to the same path:

```yaml
services:
  agent:
    devices:
      - /dev/input/by-id/<your-keyboard>-event-kbd
    group_add:
      - "995"   # getent group input | cut -d: -f3
```

### Troubleshooting

- **`could not select device driver "nvidia"`:** the NVIDIA Container
  Toolkit isn't installed, or Docker hasn't been restarted since.
- **A missing `libnvidia-*.so` when the container starts:** the toolkit's
  device list is out of date after a driver update. Regenerate it with
  `sudo nvidia-ctk cdi generate --output=/var/run/cdi/nvidia.yaml` (or
  restart `nvidia-cdi-refresh.service`).
- **`XDG_RUNTIME_DIR is unset` or a missing `pulse/native`:** run compose in
  your login session, where the sound server is; `pactl info` names it.
- **No microphone or speaker:** the agent logs the devices it opened at
  start-up (`input=... output=...`); compare them with `pactl list short sources`
  and `pactl list short sinks` on the host.
- **`Permission denied` on a mounted directory:** it belongs to another user,
  or Docker created it as root; `sudo chown -R $USER` it.
- **A path in `config.yaml` doesn't apply:** the image pins `data`, `ort`,
  `parakeet`, `kokoro` and `skills` to its own paths with environment
  variables, so a `config.yaml` shared with a local install can keep the
  host's paths.

## Run it locally

You need **Go 1.25** and a C toolchain such as gcc (talkie-core uses CGO).

1. **Install the agent and its skills:**
   ```
   go install github.com/androiddrew/example-eino-talkie-agent@latest
   example-eino-talkie-agent -install-skills
   ```
   `-install-skills` writes the bundled skills into
   `~/.config/eino-talkie-agent/skills` and keeps any already there. From a
   clone, `go run .` runs the same program.
2. **Download ONNX Runtime 1.22, Parakeet TDT 0.6B v3 and Kokoro v1.0** into
   the agent's data directory with talkie-core's `get-assets.sh`, each file
   pinned and checked against its SHA256 (about 1 GB):
   ```
   curl -fsSL https://raw.githubusercontent.com/androiddrew/talkie-core/v0.2.0/scripts/get-assets.sh \
     | bash -s -- -d ~/.local/share/eino-talkie-agent
   ```
   On an NVIDIA GPU, add `--cuda --fp32` after the directory: the GPU build
   of ONNX Runtime, which needs CUDA 12 and cuDNN 9, and Parakeet's FP32
   files (2.5 GB), since its INT8 files are meant for the CPU. To keep the models elsewhere, such as
   `/data/talkie`, give that directory to `-d` and put `data: /data/talkie`
   in `config.yaml`. On a Jetson, install ONNX Runtime with CUDA yourself,
   add `--no-ort --fp32`, and set `ort` to its library.
3. **Train talkie to the room**, once per microphone and speaker: stay quiet
   while it measures the room, then say the wake word ("hey computer") five
   times and read a sentence aloud.
   ```
   example-eino-talkie-agent -train
   ```
4. **Talk to it:**
   ```
   example-eino-talkie-agent        # add -cuda with the GPU build
   ```

### Run it as a service

[`systemd/eino-talkie-agent.service`](systemd/eino-talkie-agent.service) runs
the installed agent as a systemd user service, in your session, so it uses
your sound server as it does in a terminal:

```
mkdir -p ~/.config/systemd/user
curl -fsSL -o ~/.config/systemd/user/eino-talkie-agent.service \
  https://raw.githubusercontent.com/androiddrew/example-eino-talkie-agent/main/systemd/eino-talkie-agent.service
systemctl --user daemon-reload
systemctl --user enable --now eino-talkie-agent
journalctl --user-unit eino-talkie-agent -f        # its log
```

- It runs `~/go/bin/example-eino-talkie-agent`; edit `ExecStart` if
  `go install` put it elsewhere (`go env GOBIN GOPATH`).
- Settings come from `config.yaml`, and the skills' secrets from
  `~/.config/eino-talkie-agent/env`; `systemctl --user restart
  eino-talkie-agent` after changing them. Skills apply on the next question
  without a restart.
- It starts when you log in. To start it at boot without logging in, as on a
  headless Jetson, run `sudo loginctl enable-linger $USER`.
- `systemctl --user stop eino-talkie-agent` stops it cleanly, and it restarts
  on a failure.

## Using it

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

Everything together: speech in and out on the GPU, the wake word, wake-word
barge-in, and the skills.

Before you start, check:
- **Ollama** serves the model:
  `curl -s http://localhost:11434/api/tags | grep gemma4:e4b`
- **SearXNG** is running, for web search:
  `curl -s http://127.0.0.1:8888/healthz` prints `OK`
- **Home Assistant** is set up for the office lights (see
  [Office lights](#office-lights)): `HASSIO_TOKEN`, `OFFICE_LIGHTS` and, unless
  it's at `http://homeassistant.local:8123`, `HASSIO_API`, in your shell or in
  `~/.config/eino-talkie-agent/env`
- **The skills** are installed: `ls ~/.config/eino-talkie-agent/skills`
  lists `kids-story`, `office-lights` and `web-search`
- **The room** is trained (`-train`)

Then launch it with these settings in `config.yaml`:

```yaml
cuda: true
voice: af_heart
wake: hey computer
barge-in: wake
key: /dev/input/by-id/<your-keyboard>-event-kbd
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
5. "Hey computer, tell me a short bedtime story."
6. "Goodbye" ends the session.

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

### Sounds instead of a screen

Without a screen, you can't see when the agent is listening or thinking.
Three optional sounds tell you, all off unless you turn them on:

| Setting | Plays |
|---|---|
| `cue-listening` | when it starts listening: after the wake word, Enter, or the Pause key |
| `cue-thinking` | on repeat while it thinks: from half a second after you stop talking until the answer starts |
| `cue-idle` | when the conversation ends and it waits for the wake word again |

Each is `default` for talkie's built-in sound (two glassy notes up, the same
down, and a quiet hum that swells and fades), the path to a WAV file of your
own, or empty for none:

```yaml
cue-listening: default
cue-thinking: ~/.config/eino-talkie-agent/sounds/thinking.wav
cue-idle: default
```

A thinking sound should loop seamlessly. talkie plays the sounds through its
own output, so the echo canceller keeps them out of what it hears.

### Without audio

`-text` runs the same agent with no audio and no ONNX Runtime. Each line of
stdin is a turn in one session, and the reply is printed as it would be
spoken:

```
printf 'what is the capital of France\nand of Spain\n' | example-eino-talkie-agent -text
```

## Files and directories

The agent follows the [XDG Base Directory](https://specifications.freedesktop.org/basedir-spec/latest/)
spec:

| What | Default | Override |
|---|---|---|
| Settings | `$XDG_CONFIG_HOME/eino-talkie-agent/config.yaml` | `-config`, `EINO_TALKIE_CONFIG` |
| Skills | `$XDG_CONFIG_HOME/eino-talkie-agent/skills/` | `-skills` |
| ONNX Runtime and the models | `$XDG_DATA_HOME/eino-talkie-agent/` | `-data`, `EINO_TALKIE_DATA` |
| Room profiles (talkie-core's) | `$XDG_CONFIG_HOME/talkie/rooms/` | |
| The skills' secrets (read by the systemd unit and compose) | `$XDG_CONFIG_HOME/eino-talkie-agent/env` | |

`$XDG_CONFIG_HOME` defaults to `~/.config` and `$XDG_DATA_HOME` to
`~/.local/share`. The data directory has the layout `get-assets.sh -d`
writes: `onnxruntime/lib/libonnxruntime.so`, `parakeet/` and `kokoro/`.
`-ort`, `-parakeet` and `-kokoro` override each part. Without
`onnxruntime/` there, the agent loads `libonnxruntime.so` through the system's
loader, for an ONNX Runtime installed system-wide. Parakeet's INT8 files are
used when its FP32 files aren't there, as after a default `get-assets.sh`.

`config.yaml` sets any flag, with the flag's name as the key:

```yaml
data: /data/talkie         # the models live here on this machine
model: gemma4:e4b
base-url: http://gpu-box:11434/v1
cuda: true
barge-in: wake
key: /dev/input/by-id/usb-My_Keyboard-event-kbd
```

Every flag also has an environment variable: `EINO_TALKIE_` and the flag's
name in upper case with `_` for `-`, such as `EINO_TALKIE_CUDA=true` or
`EINO_TALKIE_BASE_URL`. `OPENAI_BASE_URL`, `OPENAI_API_MODEL`,
`OPENAI_API_KEY` and `TALKIE_ORT` work too. A setting comes from, in order:
its flag, its environment variable, `config.yaml`, then its default. Paths
may start with `~/`. An unknown key is an error, so a misspelled setting
doesn't go unnoticed.

## Flags

Each flag's environment variable is `EINO_TALKIE_` and its name (see
[Files and directories](#files-and-directories)); the table gives the older
names that also work.

| Flag | Also | Default | |
|---|---|---|---|
| `-config` | | `$XDG_CONFIG_HOME/eino-talkie-agent/config.yaml` | the settings file; a missing default one is fine |
| `-data` | | `$XDG_DATA_HOME/eino-talkie-agent` | ONNX Runtime and the models |
| `-install-skills` | | | install the bundled skills into `-skills`, keeping any there, and exit |
| `-train` | | | train talkie to the room and the devices, save the profile, and exit |
| `-base-url` | `OPENAI_BASE_URL` | `http://localhost:11434/v1` | OpenAI-compatible API |
| `-model` | `OPENAI_API_MODEL` | `gemma4:e4b` | e.g. `qwen3.5:9b` (see [Models](#models)) |
| `-api-key` | `OPENAI_API_KEY` | `ollama` | Ollama ignores it |
| `-reasoning` | | `none` | sent as `reasoning_effort`. `low`, `medium` and `high` turn thinking on (see below) |
| `-temperature` | | `0.2` | sampling temperature (see [Models](#models)); negative leaves it to the server |
| `-skills` | | `$XDG_CONFIG_HOME/eino-talkie-agent/skills` | the skills directory; `""` for none |
| `-instruction` | | a voice prompt | the system prompt |
| `-text` | | off | no audio: stdin turns, printed replies |
| `-cue-listening`, `-cue-thinking`, `-cue-idle` | | none | sounds for listening, thinking and idle: `default`, a WAV file, or none (see [Sounds instead of a screen](#sounds-instead-of-a-screen)) |

The talkie flags are the same as `talkie-demo`'s: `-ort` (or `TALKIE_ORT`),
`-cuda`, `-parakeet`, `-int8`, `-kokoro` (these three default to the data
directory), `-voice`, `-wake` (default "hey
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
    └── office-lights
```

`SKILL.md` has YAML front matter with a `name` (letters, digits, `-` and
`_`; it defaults to the directory's name) and a `description`, then
instructions for the model. The repository's [`skills`](skills) directory
has three skills:
- `office-lights` controls the office lights;
- `web-search` searches the web;
- `kids-story` tells a children's story of the kind asked for.

`example-eino-talkie-agent -install-skills` installs all three. It keeps a
skill that is already there, so your edits survive; delete a skill's
directory and run it again to get the bundled version back. The scripts must
be executable, which `-install-skills` takes care of; copying them out of Go's
module cache doesn't, since Go stores its files read-only and not executable.

### Office lights

`office-lights` turns a light on or off, makes it warm (3000 K) or cool
(5400 K), or sets it to half or full brightness, through Home Assistant's
REST API. Its script,
[`skills/office-lights/scripts/office-lights`](skills/office-lights/scripts/office-lights),
is Python with only the standard library. It reads:

| Variable | |
|---|---|
| `HASSIO_TOKEN` | a long-lived access token, from your Home Assistant profile's Security tab |
| `OFFICE_LIGHTS` | the light's entity ID, such as `light.office` (a light group works too) |
| `HASSIO_API` | Home Assistant's URL; the default is `http://homeassistant.local:8123` |

It exits non-zero, with an `Error:` line, when Home Assistant can't be
reached, rejects the token or has no such light, so the agent can say it
didn't work.

### Web search

`web-search` searches through a [SearXNG](https://docs.searxng.org)
instance, a self-hosted metasearch engine that needs no API key. Its script,
[`skills/web-search/scripts/web-search`](skills/web-search/scripts/web-search),
is Python with only the standard library. It prints the top five results,
each with a title, a snippet and its source, for the model to answer from.
`SEARXNG_URL` selects the instance; the default is `http://127.0.0.1:8888`.

In Docker, `compose.yaml` runs SearXNG next to the agent, listening only on
`127.0.0.1:8888`, with the JSON API on: its settings are
[`docker/searxng/settings.yml`](docker/searxng/settings.yml), and its secret
key is `SEARXNG_SECRET` in `.env`. With the agent installed locally, run just
SearXNG from a checkout, after writing `.env` as in step 1 of
[Run it in Docker](#run-it-in-docker):

```
docker compose up -d searxng
```

It starts again after a reboot. Searches take 0.5 to 1.5 s. Some engines
refuse SearXNG (DuckDuckGo sometimes shows a CAPTCHA), but Google's results
come through.

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

### How skills work

Before each turn the agent reads the skills, so a new or edited skill applies
on the next question without a restart. Each skill's description and
instructions go in the system prompt, and each skill becomes a tool named
after it. The tool runs one of the skill's scripts with the arguments the
model gives it. The script runs directly, without a shell, from the skill's
directory, for at most 30 seconds. A skill with one script takes only `args`;
a skill with several also takes the script's name. The model has no other way
to run anything.

The scripts inherit the agent's environment, which is how `office-lights`
gets its token: set the variables in the shell that starts the agent.

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

The live tests ran against three models on Ollama on an NVIDIA Jetson Orin:

| Model | Plain answer, first token | Light request, first word | Skills |
|---|---|---|---|
| `gemma4:e4b` | 0.35 s | 0.5 s | every request, at 0.2 and 1.0 |
| `gemma4:12b` | 2 s warm | 1.4 s | every request, at 0.2 and 1.0; ran out of memory (below) |
| `qwen3.5:9b` | 0.4 s | 1.9 to 2.6 s | every request at 0.2 only |

- **`gemma4:e4b`** is the best fit for voice: the fastest, and reliable with
  skills, and the default. Told not to use markdown or code, it wrote
  its tool calls as text, such as `office-lights{args:["on"]}`, which talkie
  would have spoken. The skills prompt now says that rules about speaking
  don't apply to tool calls, and since then it has made real calls every time.
- **`qwen3.5:9b`** answers well. With skills, at the server's default
  temperature of 1.0, it sometimes skipped the tool and said the lights were
  on anyway. Once that happened, the history taught it to keep doing so for
  the rest of the session. At temperature 0.2 it called the tool for every
  request in the live test. Use `-model qwen3.5:9b`; it needs
  `-temperature 0.2`, the default.
- **`gemma4:12b`** called the tool for every request, at both 0.2 and 1.0,
  and started speaking about 0.5 s sooner than qwen after a light request
  (about 1.4 s).
  Use `-model gemma4:12b`. Thinking is on by default in this model, so it
  needs `-reasoning none` (the default) too. On the Orin, with a
  98,304-token context and 3 parallel slots, the kernel's out-of-memory
  killer stopped its `llama-server` after a few turns of one session. The
  request then failed, and the reload took about 15 s. A smaller context and
  one slot
  (`OLLAMA_CONTEXT_LENGTH=8192`, `OLLAMA_NUM_PARALLEL=1`, and perhaps
  `OLLAMA_MAX_LOADED_MODELS=1` on the container) should avoid that.

All three models rely on the fix-ups in the code, which are not specific to
any one model:
- the turn's tool calls stay in the history;
- what a model says after a tool call in the same message isn't spoken,
  because it can't know the result yet;
- tool errors go back to the model.

## Room training

`-train` tunes talkie to the room, once per microphone and speaker. It takes
about a minute: stay quiet while it measures the room's noise and the
speaker's echo, listen while Kokoro speaks a paragraph so the echo canceller
learns the room, then say the wake word five times and read a sentence aloud,
which tunes the wake word to your voice. It prints what it found, with advice
such as turning the speaker down, and saves the profile in
`~/.config/talkie/rooms`, keyed by the devices' names. Pick the devices with
`-input` and `-output`, by part of their names, as when you run the agent;
the agent logs the names it opened at start-up. talkie-core's
`talkie-demo -train` writes the same profiles.

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
  each skill, and turns tool errors into results. `install.go` writes the
  skills embedded in the binary for `-install-skills`.
- `config.go` finds the XDG directories, applies the environment and
  `config.yaml` to the flags, and finds the models in the data directory.
- `train.go` is `-train`, room training through talkie-core's `Train`.
- `compose.yaml` and `docker/` have the images and their compose files,
  `systemd/` the user units.
- `latency.go` joins talkie's events from `Config.OnEvent` with the agent's
  timings over a buffered channel. OnEvent never blocks: when the channel is
  full, the record is dropped and counted.

## Tests

```
go vet ./... && go test -race ./...
EINO_TALKIE_TEST_LLM=http://localhost:11434/v1 go test -run Live -v ./...
EINO_TALKIE_TEST_LLM=... EINO_TALKIE_TEST_THINKING=1 OPENAI_API_MODEL=qwen3.5:9b go test -run LiveThinking -v ./...
```

The unit tests use a fake eino chat model and need neither a network nor ONNX
Runtime. The live tests run one real turn, a turn cancelled after its first
delta, and ten light requests in different words. The light requests use the
repository's office-lights skill with a script that only records its
arguments, so no real lights change. `TestLiveWebSearch` uses the real
web-search skill, so it needs SearXNG running. It checks that the model
searches for facts, doesn't search for chit-chat, and still uses the lights
skill. `TestLiveKidsStory` checks that the model asks for stories of
different kinds and not for chit-chat. Set `OPENAI_API_MODEL` to test
another model, and `EINO_TALKIE_TEST_TEMP` to test another temperature.

## Known limits

- Linux only: `-key` reads Linux input events, and skill scripts run in
  their own process group.
- The conversation history is kept in memory, for one talkie session at a
  time.
- Skill scripts inherit the agent's environment, and the model chooses their
  arguments. Install only skills you trust.
- Every skill's instructions are in every turn's prompt, which suits a few
  short skills.
- The CPU and CUDA images have run on x86-64 with PipeWire, and the Jetson
  Xavier image on an AGX Xavier with PulseAudio and a USB headset. The Jetson
  Orin image hasn't been built yet. In Docker, ALSA can't open a device a
  sound server on the host is using.

## License

Apache-2.0; see LICENSE, NOTICE and THIRD_PARTY.md. talkie-core's models have
their own licenses; see its THIRD_PARTY.md.

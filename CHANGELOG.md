# Changelog

## [Unreleased]

## [0.1.0] - 2026-10-25

### Added

- A talkie-core `Agent` backed by an eino `ChatModelAgent` on any OpenAI-compatible server (Ollama by default, `gemma4:e4b`):
  - the model's reply streams into talkie a sentence at a time, filtered for speech (URLs, code, list numbers and `<think>` blocks);
  - an interruption cancels the model's request, and the next turn's history holds only what the user heard;
  - thinking is off by default (`-reasoning none`), since it delays the first word by about a minute.
- `go install`-able, following the XDG Base Directory spec:
  - settings in `$XDG_CONFIG_HOME/eino-talkie-agent/config.yaml` (`-config`), whose keys are the flag names; a flag, then its environment variable, then the file, then the default;
  - ONNX Runtime and the models in `$XDG_DATA_HOME/eino-talkie-agent` (`-data`), laid out as talkie-core's `get-assets.sh -d` writes them, with `-ort`, `-parakeet` and `-kokoro` overriding each part; Parakeet INT8 when its FP32 files aren't there;
  - `-install-skills` writes the bundled skills, with executable scripts, keeping any already installed.
- Every flag also has an environment variable, `EINO_TALKIE_` and its name (such as `EINO_TALKIE_CUDA`); `OPENAI_BASE_URL`, `OPENAI_API_MODEL`, `OPENAI_API_KEY` and `TALKIE_ORT` still work.
- `-train`: room training, as talkie-core's `talkie-demo -train`, with the models found as the agent finds them.
- Docker images: `cpu` (amd64 and arm64), `cuda` (CUDA 12), and `jetson-xavier` and `jetson-orin` on kokoro-run's ONNX Runtime base images (the Xavier's from Docker Hub). `compose.yaml` runs the agent with SearXNG for web search; override files add a GPU or ALSA sound. Settings, skills, room profiles and models stay on the host; sound goes through the host's PipeWire or PulseAudio, or ALSA.
- systemd user units for the installed agent and for its compose stack.
- A warning when a GPU run falls back to Parakeet's INT8 files, which are meant for the CPU.
- SIGTERM stops the agent cleanly, as `docker stop` and systemd send it.
- Skills in the Agent Skills format, from `$XDG_CONFIG_HOME/eino-talkie-agent/skills` or `-skills`:
  - their instructions go in the system prompt, and each skill is a tool that runs its scripts without a shell, for at most 30 s;
  - they are read before each turn, so a new or edited skill applies without a restart;
  - tool errors go back to the model, and tool calls stay in the history.
- Three skills: `office-lights` (Home Assistant), `web-search` (SearXNG) and `kids-story`.
- `-text`: the same agent without audio, a turn per line of stdin.
- `-cue-listening`, `-cue-thinking` and `-cue-idle`: optional sounds when it starts listening, while it thinks, and when the conversation ends, for a device without a screen; talkie's built-in sounds (`default`) or WAV files, all off by default.
- A `turn_latency` log line per reply, from talkie's events and the agent's timings.
- `-key`: the Pause key of a keyboard's event device interrupts.

### Known limits

- The Jetson Orin image was not built for this release; the CPU and CUDA images ran on x86-64, and the Jetson Xavier image on an AGX Xavier.

- Linux only: `-key` reads Linux input events, and skill scripts run in their own process group.
- The conversation history is kept in memory for one talkie session at a time.
- Skill scripts inherit the agent's environment, and the model chooses their arguments.
- Every skill's instructions are in every turn's prompt, which suits a few short skills.

[Unreleased]: https://github.com/androiddrew/example-eino-talkie-agent/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/androiddrew/example-eino-talkie-agent/releases/tag/v0.1.0

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
- Skills in the Agent Skills format, from `$XDG_CONFIG_HOME/eino-talkie-agent/skills` or `-skills`:
  - their instructions go in the system prompt, and each skill is a tool that runs its scripts without a shell, for at most 30 s;
  - they are read before each turn, so a new or edited skill applies without a restart;
  - tool errors go back to the model, and tool calls stay in the history.
- Three skills: `office-lights` (Home Assistant), `web-search` (SearXNG) and `kids-story`.
- `-text`: the same agent without audio, a turn per line of stdin.
- A `turn_latency` log line per reply, from talkie's events and the agent's timings.
- `-key`: the Pause key of a keyboard's event device interrupts.

### Known limits

- Linux only: `-key` reads Linux input events, and skill scripts run in their own process group.
- The conversation history is kept in memory for one talkie session at a time.
- Skill scripts inherit the agent's environment, and the model chooses their arguments.
- Every skill's instructions are in every turn's prompt, which suits a few short skills.

[Unreleased]: https://github.com/androiddrew/example-eino-talkie-agent/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/androiddrew/example-eino-talkie-agent/releases/tag/v0.1.0

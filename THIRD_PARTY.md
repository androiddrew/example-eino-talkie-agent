# Attribution and terms

Original code, the skills and their scripts are Apache-2.0 (LICENSE/NOTICE).
Nothing upstream is vendored.

| Component | Source/version | Terms |
|---|---|---|
| talkie-core | github.com/androiddrew/talkie-core v0.1.0; external Go dependency, with its own dependencies and models | Apache-2.0; see its THIRD_PARTY.md |
| eino | github.com/cloudwego/eino v0.8.6; external Go dependency | Apache-2.0, Copyright 2024 CloudWeGo Authors |
| eino-ext OpenAI chat model | github.com/cloudwego/eino-ext/components/model/openai v0.1.11; external Go dependency | Apache-2.0, Copyright 2024 CloudWeGo Authors |
| yaml.v3 | gopkg.in/yaml.v3 v3.0.1; external Go dependency, for SKILL.md front matter | MIT and Apache-2.0, Copyright 2011-2016 Canonical Ltd. |

Used at run time, not part of the module:

| What | Source | Terms |
|---|---|---|
| ONNX Runtime 1.22, Parakeet TDT 0.6B v3, Kokoro-82M v1.0 | as talkie-core's `scripts/get-assets.sh` downloads them | MIT, CC-BY-4.0 and Apache-2.0; see talkie-core's THIRD_PARTY.md |
| The LLM | any OpenAI-compatible server, such as Ollama | the model's own terms |
| SearXNG, for the web-search skill | github.com/searxng/searxng, run separately | AGPL-3.0; the skill only calls its HTTP API |
| Home Assistant, for the office-lights skill | home-assistant.io, run separately | Apache-2.0; the skill only calls its REST API |

# TEI (Text Embeddings Inference) on Mac

## Install

```
brew install text-embeddings-inference
```

## Run

```
text-embeddings-router --model-id Qwen/Qwen3-Embedding-0.6B --port 8090
```

This serves an OpenAI-compatible `/v1/embeddings` endpoint with Metal acceleration.

## TODO

- [ ] Create `serve-embed.sh` (like `serve-llm.sh`) — start TEI locally, expose via Tailscale Funnel
- [ ] Refactor `cmd/semantic index` to use HTTP `/v1/embeddings` instead of shelling out to `llama-embedding` CLI
- [ ] Refactor `cmd/semantic search` to use HTTP `/v1/embeddings` instead of `embedCLI()`
- [ ] Remove `LLAMA_EMBEDDING` / `EMBED_MODEL` env var dependencies — replace with `EMBED_ENDPOINT`
- [ ] Update `run-experiment.sh` to drop llama-embedding references
- [ ] Update `.gitignore` for `semantic`, `experiment` binaries and `index.json`
- [ ] Commit `cmd/semantic/`, `cmd/experiment/`, and shell scripts

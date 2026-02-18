# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PicoOraClaw is an ultra-lightweight personal AI assistant written in Go, forked from PicoClaw. It extends PicoClaw with **Oracle AI Database** as an optional backend for persistent storage and semantic vector search. The binary is a single self-contained executable (~10MB RAM) targeting embedded Linux devices (RISC-V, ARM64, x86_64).

Module path: `github.com/jasperan/picooraclaw`

## Build & Development Commands

```bash
make build          # Build for current platform → build/picooraclaw-<os>-<arch>
make build-all      # Cross-compile for linux/{amd64,arm64,riscv64}, darwin/arm64, windows/amd64
make install        # Build + install to ~/.local/bin
make test           # Run all tests: go test ./...
make fmt            # Format code: go fmt ./...
make vet            # Vet code: go vet ./...
make deps           # Update dependencies: go get -u ./... && go mod tidy
make clean          # Remove build/ directory
make generate       # Run go generate (copies workspace/ into cmd/ for embedding)
```

Run a single test:
```bash
go test ./pkg/tools/ -run TestEditTool -v
```

The `build` target runs `go generate` first, which embeds `workspace/` files into the binary via `//go:embed`.

## Architecture

### Entry Point & CLI Commands

`cmd/picooraclaw/main.go` — Single main file handling all CLI subcommands via a switch statement:
- `onboard` — Initialize config + workspace
- `agent` — Direct chat (one-shot `-m` or interactive REPL)
- `gateway` — Long-running service with chat channels, heartbeat, cron, health endpoints
- `setup-oracle` — Initialize Oracle schema + ONNX embedding model
- `oracle-inspect` — Inspect data stored in Oracle (memories, sessions, transcripts, state, etc.)
- `status`, `auth`, `cron`, `skills`, `migrate`

### Package Layout (`pkg/`)

| Package | Purpose |
|---------|---------|
| `agent` | Core agent loop (`AgentLoop`), context building, session/state/memory interfaces |
| `providers` | LLM provider abstraction (`LLMProvider` interface). Implementations: OpenAI-compatible HTTP, Anthropic Claude, GitHub Copilot, Codex CLI |
| `tools` | Tool interface + implementations (filesystem, shell, web search, edit, spawn/subagent, cron, remember/recall, message, I2C, SPI) |
| `channels` | Chat platform adapters (Telegram, Discord, Slack, DingTalk, LINE, Feishu, QQ, WhatsApp, MaixCAM, OneBot) |
| `oracle` | Oracle AI Database integration — connection pooling, schema init, embedding service (ONNX + API modes), Oracle-backed stores |
| `config` | Config loading/saving from `~/.picooraclaw/config.json`, env var overrides |
| `bus` | Internal message bus for inter-component communication |
| `session` | File-based session/history manager |
| `state` | File-based key-value state manager |
| `cron` | Scheduled job service (cron expressions + interval-based) |
| `heartbeat` | Periodic task runner (reads HEARTBEAT.md) |
| `skills` | Skill loader/installer (workspace, global, builtin, GitHub-hosted) |
| `voice` | Groq Whisper voice transcription |
| `health` | HTTP health/readiness endpoints |
| `auth` | OAuth/token authentication (OpenAI, Anthropic) |
| `migrate` | Migration from OpenClaw/PicoClaw |
| `devices` | USB device event monitoring |
| `logger` | Structured logging |

### Key Interfaces

- **`tools.Tool`** (`pkg/tools/base.go`): All tools implement `Name()`, `Description()`, `Parameters()`, `Execute()`. Extended by `ContextualTool` (channel/chat context) and `AsyncTool` (background execution with callbacks).
- **`providers.LLMProvider`** (`pkg/providers/types.go`): `Chat()` + `GetDefaultModel()`. All LLM backends implement this.
- **Storage interfaces** (`pkg/agent/interfaces.go`): `SessionManagerInterface`, `StateManagerInterface`, `MemoryStoreInterface`. Both file-based and Oracle implementations satisfy these, enabling `agent.NewAgentLoopWithStores()` for dependency injection.

### Oracle Integration Pattern

When `oracle.enabled=true` in config:
1. `initOracleAgent()` creates Oracle-backed stores (session, state, memory) via `pkg/oracle`
2. `remember`/`recall` tools are registered, using `VECTOR_EMBEDDING()` for semantic search
3. On connection failure, the system gracefully falls back to file-based storage

The Oracle package uses `go-ora/v2` (pure Go, no CGO/Instant Client required). Embedding can use either in-database ONNX models or external API providers.

### Agent Loop Flow

1. User message arrives (CLI, channel, or heartbeat)
2. `ContextBuilder` assembles system prompt from workspace files (IDENTITY.md, SOUL.md, TOOLS.md, etc.) or Oracle `PICO_PROMPTS`
3. Session history is loaded and the LLM is called via the provider
4. If the LLM returns tool calls, they are executed via `ToolRegistry` and results fed back (up to `max_tool_iterations`)
5. Final response is returned/sent to the originating channel

### Workspace Embedding

`workspace/` directory is embedded into the binary at build time via `//go:embed`. The `make generate` step copies it into `cmd/picooraclaw/workspace` before compilation. The `onboard` command extracts these embedded files to `~/.picooraclaw/workspace/`.

## Config

Runtime config: `~/.picooraclaw/config.json` (created by `picooraclaw onboard`)
Example config: `config/config.example.json`
All config fields support env var overrides with `PICO_` or `PICOCLAW_` prefixes.

## Testing Notes

- Tests use `go-sqlmock` for Oracle database tests (`pkg/oracle/*_test.go`)
- No external services required for unit tests
- Integration tests (tagged in filenames like `integration_test.go`) may require a running Oracle instance

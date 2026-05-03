# README drift findings

Final summary:

- `install.sh` still needs a behavior fix: the advertised one-command installer exits 0 even when its auto-build path does not produce the `picooraclaw` executable. A `TODO(readme-drift)` comment was added near the build command.
- `install.sh` still needs a Go-version check fix: it only checks for a `go` binary and still reports `Go 1.21+`, while `go.mod` requires Go 1.25. A `TODO(readme-drift)` comment was added near the prerequisite check.
- `scripts/setup-oracle.sh` still patches Oracle config with legacy camelCase keys (`onnxModel`, `agentId`) instead of the snake_case JSON keys consumed by `pkg/config`. A `TODO(readme-drift)` comment was added near the config patch.
- `deploy/oci/scripts/setup.sh` still installs a hard-coded Go 1.24 toolchain while `go.mod` requires Go 1.25. A `TODO(readme-drift)` comment was added near the install step.
- `deploy/oci/scripts/setup.sh` still patches Oracle config with legacy camelCase keys (`walletPath`, `onnxModel`, `agentId`) instead of the snake_case JSON keys consumed by `pkg/config`. `TODO(readme-drift)` comments were added near both config patch blocks.

README sections verified and fixed:

- Build/install commands: `make build` and `make install` were run in a scratch copy and exited 0; `install.sh` was run in scratch and logged as unresolved because it exited 0 without producing the advertised binary.
- CLI subcommands: README command tables were checked against `cmd/picooraclaw/main.go` switch cases.
- Config fields and env vars: Oracle field names and `PICO_ORACLE_*` env vars were checked against `pkg/config/config.go` JSON/env tags and corrected in all three READMEs.
- File paths: `~/.picooraclaw/config.json` and `~/.picooraclaw/workspace` were checked against `cmd/picooraclaw/main.go` and `pkg/config/config.go`.
- Tool names: `remember` and `recall` were checked against `pkg/tools` and registration in `cmd/picooraclaw/main.go`.
- Channels: README channel lists were corrected to match registered channel implementations in `pkg/channels`.
- Oracle features: `VECTOR_EMBEDDING`, ONNX model handling, `go-ora`, schema tables, and vector indexes were checked against `pkg/oracle` and `cmd/picooraclaw/main.go`.
- Make targets: README build targets were checked against `Makefile`.
- Docker Compose: service names and profiles were checked against `docker-compose.yml`; the English one-shot command was corrected to `picooraclaw-agent`.

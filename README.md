<div align="center">
  <img src="assets/new_logo.png" alt="PicoClaw" width="512">

  <h1>PicoOraClaw: Ultra-Efficient AI Assistant in Go + Oracle AI Database based on PicoClaw</h1>

  <h3>$10 Hardware · 10MB RAM · 1s Boot · Oracle AI Vector Search</h3>

  <p>
    <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20RISC--V-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://picoclaw.io"><img src="https://img.shields.io/badge/Website-picoclaw.io-blue?style=flat&logo=google-chrome&logoColor=white" alt="Website"></a>
    <a href="https://x.com/SipeedIO"><img src="https://img.shields.io/badge/X_(Twitter)-SipeedIO-black?style=flat&logo=x&logoColor=white" alt="Twitter"></a>
  </p>

 [中文](README.zh.md) | [日本語](README.ja.md) | **English**
</div>

---

PicoOraClaw is a fork of [PicoClaw](https://github.com/sipeed/picoclaw) that adds **Oracle AI Database** as a backend for persistent storage and semantic vector search. The agent remembers facts and recalls them by meaning using in-database ONNX embeddings — no external embedding API required.

## Quickstart (5 minutes)

Everything you need: **Go 1.24+**, **Ollama** and **Docker** (for Oracle AI Database).

### Step 1: Build

```bash
git clone https://github.com/jasperan/picooraclaw.git
cd picooraclaw
make build
```

### Step 2: Initialize

```bash
./build/picooraclaw onboard
```

### Step 3: Start Ollama and pull a model

```bash
# Install Ollama if needed: https://ollama.com/download
ollama pull qwen3:latest
```

### Step 4: Configure for Ollama

Edit `~/.picooraclaw/config.json`:

```json
{
  "agents": {
    "defaults": {
      "provider": "ollama",
      "model": "qwen3:latest",
      "max_tokens": 8192,
      "temperature": 0.7
    }
  },
  "providers": {
    "ollama": {
      "api_key": "",
      "api_base": "http://localhost:11434/v1"
    }
  }
}
```

### Step 5: Chat

```bash
# One-shot
./build/picooraclaw agent -m "Hello!"

# Interactive mode
./build/picooraclaw agent
```

That's it — you have a working AI assistant with local inference. No API keys, no cloud dependency.

---

## Add Oracle AI Vector Search

Oracle gives you persistent storage, semantic memory (remember/recall by meaning), and crash-safe ACID transactions. Without it, storage is file-based.

Run the setup script — it handles everything automatically:

```bash
./scripts/setup-oracle.sh [optional-password]
```

This single script:
1. Pulls and starts the Oracle AI Database Free container
2. Waits for the database to be ready
3. Creates the `picooraclaw` database user with the required grants
4. Patches your `~/.picooraclaw/config.json` with the Oracle connection settings
5. Runs `picooraclaw setup-oracle` to initialize the schema and load the ONNX embedding model

Expected output when complete:
```
── Step 4/4: Schema + ONNX model ─────────────────────────────────────────
  Running picooraclaw setup-oracle...
✓ Connected to Oracle AI Database
✓ Schema initialized (8 tables with PICO_ prefix)
✓ ONNX model 'ALL_MINILM_L12_V2' already loaded
✓ VECTOR_EMBEDDING() test passed
✓ Prompts seeded from workspace

════════════════════════════════════════════════════════
  Oracle AI Database setup complete!
  Test with:
    ./build/picooraclaw agent -m "Remember that I love Go"
    ./build/picooraclaw agent -m "What language do I like?"
    ./build/picooraclaw oracle-inspect
════════════════════════════════════════════════════════
```

### Step 5: Test semantic memory

```bash
# Store a fact
./build/picooraclaw agent -m "Remember that my favorite language is Go"

# Recall by meaning (not keywords)
./build/picooraclaw agent -m "What programming language do I prefer?"
```

The second command finds the stored memory via cosine similarity on 384-dimensional vectors — no keyword matching.

### Step 6: Inspect what's stored

The `oracle-inspect` command lets you view everything stored in Oracle without writing SQL.

```bash
picooraclaw oracle-inspect [table] [options]
```

**Tables:** `memories`, `sessions`, `transcripts`, `state`, `notes`, `prompts`, `config`, `meta`
**Options:** `-n <limit>` max rows (default 20), `-s <text>` semantic search (memories only)

#### Overview dashboard (no arguments)

```bash
./build/picooraclaw oracle-inspect
```

```
=============================================================
  PicoOraClaw Oracle AI Database Inspector
=============================================================

  Table                  Rows
  ─────────────────────  ────
  Memories                   4  ████
  Sessions                   1  █
  Transcripts                0  (empty)
  State                      0  (empty)
  Daily Notes                0  (empty)
  Prompts                    4  ████
  Config                     0  (empty)
  Meta                       1  █
  ─────────────────────  ────
  Total                     10

  Recent Memories (last 5):
  ─────────────────────────────────────────────────────────
  2026-02-18 02:20  0.7 [preference]  my favorite programming language is Go
  2026-02-16 06:13  0.7 [preference]  I prefer Python and Go for programming
  2026-02-16 06:12  0.7 [employment]  I work at Oracle as a developer
  2026-02-16 06:12  0.7 [preference]  my favorite color is blue

  Recent Transcripts (last 5):
  ─────────────────────────────────────────────────────────
  (no transcripts yet)

  Recent Sessions (last 5):
  ─────────────────────────────────────────────────────────
  2026-02-18 04:34  cli:default    **Cohesive Summary:** The user stored their
  favorite color and employment details using the "remember" tool. When recalling,
  the assistant retrieved stored memories via vector similarity, highlighting the
  tool's role in storing and retrieving personal data across sessions.

  Recent State Entries (last 5):
  ─────────────────────────────────────────────────────────
  (no state entries yet)

  Recent Daily Notes (last 5):
  ─────────────────────────────────────────────────────────
  (no daily notes yet)

  System Prompts (last 5):
  ─────────────────────────────────────────────────────────
  2026-02-18 04:05  AGENT                        357 chars
  2026-02-18 04:05  USER                         365 chars
  2026-02-18 04:05  SOUL                         296 chars
  2026-02-18 04:05  IDENTITY                    1271 chars

  Config Entries (last 5):
  ─────────────────────────────────────────────────────────
  (no config entries stored yet)

  Schema Metadata:
  ─────────────────────────────────────────────────────────
  2026-02-18 04:05  schema_version                 = 1.0.0

  Tip: Run 'picooraclaw oracle-inspect <table>' for details
       Run 'picooraclaw oracle-inspect memories -s "query"' for semantic search
```

#### List all memories

```bash
./build/picooraclaw oracle-inspect memories
```

```
  All Memories
  ─────────────────────────────────────────────────────────

  ID: 0e74a94c  Vector: yes
  Created: 2026-02-18 02:20  Importance: 0.7  Category: preference  Accessed: 0x
  Content: my favorite programming language is Go

  ID: 383ff5d3  Vector: yes
  Created: 2026-02-16 06:13  Importance: 0.7  Category: preference  Accessed: 0x
  Content: I prefer Python and Go for programming

  ID: 22b84dba  Vector: yes
  Created: 2026-02-16 06:12  Importance: 0.7  Category: employment  Accessed: 1x
  Content: I work at Oracle as a developer

  ID: e364704d  Vector: yes
  Created: 2026-02-16 06:12  Importance: 0.7  Category: preference  Accessed: 1x
  Content: my favorite color is blue
```

#### Semantic search over memories

```bash
./build/picooraclaw oracle-inspect memories -s "what does the user like to program in"
```

```
  Semantic Search: "what does the user like to program in"
  ─────────────────────────────────────────────────────────

  [ 61.3% match]  ID: 383ff5d3
  Created: 2026-02-16 06:13  Importance: 0.7  Category: preference  Accessed: 0x
  Content: I prefer Python and Go for programming

  [ 60.7% match]  ID: 0e74a94c
  Created: 2026-02-18 02:20  Importance: 0.7  Category: preference  Accessed: 0x
  Content: my favorite programming language is Go

  [ 30.9% match]  ID: 22b84dba
  Created: 2026-02-16 06:12  Importance: 0.7  Category: employment  Accessed: 1x
  Content: I work at Oracle as a developer

  [ 16.3% match]  ID: e364704d
  Created: 2026-02-16 06:12  Importance: 0.7  Category: preference  Accessed: 1x
  Content: my favorite color is blue
```

#### View a system prompt in full

```bash
./build/picooraclaw oracle-inspect prompts IDENTITY
./build/picooraclaw oracle-inspect prompts SOUL
```

#### Schema metadata, ONNX models, and vector indexes

```bash
./build/picooraclaw oracle-inspect meta
```

```
  Schema Metadata
  ─────────────────────────────────────────────────────────
  schema_version                 = 1.0.0

  ONNX Models
  ─────────────────────────────────────────────────────────
  ALL_MINILM_L12_V2          EMBEDDING        ONNX

  Vector Indexes
  ─────────────────────────────────────────────────────────
  IDX_PICO_DAILY_NOTES_VEC        on PICO_DAILY_NOTES
  IDX_PICO_MEMORIES_VEC           on PICO_MEMORIES
```

#### Other tables

```bash
picooraclaw oracle-inspect sessions                       # Chat sessions
picooraclaw oracle-inspect transcripts -n 50              # Last 50 transcript lines
picooraclaw oracle-inspect prompts                        # System prompts (IDENTITY, SOUL, etc.)
picooraclaw oracle-inspect state                          # Agent key-value state
picooraclaw oracle-inspect notes                          # Daily notes
picooraclaw oracle-inspect config                         # Stored config entries
```

---

## CLI Reference

| Command | Description |
|---|---|
| `picooraclaw onboard` | Initialize config and workspace |
| `picooraclaw agent -m "..."` | One-shot chat |
| `picooraclaw agent` | Interactive chat mode |
| `picooraclaw gateway` | Start long-running service with channels |
| `picooraclaw status` | Show status |
| `picooraclaw setup-oracle` | Initialize Oracle schema + ONNX model |
| `picooraclaw oracle-inspect` | Inspect data stored in Oracle |
| `picooraclaw oracle-inspect memories -s "query"` | Semantic search over memories |
| `picooraclaw cron list` | List scheduled jobs |
| `picooraclaw skills list` | List installed skills |

---

## How Oracle Storage Works

```
                           ┌──────────────────────────────────────────┐
                           │         Oracle AI Database               │
                           │                                          │
  picooraclaw binary       │  ┌──────────────┐  ┌──────────────────┐ │
  ┌───────────────────┐    │  │ PICO_MEMORIES │  │ PICO_DAILY_NOTES │ │
  │  AgentLoop        │    │  │  + VECTOR idx │  │  + VECTOR idx    │ │
  │  ├─ SessionStore ──────│──│──────────────┐│  └──────────────────┘ │
  │  ├─ StateStore   ──────│──│ PICO_SESSIONS││                       │
  │  ├─ MemoryStore  ──────│──│ PICO_STATE   ││  ┌──────────────────┐ │
  │  ├─ PromptStore  ──────│──│ PICO_PROMPTS ││  │ ALL_MINILM_L12_V2│ │
  │  ├─ ConfigStore  ──────│──│ PICO_CONFIG  ││  │   (ONNX model)   │ │
  │  └─ Tools:       │    │  │ PICO_META    ││  │  384-dim vectors  │ │
  │     ├─ remember  ──────│──│ PICO_TRANS.  ││  └──────────────────┘ │
  │     └─ recall    ──────│──└──────────────┘│                       │
  └───────────────────┘    │   go-ora v2.9.0  │                       │
         (pure Go)         │   (pure Go driver)│                       │
                           └──────────────────────────────────────────┘
```

| Table | Purpose |
|---|---|
| `PICO_MEMORIES` | Long-term memory with 384-dim vector embeddings for semantic search |
| `PICO_SESSIONS` | Chat history per channel |
| `PICO_TRANSCRIPTS` | Full conversation audit log |
| `PICO_STATE` | Agent key-value state |
| `PICO_DAILY_NOTES` | Daily journal entries with vector embeddings |
| `PICO_PROMPTS` | System prompts (IDENTITY.md, SOUL.md, etc.) |
| `PICO_CONFIG` | Runtime configuration |
| `PICO_META` | Schema versioning metadata |

The `remember` tool stores text + vector embedding via `VECTOR_EMBEDDING(ALL_MINILM_L12_V2 USING :text AS DATA)`. The `recall` tool searches by cosine similarity via `VECTOR_DISTANCE()`. Results with < 30% similarity are filtered out.

---

## Using a Cloud LLM Instead of Ollama

If you prefer a cloud provider, set `provider` and add your API key:

<details>
<summary><b>OpenRouter (access to all models)</b></summary>

```json
{
  "agents": {
    "defaults": {
      "provider": "openrouter",
      "model": "anthropic/claude-opus-4-5"
    }
  },
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-xxx",
      "api_base": "https://openrouter.ai/api/v1"
    }
  }
}
```

Get a key at [openrouter.ai/keys](https://openrouter.ai/keys) (200K free tokens/month).

</details>

<details>
<summary><b>Zhipu (best for Chinese users)</b></summary>

```json
{
  "agents": {
    "defaults": {
      "provider": "zhipu",
      "model": "glm-4.7"
    }
  },
  "providers": {
    "zhipu": {
      "api_key": "your-key",
      "api_base": "https://open.bigmodel.cn/api/paas/v4"
    }
  }
}
```

Get a key at [bigmodel.cn](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys).

</details>

<details>
<summary><b>All supported providers</b></summary>

| Provider | Purpose | Get API Key |
|---|---|---|
| `ollama` | Local inference (recommended) | [ollama.com](https://ollama.com) |
| `openrouter` | Access to all models | [openrouter.ai](https://openrouter.ai/keys) |
| `zhipu` | Zhipu/GLM models | [bigmodel.cn](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) |
| `anthropic` | Claude models | [console.anthropic.com](https://console.anthropic.com) |
| `openai` | GPT models | [platform.openai.com](https://platform.openai.com) |
| `gemini` | Gemini models | [aistudio.google.com](https://aistudio.google.com) |
| `deepseek` | DeepSeek models | [platform.deepseek.com](https://platform.deepseek.com) |
| `groq` | Fast inference + voice transcription | [console.groq.com](https://console.groq.com) |

</details>

---

## Chat Channels

Connect PicoOraClaw to Telegram, Discord, Slack, DingTalk, LINE, QQ, or Feishu via the `gateway` command.

<details>
<summary><b>Telegram</b> (Recommended)</summary>

1. Message `@BotFather` on Telegram, send `/newbot`, copy the token
2. Add to `~/.picooraclaw/config.json`:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allowFrom": ["YOUR_USER_ID"]
    }
  }
}
```

3. Run `picooraclaw gateway`

</details>

<details>
<summary><b>Discord</b></summary>

1. Create a bot at [discord.com/developers](https://discord.com/developers/applications), enable MESSAGE CONTENT INTENT
2. Add to config:

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allowFrom": ["YOUR_USER_ID"]
    }
  }
}
```

3. Invite bot with `Send Messages` + `Read Message History` permissions
4. Run `picooraclaw gateway`

</details>

<details>
<summary><b>QQ, DingTalk, LINE, Feishu, Slack</b></summary>

See `config/config.example.json` for the full channel configuration reference. Each channel follows the same pattern:

```json
{
  "channels": {
    "<channel_name>": {
      "enabled": true,
      "<credentials>": "...",
      "allow_from": []
    }
  }
}
```

Run `picooraclaw gateway` after configuring.

</details>

---

## Oracle on Autonomous Database (Cloud)

<details>
<summary><b>ADB wallet-less TLS</b></summary>

```json
{
  "oracle": {
    "enabled": true,
    "mode": "adb",
    "dsn": "(description=(retry_count=20)(retry_delay=3)(address=(protocol=tcps)(port=1522)(host=adb.us-ashburn-1.oraclecloud.com))(connect_data=(service_name=xxx_myatp_low.adb.oraclecloud.com))(security=(ssl_server_dn_match=yes)))",
    "user": "picooraclaw",
    "password": "YourPass123"
  }
}
```

</details>

<details>
<summary><b>ADB with mTLS wallet</b></summary>

```json
{
  "oracle": {
    "enabled": true,
    "mode": "adb",
    "host": "adb.us-ashburn-1.oraclecloud.com",
    "port": 1522,
    "service": "xxx_myatp_low.adb.oraclecloud.com",
    "walletPath": "/path/to/wallet",
    "user": "picooraclaw",
    "password": "YourPass123"
  }
}
```

Download wallet from OCI Console > Autonomous Database > DB Connection > Download Wallet.

</details>

<details>
<summary><b>Oracle config reference</b></summary>

| Field | Env Variable | Default | Description |
|---|---|---|---|
| `enabled` | `PICO_ORACLE_ENABLED` | `false` | Enable Oracle backend |
| `mode` | `PICO_ORACLE_MODE` | `freepdb` | `freepdb` or `adb` |
| `host` | `PICO_ORACLE_HOST` | `localhost` | Oracle host |
| `port` | `PICO_ORACLE_PORT` | `1521` | Listener port |
| `service` | `PICO_ORACLE_SERVICE` | `FREEPDB1` | Service name |
| `user` | `PICO_ORACLE_USER` | `picooraclaw` | DB username |
| `password` | `PICO_ORACLE_PASSWORD` | — | DB password |
| `dsn` | `PICO_ORACLE_DSN` | — | Full DSN (ADB wallet-less) |
| `walletPath` | `PICO_ORACLE_WALLET_PATH` | — | Wallet directory (ADB mTLS) |
| `onnxModel` | `PICO_ORACLE_ONNX_MODEL` | `ALL_MINILM_L12_V2` | ONNX model for embeddings |
| `agentId` | `PICO_ORACLE_AGENT_ID` | `default` | Multi-agent isolation key |

</details>

---

## Troubleshooting

<details>
<summary><b>Oracle: Connection refused / ORA-12541</b></summary>

```bash
docker ps | grep oracle          # Is it running?
docker logs oracle-free          # Wait for "DATABASE IS READY"
ss -tlnp | grep 1521            # Is port 1521 listening?
```

</details>

<details>
<summary><b>Oracle: ORA-01017 invalid username/password</b></summary>

```bash
docker exec -it oracle-free sqlplus sys/YourPass123@localhost:1521/FREEPDB1 as sysdba
SQL> ALTER USER picooraclaw IDENTIFIED BY NewPassword123;
```

</details>

<details>
<summary><b>Oracle: VECTOR_EMBEDDING() returns ORA-04063</b></summary>

ONNX model not loaded. Run `picooraclaw setup-oracle` or manually:

```sql
BEGIN
  DBMS_VECTOR.LOAD_ONNX_MODEL('PICO_ONNX_DIR', 'all_MiniLM_L12_v2.onnx', 'ALL_MINILM_L12_V2');
END;
/
```

Requires `GRANT CREATE MINING MODEL TO picooraclaw;` as SYSDBA.

</details>

<details>
<summary><b>Agent falls back to file-based mode</b></summary>

Oracle is enabled but connection failed at startup. Check:
- Is the Oracle container healthy? (`docker ps`)
- Password match between config and `ORACLE_PWD`?
- Service name should be `FREEPDB1` (not `FREE` or `XE`)

</details>

---

## Build Targets

```bash
make build          # Build for current platform
make build-all      # Cross-compile: linux/{amd64,arm64,riscv64}, darwin/arm64, windows/amd64
make install        # Build + install to ~/.local/bin
make test           # go test ./...
make fmt            # go fmt ./...
make vet            # go vet ./...
```

## Docker Compose

```bash
# Full stack with Oracle
PICO_ORACLE_PASSWORD=YourPass123 docker compose --profile oracle --profile gateway up -d

# Without Oracle
docker compose --profile gateway up -d

# One-shot agent
docker compose run --rm picoclaw-agent -m "What is 2+2?"
```

## Features

- Single static binary (~10MB RAM), runs on RISC-V/ARM64/x86_64
- Ollama, OpenRouter, Anthropic, OpenAI, Gemini, Zhipu, DeepSeek, Groq providers
- Oracle AI Database with AI Vector Search (384-dim ONNX embeddings)
- Chat channels: Telegram, Discord, Slack, QQ, DingTalk, LINE, Feishu, WhatsApp
- Scheduled tasks via cron expressions
- Heartbeat periodic tasks
- Skills system (workspace, global, GitHub-hosted)
- Security sandbox with workspace restriction
- Graceful fallback to file-based storage when Oracle is unavailable

## Contributing

PRs welcome! Discord: <https://discord.gg/V4sAZ9XWpN>

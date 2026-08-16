# Spec 05 — Single-File Web UI

**Tier B1 · Scope S**

## Problem

The web channel (`pkg/channels/web/`) is a complete, tested HTTP API —
`POST /v1/chat`, `GET /v1/events` (SSE with cursor resume), `/v1/sessions`
(list/create/delete), `GET /v1/memory?q=` — but has **no face**. `curl` is the
only client. A personal assistant with no UI can't be demoed, screenshotted, or
used from a phone on the $10-hardware story.

## Goal

A **single self-contained HTML file** (vanilla JS, no build step, no CDN —
works fully offline on the box) embedded into the binary via `go:embed` and
served by the web channel at `/`. Tabs: **Chat**, **Memory**, **Sessions**,
**Dashboard**. This matches the project's existing single-file-artifact style
(the 24-slide `picooraclaw-presentation.html`).

## Design

### 1. Serving

`pkg/channels/web/` gains `ui.go` + `ui/index.html` (embedded):

```go
//go:embed ui/index.html
var uiHTML string

mux.HandleFunc("/", c.handleUI)          // serves uiHTML
mux.HandleFunc("/healthz", ...)          // existing gateway health at 18790 stays
```

- `/` returns the HTML with `Cache-Control: no-store` (dev-friendly) and the
  server injects `window.PICO_CONFIG = { token: <cfg.Token>, endpoint: "/" }`.
- When `cfg.Token` is set, the UI stores it in `localStorage` (prompt once) and
  sends `Authorization: Bearer` on every call — no CORS needed (same origin).
- Auth failures surface a friendly "set your token" screen instead of raw 401.

### 2. UI structure (one file, ~700 lines, dark theme)

```
┌────────────────────────────────────────────────┐
│ PicoOraClaw   [Chat] [Memory] [Sessions] [Dash]│
├────────────────────────────────────────────────┤
│ Chat tab:                                     │
│   • session picker (from /v1/sessions)        │
│   • message list (incremental, appended)      │
│   • SSE /v1/events?session_id=… live stream   │
│     - message_start → typing indicator        │
│     - tool_call_start/end → tool chips        │
│       (name + ✓/✗ + args preview)             │
│   • input box + Enter to send (POST /v1/chat) │
│ Memory tab:                                   │
│   • search box → /v1/memory?q=…&limit=50      │
│   • result cards: text, score bar, date       │
│ Sessions tab:                                 │
│   • list + create (title) + delete            │
│ Dashboard tab:                                │
│   • /v1/status (new, see §3) overview:        │
│     table rows, memory count, last activity   │
└────────────────────────────────────────────────┘
```

Tool chips render args as collapsed `<details>`; `message_end` appends the
final assistant text. Cursor resume uses the `from` param + sessionStorage.

### 3. Small server additions (kept minimal)

| addition | purpose |
|---|---|
| `GET /v1/status` | dashboard: per-table row counts + `schema_version` + uptime (reuses `oracle-inspect` overview queries, moved to `pkg/oracle/analytics.go` per Spec 04) |
| `GET /v1/health` | lightweight `200 ok` (existing `/health` on gateway stays for infra) |

No new auth model: the existing bearer token wraps everything (register the new
routes inside the same `authMiddleware` chain).

## Files touched

- `pkg/channels/web/ui/index.html` (new) — the UI
- `pkg/channels/web/ui.go` (new) — embed + `/` route + `/v1/status`
- `pkg/channels/web/server.go` — register routes
- `pkg/channels/web/server_test.go` — route existence + status JSON
- `pkg/oracle/analytics.go` (Spec 04) — status aggregation source

## Testing

- `TestHandleUI_ServesHTML`: GET `/` → 200, `text/html`, contains `<div id="app">`.
- `TestHandleStatus`: GET `/v1/status` with fake lister → JSON shape.
- Bearer rejection path already covered (`server_test.go:43ca03b` pattern) —
  extend to `/` and `/v1/status`.
- Manual: `gateway --enable-web`, open `http://host:port/`, chat end-to-end
  with tool chips streaming.

## Risks

- **SSE + mobile browsers** — keep `X-Accel-Buffering: no` (already set) and
  a `: ping` keepalive (already sent); add `EventSource` reconnection with
  cursor.
- **XSS** — all dynamic content inserted via `textContent`, never `innerHTML`
  for user/agent strings; tool args rendered in `<details><pre>` with
  `textContent`.
- **Single file size** — ~700 lines is fine; no external deps so no supply
  chain or CDN-failure risk.

## Acceptance criteria

- `gateway --enable-web` then browsing to the box serves a working chat UI with
  live tool chips and streaming replies.
- Memory search returns cards with scores.
- Sessions can be created/listed/deleted from the UI.
- Works with the token set; shows a clear message when unauthorized.

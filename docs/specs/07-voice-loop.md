# Spec 07 — Voice Loop

**Tier B3 · Scope S**

## Problem

Voice is half-implemented. `pkg/voice/transcriber.go` has a working
`GroqTranscriber` (OpenAI-compatible `/v1/audio/transcriptions`, whisper-class)
and it is already attached to Telegram, Discord, and Slack channels
(`cmd/picooraclaw/main.go:614-635`). But the **CLI has no voice** — the
"$10 assistant you can talk to" story is untold — and there is no
speak-back (TTS) anywhere.

## Goal

1. `agent --voice <audio-file>` — transcribe → run agent → print reply.
2. `agent --voice-record [seconds]` — record from mic (arecord/ffmpeg when
   present) then same path.
3. Optional reply-as-speech via a pluggable `Speaker` interface with a zero-dep
   default (`espeak`/`say` when available) — no cloud TTS in v1.

## Design

### 1. `pkg/voice` additions

```go
// transcriber.go (existing) — unchanged; add a tiny factory:
func NewTranscriber(cfg *config.ProvidersConfig) (*GroqTranscriber, bool)

// speaker.go (new)
type Speaker interface { Speak(text string) error }          // synchronous
func NewSpeaker(cfg) Speaker                                  // nil if unsupported
func (s *SystemSpeaker) Speak(text string) error              // exec espeak|say
```

`SystemSpeaker` probes `espeak` (Linux), `say` (macOS) at construction;
`Speak` streams text via stdin. Config `voice.speaker: auto|espeak|say|off`.

### 2. CLI (`cmd/picooraclaw/main.go`)

```
picooraclaw agent --voice note.wav            # transcribe + one-shot chat
picooraclaw agent --voice-record 30           # arecord 30s → same
picooraclaw agent --voice --speak             # also speak the reply
```

Flow (`agentCmd`):
1. Resolve audio path (record if `--voice-record`; `exec.Command` arecord →
   temp wav; error message if arecord missing).
2. `transcriber.Transcribe(path)` (reuse existing method — check signature;
   it returns `TranscriptionResponse{Text,...}`).
3. Normal one-shot `AgentLoop` turn with the transcript as `-m`.
4. `--speak` → `Speaker.Speak(reply)`.

Channels are untouched; Telegram/Discord/Slack voice already works.

### 3. Config (`pkg/config/config.go`)

```go
type VoiceConfig struct {
    Speaker string `json:"speaker" env:"PICOCLAW_VOICE_SPEAKER"` // auto|espeak|say|off
}
```

## Files touched

- `pkg/voice/speaker.go` (new)
- `pkg/voice/transcriber.go` — `NewTranscriber` factory (optional)
- `cmd/picooraclaw/main.go` — `agentCmd` flags
- `pkg/config/config.go` — `VoiceConfig`

## Testing

- Unit: `SystemSpeaker` probe logic (fake execer), config parsing.
- Transcribe path: already covered by channel tests? — add a fake
  `Transcriber` interface for the CLI flow (no network in unit tests).
- Manual: `agent --voice-record 5 "hello"` → hears back on `--speak`.

## Risks

- **arecord/ffmpeg absence** — clear error message with install hint; never
  hang (exec timeout 30 s).
- **Groq API key needed for transcription** — `--voice` without key prints the
  same guidance as channel voice setup.
- **TTS quality** — system TTS is robotic; acceptable v1, `off` default on
  headless boxes, cloud TTS (OCI Speech / ElevenLabs-style) noted as v2.

## Acceptance criteria

- `agent --voice <wav>` produces the same reply as `agent -m <transcript>`.
- `--voice-record` works where `arecord` exists; else a helpful error.
- `--voice --speak` speaks the reply when a system TTS binary is present.
- No behavior change to existing channels; all unit tests green.

package voice

import (
	"bytes"
	"fmt"
	"os/exec"
	"time"
)

// Speaker renders assistant text as speech using a system TTS binary.
// Zero-dependency: probes espeak (Linux) / say (macOS) at construction.
type Speaker struct {
	bin string
}

// NewSpeaker probes for an available system TTS. Returns nil when none is
// found (callers must treat nil as "speech unavailable").
func NewSpeaker(preferred string) *Speaker {
	if preferred == "off" {
		return nil
	}
	candidates := []string{"espeak", "say"}
	if preferred == "espeak" || preferred == "say" {
		candidates = []string{preferred}
	}
	for _, bin := range candidates {
		if p, err := exec.LookPath(bin); err == nil {
			return &Speaker{bin: p}
		}
	}
	return nil
}

// Speak renders text synchronously. Returns an error when TTS fails.
func (s *Speaker) Speak(text string) error {
	if s == nil {
		return fmt.Errorf("no system TTS available")
	}
	// Truncate absurdly long replies.
	if len(text) > 4000 {
		text = text[:4000]
	}

	ctxTimeout := 60 * time.Second
	var cmd *exec.Cmd
	if s.bin == "say" {
		cmd = exec.Command(s.bin, text) // macOS: say <text>
	} else {
		// espeak: stream text on stdin for reliable quoting.
		cmd = exec.Command(s.bin, "--stdin")
		cmd.Stdin = bytes.NewReader([]byte(text))
	}
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tts start failed: %w", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("tts failed: %w", err)
		}
		return nil
	case <-time.After(ctxTimeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("tts timed out")
	}
}

// Available reports whether a system TTS binary was found.
func (s *Speaker) Available() bool { return s != nil }

package agent

import (
	"testing"

	"github.com/jasperan/picooraclaw/pkg/bus"
	"github.com/jasperan/picooraclaw/pkg/config"
)

func TestAgentLoop_SetEventEmitter(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	cfg := &config.Config{}
	cap := &captureEmitter{}

	al := NewAgentLoop(cfg, msgBus, nil)
	al.SetEventEmitter(cap)

	al.emitter.Emit(Event{Type: EventMessageStart, SessionID: "x"})
	if len(cap.events) != 1 {
		t.Fatalf("expected 1 event captured, got %d", len(cap.events))
	}
}

func TestAgentLoop_SetEventEmitter_NilFallsBackToNoop(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	cfg := &config.Config{}

	al := NewAgentLoop(cfg, msgBus, nil)
	al.SetEventEmitter(nil)

	if al.emitter == nil {
		t.Fatalf("expected emitter to fall back to NoopEmitter, got nil")
	}
	// Should not panic.
	al.emitter.Emit(Event{Type: EventMessageStart, SessionID: "x"})
}

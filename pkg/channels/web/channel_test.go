package web

import (
	"context"
	"testing"

	"github.com/jasperan/picooraclaw/pkg/bus"
	"github.com/jasperan/picooraclaw/pkg/config"
)

func TestNewChannel_StartsAndStops(t *testing.T) {
	cfg := config.WebConfig{Enabled: true, Host: "127.0.0.1", Port: 0, Token: ""}
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	ch, err := NewChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("channel should be running")
	}
	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jasperan/picooraclaw/pkg/bus"
	"github.com/jasperan/picooraclaw/pkg/config"
	"github.com/jasperan/picooraclaw/pkg/logger"
)

type Channel struct {
	cfg     config.WebConfig
	bus     *bus.MessageBus
	broker  *EventBroker
	server  *http.Server
	mu      sync.RWMutex
	running bool
	addr    string
}

func NewChannel(cfg config.WebConfig, msgBus *bus.MessageBus) (*Channel, error) {
	c := &Channel{
		cfg:    cfg,
		bus:    msgBus,
		broker: NewEventBroker(),
	}
	return c, nil
}

func (c *Channel) Name() string { return "web" }

func (c *Channel) Broker() *EventBroker { return c.broker }

func (c *Channel) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	c.registerRoutes(mux)

	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web channel listen %s: %w", addr, err)
	}
	c.addr = ln.Addr().String()

	c.server = &http.Server{
		Handler:           c.authMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	c.setRunning(true)
	logger.InfoCF("web", "web channel listening", map[string]interface{}{"addr": c.addr})

	go func() {
		if err := c.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("web", "server error", map[string]interface{}{"error": err.Error()})
		}
	}()
	return nil
}

func (c *Channel) Stop(ctx context.Context) error {
	c.setRunning(false)
	if c.server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.server.Shutdown(shutdownCtx)
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	c.broker.Emit(Event{Type: "message_text", SessionID: msg.ChatID, Text: msg.Content, Timestamp: time.Now()})
	return nil
}

func (c *Channel) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

func (c *Channel) Addr() string { return c.addr }

func (c *Channel) setRunning(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = v
}

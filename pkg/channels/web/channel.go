package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasperan/picooraclaw/pkg/agent"
	"github.com/jasperan/picooraclaw/pkg/bus"
	"github.com/jasperan/picooraclaw/pkg/config"
	"github.com/jasperan/picooraclaw/pkg/logger"
)

type SessionLister interface {
	ListSessions() []SessionInfo
	CreateSession(title string) (SessionInfo, error)
	DeleteSession(id string) error
}

type SessionInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	LastAt int64  `json:"last_at"`
}

type MemorySearcher interface {
	Search(query string, limit int) []MemoryResult
}

type MemoryResult struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
	Date  int64   `json:"date"`
}

type Channel struct {
	cfg      config.WebConfig
	bus      *bus.MessageBus
	broker   *EventBroker
	server   *http.Server
	mu       sync.RWMutex
	running  bool
	addr     string
	sessions SessionLister
	memory   MemorySearcher
	status   StatusProvider
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

// isLoopbackHost reports whether a configured bind host only exposes the local machine.
// An empty host means "all interfaces" for net.Listen, so it is not loopback.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "::1", "[::1]":
		return true
	}
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (c *Channel) Broker() *EventBroker { return c.broker }

func (c *Channel) Start(ctx context.Context) error {
	if c.IsRunning() {
		return errors.New("web channel already running")
	}

	// Fail closed on the dangerous combination: authMiddleware skips authentication entirely when
	// cfg.Token is empty (server.go), and the library default is Host "0.0.0.0", so enabling this
	// channel without a token would publish the agent UI to the whole network with no access
	// control. Set channels.web.token, or bind a loopback host.
	if c.cfg.Token == "" && !isLoopbackHost(c.cfg.Host) {
		return fmt.Errorf(
			"web channel refuses to listen on %q without a token: set channels.web.token "+
				"(PICOCLAW_CHANNELS_WEB_TOKEN) or bind 127.0.0.1 - the web UI has no other access control",
			c.cfg.Host,
		)
	}

	mux := http.NewServeMux()
	c.registerRoutes(mux)

	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web channel listen %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           c.authMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	c.mu.Lock()
	c.addr = ln.Addr().String()
	c.server = srv
	c.running = true
	c.mu.Unlock()

	logger.InfoCF("web", "web channel listening", map[string]interface{}{"addr": c.addr})

	go func() {
		defer c.setRunning(false)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("web", "server error", map[string]interface{}{"error": err.Error()})
		}
	}()
	return nil
}

func (c *Channel) Stop(ctx context.Context) error {
	c.setRunning(false)
	c.mu.RLock()
	srv := c.server
	c.mu.RUnlock()
	if srv == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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

// IsAllowed returns true for all senders. The web channel is a trusted local
// HTTP endpoint; access control is enforced at the transport layer (bind
// address, auth middleware) rather than per-message.
func (c *Channel) IsAllowed(senderID string) bool { return true }

func (c *Channel) Addr() string { return c.addr }

func (c *Channel) setRunning(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = v
}

// SetSessions attaches a session-store backend. Safe to call before Start().
// When unset, GET returns an empty list and POST/DELETE return 503.
func (c *Channel) SetSessions(s SessionLister) { c.sessions = s }

// SetMemory attaches a memory-search backend. Safe to call before Start().
// When unset, /v1/memory returns an empty JSON array.
func (c *Channel) SetMemory(m MemorySearcher) { c.memory = m }

// Emit satisfies agent.EventEmitter by forwarding into the broker.
func (c *Channel) Emit(e agent.Event) {
	c.broker.Emit(Event{
		Type:       string(e.Type),
		SessionID:  e.SessionID,
		MessageID:  e.MessageID,
		ToolCallID: e.ToolCallID,
		Tool:       e.ToolName,
		Args:       e.Args,
		Result:     e.Result,
		OK:         e.OK,
		Text:       e.Text,
		Error:      e.Error,
		Note:       e.Note,
		Timestamp:  e.Timestamp,
	})
}

// muxForTest exposes the internal mux for tests only.
func (c *Channel) muxForTest() *http.ServeMux {
	m := http.NewServeMux()
	c.registerRoutes(m)
	return m
}

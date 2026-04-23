package web

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (c *Channel) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/chat", c.handleChat)
	mux.HandleFunc("/v1/events", c.handleEvents)
	mux.HandleFunc("/v1/sessions", c.handleSessions)
	mux.HandleFunc("/v1/memory", c.handleMemory)
}

func (c *Channel) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.cfg.Token != "" {
			if r.Header.Get("Authorization") != "Bearer "+c.cfg.Token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Channel) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}
	from := r.URL.Query().Get("from")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := c.broker.Subscribe(sessionID, from)
	defer c.broker.Unsubscribe(sub)

	fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

	enc := json.NewEncoder(w)
	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				return
			}
			fmt.Fprint(w, "data: ")
			_ = enc.Encode(ev) // writes JSON + newline
			fmt.Fprint(w, "\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (c *Channel) handleChat(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
func (c *Channel) handleSessions(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
func (c *Channel) handleMemory(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

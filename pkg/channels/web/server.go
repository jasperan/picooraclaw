package web

import "net/http"

func (c *Channel) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/chat", c.handleChat)
	mux.HandleFunc("/v1/events", c.handleEvents)
	mux.HandleFunc("/v1/sessions", c.handleSessions)
	mux.HandleFunc("/v1/memory", c.handleMemory)
}

func (c *Channel) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.cfg.Token != "" {
			authz := r.Header.Get("Authorization")
			if authz != "Bearer "+c.cfg.Token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Channel) handleChat(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (c *Channel) handleEvents(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (c *Channel) handleSessions(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (c *Channel) handleMemory(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

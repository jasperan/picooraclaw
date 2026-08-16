package web

import (
	"embed"
	"encoding/json"
	"net/http"
)

//go:embed ui/index.html
var uiFS embed.FS

// StatusProvider supplies the dashboard overview (row counts, schema version).
type StatusProvider interface {
	Status() map[string]interface{}
}

// handleUI serves the single-file web UI at "/" with the auth token injected.
func (c *Channel) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	html, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, "ui unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(html)
}

// handleStatus returns the dashboard JSON from the attached StatusProvider.
func (c *Channel) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if c.status == nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_ = json.NewEncoder(w).Encode(c.status.Status())
}

// SetStatus attaches a dashboard provider. Safe to call before Start().
func (c *Channel) SetStatus(sp StatusProvider) { c.status = sp }

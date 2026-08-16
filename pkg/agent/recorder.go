package agent

import (
	"encoding/json"
	"sync"
	"time"
)

// Episode is the durable record of one agent run (Spec 02). The agent package
// defines the shape; persistence is provided by an EpisodeSink (Oracle-backed
// in cmd/picooraclaw, no-op elsewhere).
type Episode struct {
	SessionID  string
	Goal       string
	Trajectory string // JSON: [{tool, ok, args, result}...]
	Outcome    string
	Status     string // success | failed | interrupted
	DurationMS int64
}

// EpisodeSink persists finished episodes.
type EpisodeSink interface {
	SaveEpisode(ep Episode) error
}

// episodeAccumulator tracks one in-flight episode per session.
type episodeAccumulator struct {
	Goal       string
	Trajectory []map[string]interface{}
	Failed     bool
	Started    time.Time
	Ended      time.Time
	events     int
}

// EpisodeRecorder implements EventEmitter and turns the event stream into
// episodes, saving them through the sink. It is a pure no-op when sink is nil.
type EpisodeRecorder struct {
	mu       sync.Mutex
	sink     EpisodeSink
	active   map[string]*episodeAccumulator
	minTools int // minimum tool events before an episode is worth saving
}

// NewEpisodeRecorder creates a recorder. sink may be nil (no-op mode).
func NewEpisodeRecorder(sink EpisodeSink) *EpisodeRecorder {
	return &EpisodeRecorder{sink: sink, active: map[string]*episodeAccumulator{}, minTools: 1}
}

// Emit consumes agent events, accumulating per-session episodes.
func (r *EpisodeRecorder) Emit(e Event) {
	if r == nil || r.sink == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	acc := r.active[e.SessionID]
	if acc == nil {
		acc = &episodeAccumulator{Started: e.Timestamp}
		r.active[e.SessionID] = acc
	}

	switch e.Type {
	case EventMessageStart:
		if e.Text != "" && acc.Goal == "" {
			acc.Goal = e.Text
		}
	case EventToolCallStart:
		acc.events++
		acc.Trajectory = append(acc.Trajectory, map[string]interface{}{
			"tool": e.ToolName, "ok": nil, "args": e.Args, "id": e.ToolCallID, "ts": e.Timestamp,
		})
	case EventToolCallEnd:
		acc.events++
		entry := map[string]interface{}{
			"tool": e.ToolName, "ok": e.OK != nil && *e.OK, "ts": e.Timestamp,
		}
		if e.Result != "" {
			entry["result"] = truncate(e.Result, 2000)
		}
		// Merge with the matching start entry (same id) when possible.
		matched := false
		if e.ToolCallID != "" {
			for i := len(acc.Trajectory) - 1; i >= 0; i-- {
				if id, _ := acc.Trajectory[i]["id"].(string); id == e.ToolCallID {
					acc.Trajectory[i] = entry
					matched = true
					break
				}
			}
		}
		if !matched {
			acc.Trajectory = append(acc.Trajectory, entry)
		}
		if e.OK != nil && !*e.OK {
			acc.Failed = true
		}
	case EventError:
		acc.Failed = true
	case EventMessageEnd:
		acc.Ended = e.Timestamp
		r.finalize(e.SessionID, e.Text)
	}
}

// finalize builds the episode and hands it to the sink, then clears state.
func (r *EpisodeRecorder) finalize(sessionID, outcome string) {
	acc := r.active[sessionID]
	if acc == nil {
		return
	}
	delete(r.active, sessionID)

	if acc.Goal == "" || len(acc.Trajectory) < r.minTools {
		return // noise: no goal or no tool activity worth replaying
	}

	status := "success"
	if acc.Failed {
		status = "failed"
	}
	trajJSON, _ := json.Marshal(acc.Trajectory)
	duration := acc.Ended.Sub(acc.Started).Milliseconds()
	if duration < 0 {
		duration = 0
	}

	ep := Episode{
		SessionID:  sessionID,
		Goal:       truncate(acc.Goal, 4000),
		Trajectory: string(trajJSON),
		Outcome:    truncate(outcome, 4000),
		Status:     status,
		DurationMS: duration,
	}
	_ = r.sink.SaveEpisode(ep)
}

// Flush finalizes any still-open episodes (called at shutdown).
func (r *EpisodeRecorder) Flush() {
	if r == nil || r.sink == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for sessionID := range r.active {
		r.finalize(sessionID, "")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

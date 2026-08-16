package agent

import (
	"testing"
	"time"
)

type fakeSink struct {
	episodes []Episode
}

func (f *fakeSink) SaveEpisode(ep Episode) error {
	f.episodes = append(f.episodes, ep)
	return nil
}

func TestEpisodeRecorder_SavesSuccessfulEpisode(t *testing.T) {
	sink := &fakeSink{}
	r := NewEpisodeRecorder(sink)
	ts := time.Now()

	r.Emit(Event{Type: EventMessageStart, SessionID: "s1", Text: "fix the oracle error", Timestamp: ts})
	r.Emit(Event{Type: EventToolCallStart, SessionID: "s1", ToolName: "recall", ToolCallID: "tc1", Timestamp: ts.Add(time.Second)})
	ok := true
	r.Emit(Event{Type: EventToolCallEnd, SessionID: "s1", ToolName: "recall", ToolCallID: "tc1", Result: "found it", OK: &ok, Timestamp: ts.Add(2 * time.Second)})
	r.Emit(Event{Type: EventMessageEnd, SessionID: "s1", Text: "done", Timestamp: ts.Add(3 * time.Second)})

	if len(sink.episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(sink.episodes))
	}
	ep := sink.episodes[0]
	if ep.Goal != "fix the oracle error" {
		t.Errorf("goal = %q", ep.Goal)
	}
	if ep.Outcome != "done" {
		t.Errorf("outcome = %q", ep.Outcome)
	}
	if ep.Status != "success" {
		t.Errorf("status = %q", ep.Status)
	}
	if ep.DurationMS <= 0 {
		t.Errorf("duration_ms = %d", ep.DurationMS)
	}
}

func TestEpisodeRecorder_MarksFailure(t *testing.T) {
	sink := &fakeSink{}
	r := NewEpisodeRecorder(sink)
	ts := time.Now()

	r.Emit(Event{Type: EventMessageStart, SessionID: "s2", Text: "deploy", Timestamp: ts})
	r.Emit(Event{Type: EventToolCallStart, SessionID: "s2", ToolName: "exec", ToolCallID: "t1", Timestamp: ts})
	fail := false
	r.Emit(Event{Type: EventToolCallEnd, SessionID: "s2", ToolName: "exec", ToolCallID: "t1", Result: "exit 1", OK: &fail, Timestamp: ts.Add(time.Second)})
	r.Emit(Event{Type: EventMessageEnd, SessionID: "s2", Text: "failed", Timestamp: ts.Add(2 * time.Second)})

	if len(sink.episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(sink.episodes))
	}
	if sink.episodes[0].Status != "failed" {
		t.Errorf("status = %q, want failed", sink.episodes[0].Status)
	}
}

func TestEpisodeRecorder_SkipsNoise(t *testing.T) {
	sink := &fakeSink{}
	r := NewEpisodeRecorder(sink)
	ts := time.Now()

	// A plain Q&A without tool calls is not an episode.
	r.Emit(Event{Type: EventMessageStart, SessionID: "s3", Text: "hello", Timestamp: ts})
	r.Emit(Event{Type: EventMessageEnd, SessionID: "s3", Text: "hi!", Timestamp: ts.Add(time.Second)})

	if len(sink.episodes) != 0 {
		t.Fatalf("expected no episode for plain chat, got %d", len(sink.episodes))
	}
}

func TestEpisodeRecorder_NilSinkIsNoop(t *testing.T) {
	r := NewEpisodeRecorder(nil)
	r.Emit(Event{Type: EventMessageStart, SessionID: "s4", Text: "x", Timestamp: time.Now()})
	r.Emit(Event{Type: EventMessageEnd, SessionID: "s4", Text: "y", Timestamp: time.Now()})
	// must not panic
}

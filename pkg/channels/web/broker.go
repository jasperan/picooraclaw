package web

import "time"

type Event struct {
	Type      string    `json:"type"`
	SessionID string    `json:"session_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"ts"`
}

type EventBroker struct{}

func NewEventBroker() *EventBroker { return &EventBroker{} }

func (b *EventBroker) Emit(e Event) {}

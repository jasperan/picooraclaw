package tools

import (
	"context"
	"fmt"
)

// Brain is the interface the brain tool needs for analytics over agent data.
type Brain interface {
	Report(topic string, days int) (string, error)
}

// BrainTool answers "what did we work on / which tools do I overuse / what
// happened last week" from the agent's own stored data (Spec 04).
type BrainTool struct {
	brain Brain
}

// NewBrainTool creates the analytics tool.
func NewBrainTool(b Brain) *BrainTool {
	return &BrainTool{brain: b}
}

func (t *BrainTool) Name() string { return "brain" }

func (t *BrainTool) Description() string {
	return "Query the agent's own history. Use when the user asks about past activity: 'what did we work on last week', 'how many conversations yesterday', 'which tools do I use most'. Topics: activity, topics, tools, channels, sessions, week."
}

func (t *BrainTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"question": map[string]interface{}{
				"type":        "string",
				"description": "One of: activity, topics, tools, channels, sessions, week",
				"enum":        []string{"activity", "topics", "tools", "channels", "sessions", "week"},
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Window in days (default 7)",
			},
		},
		"required": []string{"question"},
	}
}

func (t *BrainTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.brain == nil {
		return ErrorResult("brain analytics not available (Oracle disabled?)")
	}
	question, _ := args["question"].(string)
	if question == "" {
		return ErrorResult("question parameter is required")
	}
	days := 7
	if d, ok := args["days"].(float64); ok && d > 0 {
		days = int(d)
	}
	out, err := t.brain.Report(question, days)
	if err != nil {
		return ErrorResult(fmt.Sprintf("brain query failed: %v", err))
	}
	return NewToolResult(out)
}

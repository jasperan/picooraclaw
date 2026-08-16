package tools

import (
	"context"
	"fmt"
	"strings"
)

// RecallResult represents a recalled memory.
type RecallResult struct {
	MemoryID   string
	Text       string
	Importance float64
	Category   string
	Score      float64
}

// Recaller is the interface the recall tool needs for semantic memory search.
type Recaller interface {
	Recall(query string, maxResults int) ([]RecallResult, error)
}

// RecallRequest carries the optional hybrid-retrieval controls.
type RecallRequest struct {
	Query      string
	MaxResults int
	Category   string
	Days       int
	Lexical    bool   // default true
	Scope      string // "memories" (default) | "transcripts" | "episodes"
}

// ExtendedRecaller is implemented by stores that support scoped hybrid recall.
type ExtendedRecaller interface {
	RecallEx(req RecallRequest) ([]RecallResult, error)
}

// RecallTool provides the "recall" tool for semantic memory search.
type RecallTool struct {
	store Recaller
}

// NewRecallTool creates a new recall tool.
func NewRecallTool(store Recaller) *RecallTool {
	return &RecallTool{store: store}
}

func (t *RecallTool) Name() string { return "recall" }

func (t *RecallTool) Description() string {
	return "Search long-term memory using semantic similarity. Use this to find previously remembered information by describing what you're looking for. Set scope='transcripts' to search past conversations, scope='episodes' to replay how past tasks were solved (steps + outcomes), or pass category/days to filter memories."
}

func (t *RecallTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query describing what to recall",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5)",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "Only return memories in this category (e.g. preference, fact, interest)",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Only return memories created within this many days",
			},
			"lexical": map[string]interface{}{
				"type":        "boolean",
				"description": "Also match exact terms (default true). Disable for pure semantic search.",
			},
			"scope": map[string]interface{}{
				"type":        "string",
				"description": "Where to search: 'memories' (default), 'transcripts' (past conversations), 'episodes' (past problem-solving runs)",
				"enum":        []string{"memories", "transcripts", "episodes"},
			},
		},
		"required": []string{"query"},
	}
}

func (t *RecallTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query parameter is required")
	}

	req := RecallRequest{
		Query:      query,
		MaxResults: 5,
		Lexical:    true,
		Scope:      "memories",
	}
	if mr, ok := args["max_results"].(float64); ok && mr > 0 {
		req.MaxResults = int(mr)
	}
	if c, ok := args["category"].(string); ok && c != "" {
		req.Category = c
	}
	if d, ok := args["days"].(float64); ok && d > 0 {
		req.Days = int(d)
	}
	if l, ok := args["lexical"].(bool); ok {
		req.Lexical = l
	}
	if s, ok := args["scope"].(string); ok && s != "" {
		req.Scope = s
	}

	var results []RecallResult
	var err error

	if ext, ok := t.store.(ExtendedRecaller); ok {
		results, err = ext.RecallEx(req)
	} else {
		if req.Scope != "memories" {
			return ErrorResult(fmt.Sprintf("scope %q requires an Oracle-backed store", req.Scope))
		}
		results, err = t.store.Recall(query, req.MaxResults)
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("Recall failed: %v", err))
	}

	if len(results) == 0 {
		return NewToolResult("No matching memories found for: " + query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matching %s:\n\n", len(results), req.Scope))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [%.0f%% match] (ID: %s", i+1, r.Score*100, r.MemoryID))
		if r.Category != "" {
			sb.WriteString(fmt.Sprintf(", category: %s", r.Category))
		}
		sb.WriteString(fmt.Sprintf(", importance: %.1f)\n", r.Importance))
		sb.WriteString(fmt.Sprintf("   %s\n\n", r.Text))
	}

	return NewToolResult(sb.String())
}

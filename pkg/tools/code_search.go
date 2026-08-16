package tools

import (
	"context"
	"fmt"
	"strings"
)

// CodeSearcher is the interface the code_search tool needs.
type CodeSearcher interface {
	SearchNL(repo, query string, limit int) ([]CodeSearchHit, error)
	CallersOf(repo, symbol string, depth, limit int) ([]CodeSearchHit, error)
	CalleesOf(repo, symbol string, depth, limit int) ([]CodeSearchHit, error)
}

// CodeSearchHit mirrors the oracle result shape (avoiding an import).
type CodeSearchHit struct {
	NodeID    string
	Kind      string
	Name      string
	Path      string
	Line      int
	Signature string
	Doc       string
	Score     float64
	Callers   []string
	Callees   []string
}

// CodeSearchTool answers natural-language and structural code questions
// against the indexed repository (Spec 03).
type CodeSearchTool struct {
	graph CodeSearcher
	repo  string
}

// NewCodeSearchTool creates the tool bound to an indexed repo.
func NewCodeSearchTool(graph CodeSearcher, repo string) *CodeSearchTool {
	return &CodeSearchTool{graph: graph, repo: repo}
}

func (t *CodeSearchTool) Name() string { return "code_search" }

func (t *CodeSearchTool) Description() string {
	return "Search the indexed codebase. Use for natural-language queries ('find the code that handles X'), tracing callers ('who calls EmbedText'), or callees ('what does HandleEvents call'). Returns real file paths + line numbers."
}

func (t *CodeSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Natural-language intent, e.g. 'handles SSE events'",
			},
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Exact symbol to locate (e.g. 'EmbedText')",
			},
			"callers_of": map[string]interface{}{
				"type":        "string",
				"description": "Find who calls this symbol",
			},
			"what_calls": map[string]interface{}{
				"type":        "string",
				"description": "Find what this symbol calls",
			},
			"depth": map[string]interface{}{
				"type":        "integer",
				"description": "Traversal depth for caller/callee queries (default 2)",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Max results (default 8)",
			},
		},
	}
}

func (t *CodeSearchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.graph == nil || t.repo == "" {
		return ErrorResult("code search not available: run `picooraclaw index <path>` first (Oracle required)")
	}

	query, _ := args["query"].(string)
	symbol, _ := args["symbol"].(string)
	callersOf, _ := args["callers_of"].(string)
	whatCalls, _ := args["what_calls"].(string)
	limit := 8
	if l, ok := args["max_results"].(float64); ok && l > 0 {
		limit = int(l)
	}
	depth := 2
	if d, ok := args["depth"].(float64); ok && d > 0 {
		depth = int(d)
	}

	var hits []CodeSearchHit
	var err error
	switch {
	case callersOf != "":
		hits, err = t.graph.CallersOf(t.repo, callersOf, depth, limit)
	case whatCalls != "":
		hits, err = t.graph.CalleesOf(t.repo, whatCalls, depth, limit)
	case symbol != "":
		hits, err = t.graph.CallersOf(t.repo, symbol, 1, limit)
	case query != "":
		hits, err = t.graph.SearchNL(t.repo, query, limit)
	default:
		return ErrorResult("provide one of: query, symbol, callers_of, what_calls")
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("code search failed: %v", err))
	}
	if len(hits) == 0 {
		return NewToolResult("No code matches found.")
	}

	var sb strings.Builder
	verb := "matches"
	if callersOf != "" {
		verb = "callers of " + callersOf
	} else if whatCalls != "" {
		verb = "callees of " + whatCalls
	}
	sb.WriteString(fmt.Sprintf("Found %d %s:\n", len(hits), verb))
	for i, h := range hits {
		sb.WriteString(fmt.Sprintf("%d. [%.0f%%] %s:%d  %s %s\n", i+1, h.Score*100, h.Path, h.Line, h.Kind, h.Name))
		if h.Signature != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", h.Signature))
		}
		if h.Doc != "" {
			d := strings.TrimSpace(h.Doc)
			if len(d) > 140 {
				d = d[:140] + "…"
			}
			sb.WriteString(fmt.Sprintf("   %s\n", d))
		}
	}
	sb.WriteString("\nUse read_file with these paths for details.")
	return NewToolResult(sb.String())
}

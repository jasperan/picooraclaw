package oracle

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// AnalyticsReport is a named, pre-rendered breakdown over agent data.
type AnalyticsReport struct {
	Title string
	Lines []string
}

// ActivityReport returns per-day message/session counts for the window.
func ActivityReport(db *sql.DB, agentID string, days int) (*AnalyticsReport, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := db.Query(`
		SELECT TO_CHAR(TRUNC(created_at), 'YYYY-MM-DD') d, COUNT(*) msgs,
		       COUNT(DISTINCT session_key) sessions,
		       SUM(CASE WHEN role = 'user' THEN 1 ELSE 0 END) user_msgs
		FROM PICO_TRANSCRIPTS
		WHERE agent_id = :1 AND created_at >= SYSDATE - :2
		GROUP BY TRUNC(created_at) ORDER BY d`, agentID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	r := &AnalyticsReport{Title: fmt.Sprintf("Activity — last %d days", days)}
	for rows.Next() {
		var d string
		var msgs, sessions, userMsgs int
		if err := rows.Scan(&d, &msgs, &sessions, &userMsgs); err != nil {
			continue
		}
		r.Lines = append(r.Lines, fmt.Sprintf("%s  %4d msgs (%d user)  %d sessions", d, msgs, userMsgs, sessions))
	}
	if len(r.Lines) == 0 {
		r.Lines = append(r.Lines, "(no transcripts in window)")
	}
	return r, nil
}

var analyticsStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "what": true,
	"how": true, "why": true, "when": true, "where": true, "who": true,
	"that": true, "this": true, "from": true, "have": true, "was": true,
	"were": true, "are": true, "you": true, "your": true, "about": true,
	"into": true, "will": true, "can": true, "could": true,
	"did": true, "does": true, "not": true, "has": true, "been": true,
	"please": true, "help": true, "need": true, "want": true, "get": true,
	"let": true, "know": true, "just": true, "like": true, "make": true,
	"use": true, "using": true, "there": true, "here": true, "should": true,
	"tell": true, "show": true, "give": true,
}

// TopicReport extracts frequent content tokens from user messages (v1
// approximation; Spec 01 embeddings enable semantic clustering as v2).
func TopicReport(db *sql.DB, agentID string, days, topN int) (*AnalyticsReport, error) {
	if days <= 0 {
		days = 30
	}
	if topN <= 0 {
		topN = 20
	}
	rows, err := db.Query(`
		SELECT content FROM PICO_TRANSCRIPTS
		WHERE agent_id = :1 AND role = 'user' AND content IS NOT NULL
		  AND created_at >= SYSDATE - :2
		FETCH FIRST 500 ROWS ONLY`, agentID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var content sql.NullString
		if err := rows.Scan(&content); err != nil || !content.Valid {
			continue
		}
		for _, tok := range strings.FieldsFunc(strings.ToLower(content.String), func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
		}) {
			if len(tok) < 3 || analyticsStopwords[tok] {
				continue
			}
			counts[tok]++
		}
	}

	type pair struct {
		tok string
		n   int
	}
	var pairs []pair
	for tok, n := range counts {
		pairs = append(pairs, pair{tok, n})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].n > pairs[j].n })
	if len(pairs) > topN {
		pairs = pairs[:topN]
	}

	r := &AnalyticsReport{Title: fmt.Sprintf("Topics — top %d frequent terms (last %d days)", topN, days)}
	if len(pairs) == 0 {
		r.Lines = append(r.Lines, "(no user messages in window)")
		return r, nil
	}
	maxN := pairs[0].n
	for _, p := range pairs {
		bar := strings.Repeat("█", int(20*float64(p.n)/float64(maxN)))
		r.Lines = append(r.Lines, fmt.Sprintf("%-18s %4d  %s", p.tok, p.n, bar))
	}
	return r, nil
}

// SessionReport returns sessions with message counts and summary previews.
func SessionReport(db *sql.DB, agentID string, limit int) (*AnalyticsReport, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(`
		SELECT s.session_key,
		       (SELECT COUNT(*) FROM PICO_TRANSCRIPTS t WHERE t.session_key = s.session_key AND t.agent_id = :1) msgs,
		       NVL(s.summary, '(no summary)'),
		       TO_CHAR(s.updated_at, 'YYYY-MM-DD HH24:MI')
		FROM PICO_SESSIONS s
		WHERE s.agent_id = :1
		ORDER BY s.updated_at DESC
		FETCH FIRST :2 ROWS ONLY`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	r := &AnalyticsReport{Title: fmt.Sprintf("Sessions — last %d by activity", limit)}
	for rows.Next() {
		var key, summary, updated string
		var msgs int
		if err := rows.Scan(&key, &msgs, &summary, &updated); err != nil {
			continue
		}
		preview := summary
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		r.Lines = append(r.Lines, fmt.Sprintf("%-28s %4d msgs  %s\n    %s", key, msgs, updated, preview))
	}
	if len(r.Lines) == 0 {
		r.Lines = append(r.Lines, "(no sessions)")
	}
	return r, nil
}

// ToolReport returns per-tool usage from agent state (counters recorded by the
// loop) plus channel breakdown.
func ToolReport(db *sql.DB, agentID string) (*AnalyticsReport, error) {
	r := &AnalyticsReport{Title: "Tool usage (from state)"}
	rows, err := db.Query(`
		SELECT state_key, state_value FROM PICO_STATE
		WHERE agent_id = :1 AND state_key LIKE 'tool_%' ORDER BY state_key`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		keys = append(keys, fmt.Sprintf("  %-24s %s", strings.TrimPrefix(k, "tool_"), v))
	}
	if len(keys) == 0 {
		keys = append(keys, "  (no per-tool counters recorded yet)")
	}
	r.Lines = keys

	ch, err := ChannelReport(db, agentID, 30)
	if err == nil {
		r.Lines = append(r.Lines, "", ch.Title)
		r.Lines = append(r.Lines, ch.Lines...)
	}
	return r, nil
}

// ChannelReport returns per-channel message counts and last activity.
func ChannelReport(db *sql.DB, agentID string, days int) (*AnalyticsReport, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := db.Query(`
		SELECT CASE
		         WHEN session_key LIKE 'web:%' THEN 'web'
		         WHEN session_key LIKE 'telegram:%' THEN 'telegram'
		         WHEN session_key LIKE 'discord:%' THEN 'discord'
		         WHEN session_key LIKE 'slack:%' THEN 'slack'
		         WHEN session_key LIKE 'whatsapp:%' THEN 'whatsapp'
		         WHEN session_key LIKE 'cli:%' THEN 'cli'
		         ELSE 'other' END channel,
		       COUNT(*) msgs,
		       MAX(created_at) last_at
		FROM PICO_TRANSCRIPTS
		WHERE agent_id = :1 AND created_at >= SYSDATE - :2
		GROUP BY CASE
		         WHEN session_key LIKE 'web:%' THEN 'web'
		         WHEN session_key LIKE 'telegram:%' THEN 'telegram'
		         WHEN session_key LIKE 'discord:%' THEN 'discord'
		         WHEN session_key LIKE 'slack:%' THEN 'slack'
		         WHEN session_key LIKE 'whatsapp:%' THEN 'whatsapp'
		         WHEN session_key LIKE 'cli:%' THEN 'cli'
		         ELSE 'other' END
		ORDER BY msgs DESC`, agentID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	r := &AnalyticsReport{Title: fmt.Sprintf("Channels — last %d days", days)}
	for rows.Next() {
		var ch string
		var msgs int
		var last time.Time
		if err := rows.Scan(&ch, &msgs, &last); err != nil {
			continue
		}
		r.Lines = append(r.Lines, fmt.Sprintf("  %-12s %4d msgs  (last %s)", ch, msgs, last.Format("2006-01-02 15:04")))
	}
	if len(r.Lines) == 0 {
		r.Lines = append(r.Lines, "  (no transcripts in window)")
	}
	return r, nil
}

// Digest assembles the raw markdown material for the digest command: activity
// stats, topics, new memories, and recent sessions.
func Digest(db *sql.DB, agentID string, days int) (*AnalyticsReport, error) {
	if days <= 0 {
		days = 7
	}
	r := &AnalyticsReport{Title: fmt.Sprintf("Digest — last %d days", days)}
	reports := []*AnalyticsReport{}
	if act, err := ActivityReport(db, agentID, days); err == nil {
		reports = append(reports, act)
	}
	if top, err := TopicReport(db, agentID, days, 20); err == nil {
		reports = append(reports, top)
	}
	for _, sub := range reports {
		r.Lines = append(r.Lines, "", sub.Title)
		r.Lines = append(r.Lines, sub.Lines...)
	}

	mem, err := RecentMemories(db, agentID, days, 10)
	if err == nil {
		r.Lines = append(r.Lines, "", fmt.Sprintf("New memories (last %d days)", days))
		r.Lines = append(r.Lines, mem...)
	}

	ses, err := SessionReport(db, agentID, 10)
	if err == nil {
		r.Lines = append(r.Lines, "", ses.Title)
		r.Lines = append(r.Lines, ses.Lines...)
	}
	return r, nil
}

// RecentMemories returns recent memory lines (id, importance, category, text).
func RecentMemories(db *sql.DB, agentID string, days, limit int) ([]string, error) {
	rows, err := db.Query(`
		SELECT memory_id, content, importance, category,
		       TO_CHAR(created_at, 'YYYY-MM-DD HH24:MI')
		FROM PICO_MEMORIES
		WHERE agent_id = :1 AND created_at >= SYSDATE - :2
		ORDER BY created_at DESC
		FETCH FIRST :3 ROWS ONLY`, agentID, days, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var id, created string
		var content, category sql.NullString
		var importance float64
		if err := rows.Scan(&id, &content, &importance, &category, &created); err != nil {
			continue
		}
		text := "(empty)"
		if content.Valid {
			text = content.String
		}
		if len(text) > 100 {
			text = text[:100] + "…"
		}
		cat := ""
		if category.Valid && category.String != "" {
			cat = " [" + category.String + "]"
		}
		lines = append(lines, fmt.Sprintf("  %s  %.1f%s  %s", created, importance, cat, text))
	}
	return lines, nil
}

// TableCounts returns row counts for the PICO_ tables plus schema_version,
// used by oracle-inspect overview and the web dashboard.
func TableCounts(db *sql.DB, agentID string) map[string]interface{} {
	out := map[string]interface{}{}
	tables := []struct {
		name  string
		label string
	}{
		{"PICO_MEMORIES", "memories"},
		{"PICO_SESSIONS", "sessions"},
		{"PICO_TRANSCRIPTS", "transcripts"},
		{"PICO_STATE", "state"},
		{"PICO_DAILY_NOTES", "notes"},
		{"PICO_PROMPTS", "prompts"},
		{"PICO_CONFIG", "config"},
		{"PICO_META", "meta"},
		{"PICO_EPISODES", "episodes"},
		{"PICO_CODE_NODES", "code_nodes"},
		{"PICO_CODE_EDGES", "code_edges"},
		{"PICO_SKILL_USAGE", "skills"},
	}
	total := int64(0)
	for _, t := range tables {
		var count int64
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", t.name)).Scan(&count); err != nil {
			continue
		}
		out[t.label] = count
		total += count
	}
	out["total"] = total
	if v := getMetaValue(db, "schema_version"); v != "" {
		out["schema_version"] = v
	}
	return out
}

// Render returns the report as a single markdown-ish string.
func (r *AnalyticsReport) Render() string {
	var sb strings.Builder
	sb.WriteString("== " + r.Title + " ==\n")
	for _, l := range r.Lines {
		sb.WriteString(l + "\n")
	}
	return sb.String()
}

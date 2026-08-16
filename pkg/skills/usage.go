package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// usageFile is the file-mode usage record (Oracle mode uses PICO_SKILL_USAGE).
const usageFile = ".usage.json"

// SkillUsage is a single skill's tracked usage.
type SkillUsage struct {
	Name     string    `json:"name"`
	UseCount int       `json:"use_count"`
	LastUsed time.Time `json:"last_used"`
}

// UsageRecorder tracks skill invocations with a file backend (Oracle-off
// parity; see pkg/oracle/skill_store.go for the database backend).
type UsageRecorder struct {
	workspace string
	mu        sync.Mutex
	data      map[string]SkillUsage
}

// NewUsageRecorder loads (or initializes) the usage file.
func NewUsageRecorder(workspace string) *UsageRecorder {
	r := &UsageRecorder{workspace: workspace, data: map[string]SkillUsage{}}
	path := filepath.Join(workspace, "skills", usageFile)
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &r.data)
	}
	return r
}

// Record bumps the counter for a skill and persists.
func (r *UsageRecorder) Record(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.data[name]
	u.Name = name
	u.UseCount++
	u.LastUsed = time.Now()
	r.data[name] = u
	r.saveLocked()
}

// List returns usage sorted by count descending.
func (r *UsageRecorder) List() []SkillUsage {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SkillUsage, 0, len(r.data))
	for _, u := range r.data {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UseCount > out[j].UseCount })
	return out
}

// Count returns the recorded use count for a skill (0 when unknown).
func (r *UsageRecorder) Count(name string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data[name].UseCount
}

func (r *UsageRecorder) saveLocked() {
	path := filepath.Join(r.workspace, "skills", usageFile)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if raw, err := json.MarshalIndent(r.data, "", "  "); err == nil {
		_ = os.WriteFile(path, raw, 0644)
	}
}

// SearchInstalled filters installed skills by substring match on name and
// description. Used as the Oracle-off fallback for `skills find`.
func SearchInstalled(skills []SkillInfo, query string) []SkillInfo {
	q := strings.ToLower(query)
	var out []SkillInfo
	for _, s := range skills {
		if strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Description), q) {
			out = append(out, s)
		}
	}
	return out
}

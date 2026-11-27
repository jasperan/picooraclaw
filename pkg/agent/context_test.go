package agent

import (
	"strings"
	"testing"
)

type mapPromptStore map[string]string

func (m mapPromptStore) LoadBootstrapFiles() map[string]string { return m }

func TestLoadBootstrapFilesFromPromptStoreIsStable(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	cb.SetPromptStore(mapPromptStore{
		"IDENTITY": "i", "USER": "u", "SOUL": "s", "AGENT": "a", "ZETA": "z", "ALPHA": "x",
	})

	first := cb.LoadBootstrapFiles()
	for i := 0; i < 50; i++ {
		if got := cb.LoadBootstrapFiles(); got != first {
			t.Fatalf("bootstrap prompt changed between calls:\n%q\n%q", first, got)
		}
	}

	var order []string
	for _, line := range strings.Split(first, "\n") {
		if strings.HasPrefix(line, "## ") {
			order = append(order, strings.TrimPrefix(line, "## "))
		}
	}
	want := []string{"AGENT", "SOUL", "USER", "IDENTITY", "ALPHA", "ZETA"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

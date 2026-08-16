package oracle

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestParseRepo_GoFiles(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.25\n",
		"main.go": `package main

// Entry point.
func main() {
	greet("world")
}

// greet says hello.
func greet(name string) string {
	return "hello " + name
}
`,
		"util/util.go": `package util

import "fmt"

// Print prints s.
func Print(s string) { fmt.Println(s) }
`,
	})

	res, err := ParseRepo(root, DefaultRepoIndexOptions())
	if err != nil {
		t.Fatal(err)
	}

	// Expect: 2 file nodes + 2 funcs in main.go + 1 func in util.go (+ no types).
	if len(res.Nodes) < 5 {
		t.Fatalf("expected >=5 nodes, got %d", len(res.Nodes))
	}
	var names []string
	for _, n := range res.Nodes {
		if n.Kind == "func" {
			names = append(names, n.Name)
		}
	}
	if !contains(names, "main") || !contains(names, "greet") || !contains(names, "Print") {
		t.Fatalf("func nodes missing, got %v", names)
	}

	// Call edge: main.go's main → greet (same file).
	foundCall := false
	for _, e := range res.Edges {
		if e.Kind == "calls" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatalf("expected a calls edge (main → greet), got %d edges", len(res.Edges))
	}
}

func TestParseRepo_PythonHeuristic(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"app.py": `import helpers

def run():
    return helpers.work()
`,
		"helpers.py": `def work():
    return 42
`,
	})
	res, err := ParseRepo(root, DefaultRepoIndexOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) < 3 { // 2 files + 2 funcs
		t.Fatalf("expected >=3 nodes, got %d", len(res.Nodes))
	}
	foundImport := false
	for _, e := range res.Edges {
		if e.Kind == "imports" {
			foundImport = true
		}
	}
	if !foundImport {
		t.Fatalf("expected an imports edge (app.py → helpers.py), got %d edges", len(res.Edges))
	}
}

func TestParseRepo_ExcludesDirs(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"node_modules/pkg/index.js": "const x = 1;\n",
		"src/real.go":               "package real\n",
	})
	res, err := ParseRepo(root, DefaultRepoIndexOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Nodes {
		if containsPath(n.Path, "node_modules") {
			t.Fatalf("node_modules file indexed: %s", n.Path)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func containsPath(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

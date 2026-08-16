package oracle

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ParseResult is the raw output of a repo walk before persistence.
type ParseResult struct {
	Nodes []CodeNode
	Edges []CodeEdge
	// pendingImports are file→module edges resolved in a post-pass once every
	// file node exists (imports may reference files parsed later).
	pendingImports []pendingImport
}

type pendingImport struct {
	Src string
	Mod string
}

// RepoIndexOptions controls the walk.
type RepoIndexOptions struct {
	// ExcludeDirs are directory names skipped at any depth.
	ExcludeDirs map[string]bool
	// MaxNodes caps inserted nodes (protects embedding cost).
	MaxNodes int
}

// DefaultRepoIndexOptions skips the usual vendored/generated directories.
func DefaultRepoIndexOptions() RepoIndexOptions {
	return RepoIndexOptions{
		ExcludeDirs: map[string]bool{
			".git": true, "node_modules": true, "build": true, "vendor": true,
			"dist": true, "coverage": true, ".ckg": true, ".venv": true,
			"__pycache__": true, "remotion": true,
		},
		MaxNodes: 5000,
	}
}

// ParseRepo walks root and produces code nodes + edges.
func ParseRepo(root string, opts RepoIndexOptions) (*ParseResult, error) {
	res := &ParseResult{}
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			if opts.ExcludeDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".py", ".js", ".ts", ".mjs", ".cjs":
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".go":
			parseGoFile(res, root, f)
		case ".py", ".js", ".ts", ".mjs", ".cjs":
			parseGenericFile(res, root, f)
		}
		if len(res.Nodes) > opts.MaxNodes {
			break
		}
	}

	// Post-pass: resolve import edges now that all file nodes exist.
	for _, pi := range res.pendingImports {
		if dst := res.findFileNode(pi.Mod); dst != "" {
			res.Edges = append(res.Edges, CodeEdge{Repo: "", Src: pi.Src, Dst: dst, Kind: "imports", Weight: 1})
		}
	}
	res.dedupeEdges()
	return res, nil
}

// dedupeEdges collapses edges with identical IDs (multiple call sites of the
// same function or repeated import candidates) so UpsertEdges never hits the
// unique constraint.
func (res *ParseResult) dedupeEdges() {
	seen := make(map[string]bool, len(res.Edges))
	out := res.Edges[:0]
	for _, e := range res.Edges {
		id := CodeEdgeID(e.Kind, e.Src, e.Dst)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, e)
	}
	res.Edges = out
}

// addImport queues an import edge for the post-pass.
func (res *ParseResult) addImport(src, mod string) {
	if mod != "" && src != "" {
		res.pendingImports = append(res.pendingImports, pendingImport{Src: src, Mod: mod})
	}
}

// relPath returns the path relative to root, slash-separated.
func relPath(root, path string) string {
	rp, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rp)
}

// parseGoFile extracts file/package/func/type nodes and import/call edges.
func parseGoFile(res *ParseResult, root, path string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return
	}
	rp := relPath(root, path)

	// File node: package + imports + first doc comment.
	var imports []string
	for _, imp := range f.Imports {
		if imp.Path != nil {
			imports = append(imports, strings.Trim(imp.Path.Value, `"`))
		}
	}
	doc := f.Doc
	fileNode := CodeNode{
		Kind:      "file",
		Name:      filepath.Base(rp),
		Path:      rp,
		Summary:   "package " + f.Name.Name + " | imports: " + strings.Join(imports, ", "),
		StartLine: 1,
		EndLine:   fset.Position(f.End()).Line,
	}
	if doc != nil {
		fileNode.Doc = doc.Text()
	}
	res.Nodes = append(res.Nodes, fileNode)
	fileID := CodeNodeID("file", rp, fileNode.Name)

	// Import edges: file → sibling package dirs (best-effort resolution).
	dir := filepath.Dir(rp)
	pkgName := f.Name.Name
	for _, imp := range imports {
		base := pathBase(imp)
		if base == "" {
			continue
		}
		// candidate sibling paths where the package lives
		cands := []string{
			filepath.ToSlash(filepath.Join(dir, base)),
			base,
		}
		for _, c := range cands {
			res.addImport(fileID, c)
		}
	}

	// Symbol index within this file for call resolution.
	symbols := map[string]CodeNode{}

	addFunc := func(name, sig string, fd *ast.FuncDecl, kind string) string {
		node := CodeNode{
			Kind: kind, Name: name, Path: rp, Signature: sig,
			StartLine: fset.Position(fd.Pos()).Line,
			EndLine:   fset.Position(fd.End()).Line,
		}
		if fd.Doc != nil {
			node.Doc = fd.Doc.Text()
		}
		node.Summary = summarizeFunc(sig, fd.Doc, fd.Body)
		res.Nodes = append(res.Nodes, node)
		id := CodeNodeID(kind, rp, name)
		symbols[name] = node
		res.Edges = append(res.Edges, CodeEdge{Repo: "", Src: fileID, Dst: id, Kind: "defines", Weight: 1})
		return id
	}

	// Walk declarations.
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			recv := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				if t, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
					recv = t.Name + "."
				}
			}
			kind := "func"
			if recv != "" {
				kind = "method"
			}
			name := recv + d.Name.Name
			_ = addFunc(name, sigOf(d), d, kind)
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						node := CodeNode{
							Kind: "type", Name: ts.Name.Name, Path: rp,
							StartLine: fset.Position(ts.Pos()).Line,
							EndLine:   fset.Position(ts.End()).Line,
						}
						if ts.Doc != nil {
							node.Doc = ts.Doc.Text()
						}
						node.Summary = "type " + ts.Name.Name + " " + typeKind(ts.Type)
						res.Nodes = append(res.Nodes, node)
						id := CodeNodeID("type", rp, ts.Name.Name)
						symbols[ts.Name.Name] = node
						res.Edges = append(res.Edges, CodeEdge{Repo: "", Src: fileID, Dst: id, Kind: "defines", Weight: 1})
					}
				}
			}
		}
	}

	// Call edges: resolve call expressions to known symbols in this file or
	// any indexed node with the same exported name.
	pkgFuncs := res.symbolIndex()
	_ = pkgName
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		default:
			return true
		}
		if dst, ok := symbols[name]; ok {
			srcID := CodeNodeID(kindOfSymbol(dst), rp, dst.Name)
			res.Edges = append(res.Edges, CodeEdge{Repo: "", Src: fileID, Dst: srcID, Kind: "calls", Weight: 1})
			return true
		}
		if dstID, ok := pkgFuncs[name]; ok && dstID != "" {
			res.Edges = append(res.Edges, CodeEdge{Repo: "", Src: fileID, Dst: dstID, Kind: "calls", Weight: 1})
		}
		return true
	})
}

// summarizeFunc builds the embedding source: signature + doc + local names.
func summarizeFunc(sig string, doc *ast.CommentGroup, body *ast.BlockStmt) string {
	var sb strings.Builder
	sb.WriteString(sig)
	if doc != nil {
		sb.WriteString(" | " + doc.Text())
	}
	if body != nil {
		seen := map[string]bool{}
		ast.Inspect(body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok && !seen[sel.Sel.Name] {
					seen[sel.Sel.Name] = true
					sb.WriteString(" " + sel.Sel.Name)
				}
			}
			return true
		})
	}
	return sb.String()
}

// sigOf renders a Go signature for a func declaration.
func sigOf(d *ast.FuncDecl) string {
	var sb strings.Builder
	if d.Recv != nil && len(d.Recv.List) > 0 {
		if t, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
			sb.WriteString("(" + t.Name + ") ")
		}
	}
	sb.WriteString("func " + d.Name.Name)
	if d.Type.Params != nil && d.Type.Params.List != nil {
		sb.WriteString(fmt.Sprintf("(%d params)", len(d.Type.Params.List)))
	} else {
		sb.WriteString("()")
	}
	return sb.String()
}

func typeKind(t ast.Expr) string {
	switch t.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.ArrayType:
		return "slice"
	case *ast.MapType:
		return "map"
	case *ast.FuncType:
		return "func"
	default:
		return "type"
	}
}

func kindOfSymbol(n CodeNode) string { return n.Kind }

// findFileNode looks up a file node id by relative path (with or without
// extension, matching any of the parsed languages).
func (res *ParseResult) findFileNode(path string) string {
	for _, n := range res.Nodes {
		if n.Kind != "file" {
			continue
		}
		if n.Path == path || strings.HasSuffix(n.Path, "/"+path) {
			return CodeNodeID("file", n.Path, n.Name)
		}
		stem := strings.TrimSuffix(n.Path, filepath.Ext(n.Path))
		if stem == path || strings.HasSuffix(stem, "/"+path) {
			return CodeNodeID("file", n.Path, n.Name)
		}
	}
	return ""
}

// symbolIndex maps exported symbol names → node id (across all parsed files).
func (res *ParseResult) symbolIndex() map[string]string {
	out := map[string]string{}
	for _, n := range res.Nodes {
		if n.Kind == "func" || n.Kind == "method" || n.Kind == "type" {
			out[n.Name] = CodeNodeID(n.Kind, n.Path, n.Name)
		}
	}
	return out
}

func pathBase(p string) string {
	p = strings.Trim(p, `"`)
	idx := strings.LastIndex(p, "/")
	if idx >= 0 {
		return p[idx+1:]
	}
	return p
}

var (
	pySymRe   = regexp.MustCompile(`^\s*(async\s+)?(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	jsSymRe   = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?(?:function|class)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	jsConstRe = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=`)
	pyImpRe   = regexp.MustCompile(`^\s*(?:from\s+([\w.]+)\s+import|import\s+([\w.]+))`)
	jsImpRe   = regexp.MustCompile(`^\s*(?:import\s+.+?from\s+['"]([^'"]+)['"]|require\s*\(\s*['"]([^'"]+)['"]\s*\))`)
)

// parseGenericFile is the heuristic parser for Python/JS/TS (v1): symbols by
// regex, import edges by stem matching against parsed file names.
func parseGenericFile(res *ParseResult, root, path string) {
	rp := relPath(root, path)
	ext := strings.ToLower(filepath.Ext(path))
	lang := "python"
	if ext != ".py" {
		lang = "js"
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	fileNode := CodeNode{Kind: "file", Name: filepath.Base(rp), Path: rp, StartLine: 1}
	fileID := CodeNodeID("file", rp, fileNode.Name)
	res.Nodes = append(res.Nodes, fileNode)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	line := 0
	symbols := map[string]CodeNode{}
	for sc.Scan() {
		line++
		text := sc.Text()
		var name string
		switch lang {
		case "python":
			if m := pySymRe.FindStringSubmatch(text); m != nil {
				name = m[3]
			} else if m := pyImpRe.FindStringSubmatch(text); m != nil {
				mod := m[1]
				if mod == "" {
					mod = m[2]
				}
				if mod != "" {
					res.addImport(fileID, pathBase(mod))
				}
			}
		default:
			if m := jsSymRe.FindStringSubmatch(text); m != nil {
				name = m[1]
			} else if m := jsConstRe.FindStringSubmatch(text); m != nil {
				name = m[1]
			} else if m := jsImpRe.FindStringSubmatch(text); m != nil {
				mod := m[1]
				if mod == "" {
					mod = m[2]
				}
				if mod != "" {
					res.addImport(fileID, pathBase(mod))
				}
			}
		}
		if name == "" {
			continue
		}
		kind := "func"
		if strings.Contains(text, "class") {
			kind = "class"
		}
		node := CodeNode{Kind: kind, Name: name, Path: rp, StartLine: line, EndLine: line}
		node.Summary = kind + " " + name + " | " + strings.TrimSpace(text)
		res.Nodes = append(res.Nodes, node)
		symbols[name] = node
		res.Edges = append(res.Edges, CodeEdge{Repo: "", Src: fileID, Dst: CodeNodeID(kind, rp, name), Kind: "defines", Weight: 1})
	}
}

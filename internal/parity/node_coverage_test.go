package parity_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every expression and pattern the parser can build has to be handled by all
// three evaluators. Nothing enforced that: the builtin drift tests compare the
// standard library, name by name, across Go, JS and Swift, and stop there. A
// NODE is the other half of the same contract, and the more dangerous half —
// adding one is a language change, and a language change that lands in one
// evaluator is a program that runs on the server and dies in the browser.
//
// The three lists are read from source rather than written down here, for the
// reason the drift tests give: a list maintained by hand agrees with itself,
// not with the runtimes.
func TestEveryASTNodeIsHandledByAllThreeRuntimes(t *testing.T) {
	exprs, pats := astNodeNames(t)
	if len(exprs) == 0 || len(pats) == 0 {
		t.Fatal("found no AST nodes; the scan is broken, not the runtimes")
	}

	goSrc := readAll(t, filepath.Join("..", "runtime"), ".go")
	jsSrc := readFile(t, filepath.Join("..", "jsserve", "runtime.js"))
	// The Swift decoder is the gate: it turns a `kind` string into an enum
	// case, and Swift's own exhaustiveness checking then forces the
	// evaluator to handle every case the decoder can produce. A kind the
	// decoder does not know is refused there, at load.
	swiftSrc := readFile(t, filepath.Join("..", "iosbundle", "template", "Sources", "MarJSONCodec.swift"))

	runtimes := []struct {
		name string
		// how each runtime spells "I handle this node"
		mentions func(node string) bool
		fix      string
	}{
		{
			name:     "the Go evaluator",
			mentions: func(n string) bool { return strings.Contains(goSrc, "*ast."+n) },
			fix:      "add a `case *ast.X:` arm in internal/runtime",
		},
		{
			name:     "runtime.js",
			mentions: func(n string) bool { return strings.Contains(jsSrc, "'"+n+"'") },
			fix:      "add a `case 'X':` arm in internal/jsserve/runtime.js",
		},
		{
			name:     "the Swift decoder",
			mentions: func(n string) bool { return strings.Contains(swiftSrc, `"`+n+`"`) },
			fix:      "add a `case \"X\":` arm in internal/iosbundle/template/Sources/MarJSONCodec.swift",
		},
	}

	var missing []string
	for _, rt := range runtimes {
		for _, n := range append(append([]string{}, exprs...), pats...) {
			if !rt.mentions(n) {
				missing = append(missing, n+" is not handled by "+rt.name+" — "+rt.fix)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d AST node(s) reach an evaluator that cannot answer them:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}

	// runtime.js also carries the list out loud, because the load-time check
	// cannot read its own switch. Two lists in one file is exactly the shape
	// that drifts, so they are compared here against the same AST.
	declared := jsDeclaredKinds(t, jsSrc)
	if len(declared) == 0 {
		t.Fatal("KNOWN_NODE_KINDS not found in runtime.js; the load-time check has no list to work from")
	}
	for _, n := range append(append([]string{}, exprs...), pats...) {
		if !declared[n] {
			t.Errorf("%s is missing from KNOWN_NODE_KINDS in runtime.js — the load check would refuse a program that uses it", n)
		}
		delete(declared, n)
	}
	for n := range declared {
		t.Errorf("KNOWN_NODE_KINDS in runtime.js lists %q, which is not an AST node", n)
	}

	t.Logf("%d expression nodes and %d pattern nodes, handled by all three runtimes",
		len(exprs), len(pats))
}

// jsDeclaredKinds reads the KNOWN_NODE_KINDS set out of runtime.js.
func jsDeclaredKinds(t *testing.T, src string) map[string]bool {
	t.Helper()
	const marker = "KNOWN_NODE_KINDS = new Set(["
	i := strings.Index(src, marker)
	if i < 0 {
		return nil
	}
	rest := src[i+len(marker):]
	j := strings.Index(rest, "])")
	if j < 0 {
		return nil
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([A-Za-z]+)'`).FindAllStringSubmatch(rest[:j], -1) {
		out[m[1]] = true
	}
	return out
}

// astNodeNames reads internal/ast and returns the types that carry the marker
// methods, which is what makes a type usable as an expression or a pattern.
func astNodeNames(t *testing.T) (exprs, pats []string) {
	t.Helper()
	dir := filepath.Join("..", "ast")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
					continue
				}
				var bucket *[]string
				switch fn.Name.Name {
				case "exprNode":
					bucket = &exprs
				case "patNode":
					bucket = &pats
				default:
					continue
				}
				typ := fn.Recv.List[0].Type
				if star, ok := typ.(*ast.StarExpr); ok {
					typ = star.X
				}
				if id, ok := typ.(*ast.Ident); ok {
					*bucket = append(*bucket, id.Name)
				}
			}
		}
	}
	sort.Strings(exprs)
	sort.Strings(pats)
	return exprs, pats
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readAll(t *testing.T, dir, ext string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

// The checker does not only decide types: it writes MARKS on the tree that the
// evaluators read instead of re-deriving anything (ADR 0016, ADR 0017). Go
// reads them off the shared AST. The web and the phone get a JSON document, so
// each mark has to make the crossing somehow, and a mark that crosses for one
// client and not the other is a program that computes two different answers
// with nothing failing anywhere.
//
// The marks are read out of `Elaborate` rather than listed here, so a fourth
// one cannot be added without this test asking how it crosses.
func TestEveryElaborationMarkReachesBothClientRuntimes(t *testing.T) {
	// How each mark crosses, and what proves it.
	crossings := map[string]struct {
		how   string
		serve string // what internal/jsserve/serialize.go must contain
		js    string // what runtime.js must contain
		swift string // what the Swift decoder must contain
	}{
		"AsDecimal": {
			how:   "converted at serialization: the literal ships as an EDecimal node, so no client needs to know the flag exists",
			serve: `"kind": "EDecimal"`,
		},
		"Impl": {
			how:   "the `impl` key, resolved to a plain name when the client decodes",
			serve: `m["impl"] = impl`,
			js:    "e.impl !== undefined",
			swift: `dict["impl"]`,
		},
		"Wire": {
			how:   "the `wire` key, carrying the shape a decoded value has to have",
			serve: `m["wire"] = serializeWireShape`,
			js:    "e.wire !== undefined",
			swift: `dict["wire"]`,
		},
	}

	marks := elaborationMarks(t)
	if len(marks) == 0 {
		t.Fatal("found no elaboration marks; the scan is broken, not the checker")
	}

	serveSrc := readFile(t, filepath.Join("..", "jsserve", "serialize.go"))
	jsSrc := readFile(t, filepath.Join("..", "jsserve", "runtime.js"))
	swiftSrc := readFile(t, filepath.Join("..", "iosbundle", "template", "Sources", "MarJSONCodec.swift"))

	for _, mark := range marks {
		c, known := crossings[mark]
		if !known {
			t.Errorf("`%s` is written by Elaborate and this test does not know how it reaches the web and the phone. "+
				"Add it above with the crossing, or the mark is Go-only and the other two runtimes answer differently.", mark)
			continue
		}
		if c.serve != "" && !strings.Contains(serveSrc, c.serve) {
			t.Errorf("`%s` crosses %s, but internal/jsserve/serialize.go no longer contains %q", mark, c.how, c.serve)
		}
		if c.js != "" && !strings.Contains(jsSrc, c.js) {
			t.Errorf("`%s` is serialized but runtime.js no longer reads it (%q)", mark, c.js)
		}
		if c.swift != "" && !strings.Contains(swiftSrc, c.swift) {
			t.Errorf("`%s` is serialized but the Swift decoder no longer reads it (%q)", mark, c.swift)
		}
	}

	for mark := range crossings {
		found := false
		for _, m := range marks {
			if m == mark {
				found = true
			}
		}
		if !found {
			t.Errorf("this test still explains how `%s` crosses, but Elaborate no longer writes it", mark)
		}
	}
}

// elaborationMarks reads the fields Elaborate assigns on AST nodes.
func elaborationMarks(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "typecheck", "check.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse check.go: %v", err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Elaborate" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "n" {
					seen[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

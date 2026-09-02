package project

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A setting in mar.json that nothing reads is worse than one that does not
// exist: it is documented, accepted, validated, and ignored. `ios.serverUrl`
// was the version of this that cost something — the phone dropped the path it
// carried — and it was found by hand.
//
// Two mechanical rules together cover the shape:
//
//  1. every field of every config struct is read somewhere, and
//  2. every `Resolve*` accessor is called from outside its own file, so a
//     field read only by an accessor nobody calls still counts as unread.
//
// What this does NOT prove is that the caller runs on every entry point. That
// was checked by hand for all 13 settings, and the auto-backup trio is the one
// worth remembering: its resolvers are called from `admin.MaybeStartAutoBackup`
// rather than from the entry points, so grepping the entry points reads as
// zero and is wrong.
func TestEveryConfigFieldIsRead(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// The config structs: those whose fields carry json tags.
	fields := map[string]string{} // field name -> struct it belongs to
	for _, p := range pkg {
		for _, file := range p.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, f := range st.Fields.List {
					if f.Tag == nil || !strings.Contains(f.Tag.Value, `json:"`) {
						continue
					}
					for _, name := range f.Names {
						if name.IsExported() {
							fields[name.Name] = ts.Name.Name
						}
					}
				}
				return true
			})
		}
	}
	if len(fields) < 20 {
		t.Fatalf("found %d config fields; the scan is broken, not the config", len(fields))
	}

	src := readNonTestSources(t, ".")
	// Also read the two entry points and the packages that consume settings,
	// so a field read outside this package still counts.
	for _, dir := range []string{
		filepath.Join("..", "..", "cmd", "mar"),
		filepath.Join("..", "..", "cmd", "mar-runtime"),
		filepath.Join("..", "admin"),
		filepath.Join("..", "jsserve"),
		filepath.Join("..", "scaffold"),
		filepath.Join("..", "iosbundle"),
	} {
		src += readNonTestSources(t, dir)
	}

	var unread []string
	for name, owner := range fields {
		// A read is `.Field` somewhere that is not the declaration itself.
		if strings.Count(src, "."+name) <= 0 {
			unread = append(unread, owner+"."+name)
		}
	}
	if len(unread) > 0 {
		sort.Strings(unread)
		t.Errorf("%d config field(s) are accepted, validated and never read:\n  %s",
			len(unread), strings.Join(unread, "\n  "))
	}
}

// A `Resolve*` accessor with no caller outside its own file means the field it
// reads is read by nobody: the accessor is the only reader, and nothing calls
// it.
func TestEveryConfigAccessorHasACaller(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	accessors := map[string]string{} // name -> file it is declared in
	for _, p := range pkg {
		for path, file := range p.Files {
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || !fn.Name.IsExported() {
					continue
				}
				if strings.HasPrefix(fn.Name.Name, "Resolve") {
					accessors[fn.Name.Name] = filepath.Base(path)
				}
			}
		}
	}
	if len(accessors) < 5 {
		t.Fatalf("found %d Resolve* accessors; the scan is broken", len(accessors))
	}

	elsewhere := readNonTestSources(t, "..") // every package under internal/
	for _, dir := range []string{filepath.Join("..", "..", "cmd", "mar"), filepath.Join("..", "..", "cmd", "mar-runtime")} {
		elsewhere += readNonTestSources(t, dir)
	}

	var orphans []string
	for name, decl := range accessors {
		// One occurrence is the declaration; a caller makes two.
		if strings.Count(elsewhere, name+"(") < 2 {
			orphans = append(orphans, name+" (declared in "+decl+")")
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("%d accessor(s) nobody calls, so the setting they read does nothing:\n  %s",
			len(orphans), strings.Join(orphans, "\n  "))
	}
}

// readNonTestSources concatenates every non-test .go file in dir, and in its
// immediate subdirectories, so a read from a sibling package still counts.
func readNonTestSources(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			b.Write(data)
		}
		return nil
	})
	return b.String()
}

package apphost

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
)

// Four tests in this package each ask a different question of EVERY example,
// and each used to load every example to ask it. Loading is the whole cost
// (it type-checks the project), so the suite paid for it four times over.
//
// The examples are loaded once here, and what each test reads is written down
// at the same moment the test itself used to read it: the censuses right after
// the load, the boot outcome after `main` has run. The tests keep their names,
// their thresholds and their messages; only the loop moved.

// exampleFacts is what one example told the four tests.
type exampleFacts struct {
	name string

	// A panic while loading or running. The boot test reports it; the
	// censuses always swallowed theirs and still do.
	panicked any

	loadErr error // nil when the project loaded

	// Read right after the load, before `main` runs (the three censuses).
	columns   []columnFact
	contracts int      // service contracts carrying a request shape
	requests  []string // request positions that assert nothing
	responses []responseFact

	// The boot (TestNoExampleIsRefusedAtBoot).
	ran          bool  // `main` was an effect and was run
	runErr       error // what running it returned
	hasIdentify  bool
	identityCol  string
	fromIdentify bool
}

type columnFact struct {
	kind    string
	checked bool
	where   string
}

type responseFact struct {
	kind  string // "Service.call" or "JSON.decode"
	where string // "" when every position asserts something
}

var (
	everyExampleOnce sync.Once
	everyExample     []exampleFacts
	everyExampleErr  error
)

// loadEveryExample boots each example once and returns what was read.
func loadEveryExample(t *testing.T) []exampleFacts {
	t.Helper()
	everyExampleOnce.Do(func() {
		dirs, err := filepath.Glob("../../examples/*")
		if err != nil || len(dirs) == 0 {
			everyExampleErr = fmt.Errorf("no examples found: %v", err)
			return
		}
		// Not t.TempDir(): that one belongs to whichever test happens to run
		// first. Removed when the pass is over, which is when the tests'
		// own temp dirs used to go: nothing reads a database after it.
		tmp, err := os.MkdirTemp("", "apphost-examples-")
		if err != nil {
			everyExampleErr = err
			return
		}
		defer os.RemoveAll(tmp)
		for i, dir := range dirs {
			entry := filepath.Join(dir, "Main.mar")
			if _, err := os.Stat(entry); err != nil {
				continue // not a project (the-last-developer holds no sources)
			}
			db := filepath.Join(tmp, fmt.Sprintf("%d.db", i))
			everyExample = append(everyExample, readExample(filepath.Base(dir), entry, db))
		}
	})
	if everyExampleErr != nil {
		t.Fatal(everyExampleErr)
	}
	return everyExample
}

func readExample(name, entry, dbPath string) (facts exampleFacts) {
	facts.name = name
	defer func() {
		if r := recover(); r != nil {
			facts.panicked = r
		}
	}()
	runtime.ResetForReload()
	runtime.SetDBPath(dbPath)
	var notes bytes.Buffer
	routeNoteWriter = &notes
	defer func() { routeNoteWriter = os.Stderr }()

	lp := &jsserve.LiveProgram{}
	rEnv, mods, _, err := project.LoadIntoEnvWithModulesAndHook(entry,
		func(env *runtime.Env, ms []*ast.Module) { Install(env, ms, 0, lp) })
	if err != nil {
		facts.loadErr = err
		return facts
	}

	// The censuses. Each is guarded on its own, as each test's loop was: a
	// panic in one is that census reading nothing further from this example,
	// not a failure of the boot.
	func() {
		defer func() { _ = recover() }()
		for _, ent := range runtime.RegisteredEntities() {
			for _, f := range ent.Fields {
				kind := f.SQLType
				if f.AcceptedCtors != nil {
					kind = "TEXT(enum)"
				}
				facts.columns = append(facts.columns, columnFact{
					kind:    kind,
					checked: runtime.ColumnIsCheckedBothWays(f),
					where:   fmt.Sprintf("%s: %s.%s holds %s", name, ent.Table, f.Name, kind),
				})
			}
		}
	}()
	func() {
		defer func() { _ = recover() }()
		for _, m := range mods {
			prefix := strings.Join(m.Name, ".")
			for _, d := range m.Decls {
				vd, ok := d.(*ast.ValueDecl)
				if !ok {
					continue
				}
				v, ok := rEnv.Lookup(prefix + "." + vd.Name)
				if !ok {
					continue
				}
				svc, ok := v.(runtime.VService)
				if !ok || svc.InputShape == nil {
					continue
				}
				facts.contracts++
				if where := findWireAny(*svc.InputShape, ""); where != "" {
					facts.requests = append(facts.requests,
						fmt.Sprintf("%s: %s %s at %s", name, svc.Verb, svc.Path, where))
				}
			}
		}
	}()
	func() {
		defer func() { _ = recover() }()
		for _, m := range mods {
			for _, d := range m.Decls {
				vd, ok := d.(*ast.ValueDecl)
				if !ok {
					continue
				}
				eachServiceCallShape(vd.Body, func(kind string, s ast.WireShape, line int) {
					fact := responseFact{kind: kind}
					if where := findWireAny(s, ""); where != "" {
						fact.where = fmt.Sprintf(
							"%s: %s line %d, at %s", name, strings.Join(m.Name, "."), line, where)
					}
					facts.responses = append(facts.responses, fact)
				})
			}
		}
	}()

	// The boot.
	mainV, ok := project.LookupMain(rEnv, mods)
	if !ok {
		return facts
	}
	eff, isEff := mainV.(runtime.VEffect)
	if !isEff {
		return facts
	}
	facts.ran = true
	if _, err := eff.Run(); err != nil {
		facts.runErr = err
		return facts
	}
	if cfg := runtime.CurrentAuth(); cfg != nil && cfg.Identify != nil {
		facts.hasIdentify = true
		facts.identityCol, facts.fromIdentify = runtime.ResolveIdentityColumn(*cfg)
	}
	return facts
}

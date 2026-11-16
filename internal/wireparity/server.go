// Package wireparity is the SERVER half of the wire round trip
// (parityfixture.WireCases), shared by the browser test in internal/jsserve
// and the phone test in internal/iosbundle so the two compare against one
// server and one rendering of a value.
//
// Nothing here re-implements the server. The program is loaded by the loader
// a deployed app uses (so every contract carries the request shape the
// checker stamped), a request goes through runtime.ExposedServiceToRoute (the
// wrapper the HTTP layer mounts: assemble, 422 on CheckWire, CoerceWire), and
// a response is the bytes that wrapper writes. Only the user's handler is
// replaced, by one that records the request it was handed, because the
// request it was handed is the whole question.
package wireparity

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parityfixture"
	"mar/internal/project"
	"mar/internal/runtime"
)

// Server is the fixture program, loaded the way a deployed server loads it.
type Server struct {
	env *runtime.Env
}

// Load writes src as a one-module project and loads it.
func Load(t testing.TB, src string) *Server {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	env, _, err := project.LoadIntoEnvForRuntime(entry, nil)
	if err != nil {
		t.Fatalf("server load: %v", err)
	}
	return &Server{env: env}
}

// Value is a top-level value as the server computed it.
func (s *Server) Value(t testing.TB, name string) runtime.Value {
	t.Helper()
	v, ok := s.env.Lookup("Main." + name)
	if !ok {
		t.Fatalf("Main.%s is not bound on the server", name)
	}
	return v
}

func (s *Server) service(t testing.TB, name string) runtime.VService {
	t.Helper()
	svc, ok := s.Value(t, name).(runtime.VService)
	if !ok {
		t.Fatalf("Main.%s is not a service", name)
	}
	if svc.InputShape == nil {
		// Without the stamp every case below would pass for the wrong
		// reason: an unstamped service checks nothing.
		t.Fatalf("Main.%s reached the server without its request shape", name)
	}
	return svc
}

// Outcome is what the server did with one request.
type Outcome struct {
	Status int
	Body   string
	// Input is what the handler was handed: nil when it was never called.
	Input runtime.Value
}

// Request sends a client's request (method, the URL it built, the body it
// built) to the service and reports what the handler received.
//
// The service is picked by name, so the method and the path the client chose
// are compared with the ones the service declares, as the HTTP router would
// match them: a wrong verb or path is a 404 here too.
func (s *Server) Request(t testing.TB, service, method, rawURL string, body *string) Outcome {
	t.Helper()
	svc := s.service(t, service)
	var got runtime.Value
	svc.Handler = runtime.VFn{Arity: 1, Native: func(args []runtime.Value) (runtime.Value, error) {
		got = args[0]
		return runtime.VEffect{Tag: "wireparity", Run: func() (runtime.Value, error) {
			return runtime.VUnit{}, nil
		}}, nil
	}}
	// The HTTP layer hands the wrapper req.URL.Path and req.URL.RawQuery
	// (internal/jsserve/unified.go buildRequestValue); parse the client's URL
	// the same way so a percent-encoding mistake is the server's to find.
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("the client built a URL that does not parse: %q: %v", rawURL, err)
	}
	if method != svc.Verb || u.Path != svc.Path {
		return Outcome{Status: http.StatusNotFound, Body: fmt.Sprintf(
			"no route: the client sent %s %s, the service is declared %s %s", method, u.Path, svc.Verb, svc.Path)}
	}
	b := ""
	if body != nil {
		b = *body
	}
	req := runtime.VRecord{
		Fields: map[string]runtime.Value{
			"path":  runtime.VString{V: u.Path},
			"query": runtime.VString{V: u.RawQuery},
			"body":  runtime.VString{V: b},
		},
		Order: []string{"path", "query", "body"},
	}
	return s.run(t, svc, req, &got)
}

// Respond runs the response contract `service` with a handler that answers
// the server's own value `value`, and returns the bytes the wrapper writes.
func (s *Server) Respond(t testing.TB, service, value string) (int, string) {
	t.Helper()
	svc := s.service(t, service)
	v := s.Value(t, value)
	svc.Handler = runtime.VFn{Arity: 1, Native: func([]runtime.Value) (runtime.Value, error) {
		return runtime.VEffect{Tag: "wireparity", Run: func() (runtime.Value, error) { return v, nil }}, nil
	}}
	req := runtime.VRecord{
		Fields: map[string]runtime.Value{
			"path":  runtime.VString{V: svc.Path},
			"query": runtime.VString{V: ""},
			"body":  runtime.VString{V: ""},
		},
		Order: []string{"path", "query", "body"},
	}
	var ignored runtime.Value
	o := s.run(t, svc, req, &ignored)
	return o.Status, o.Body
}

func (s *Server) run(t testing.TB, svc runtime.VService, req runtime.VRecord, got *runtime.Value) Outcome {
	t.Helper()
	route, ok := runtime.ExposedServiceToRoute(runtime.VExposedService{Service: svc}).(runtime.VRecord)
	if !ok {
		t.Fatal("ExposedServiceToRoute did not answer a route record")
	}
	h, ok := route.Fields["handler"].(runtime.VFn)
	if !ok || h.Native == nil {
		t.Fatal("the route has no native handler")
	}
	effV, err := h.Native([]runtime.Value{req})
	if err != nil {
		t.Fatalf("route handler: %v", err)
	}
	eff, ok := effV.(runtime.VEffect)
	if !ok {
		t.Fatalf("route handler answered %T, not an effect", effV)
	}
	resp, err := eff.Run()
	if err != nil {
		t.Fatalf("route effect: %v", err)
	}
	rec, ok := resp.(runtime.VRecord)
	if !ok {
		t.Fatalf("route answered %T, not a response record", resp)
	}
	status, _ := rec.Fields["status"].(runtime.VInt)
	body, _ := rec.Fields["body"].(runtime.VString)
	return Outcome{Status: int(status.V), Body: body.V, Input: *got}
}

// Dump renders a value with its KIND in every position, so a Duration that
// came back an Int, a Decimal that lost its scale, or a Result that came back
// a record are all different strings. The browser and phone tests implement
// the same rendering over their own value types; the three must agree
// character for character, which is what makes them comparable at all.
//
//	I<n>  D<decimal text, scale kept>  S[<code points>]  B1/B0  U
//	Ch<code point>  Tm<unix ms>  A<deci-degrees>  Du<ms>
//	L[..]  Tu[..]  R{k=..,..} (keys sorted)  K<Tag>[..]
//	Di[(k>v),..]  Se[..]  (both in stored order)
func Dump(v runtime.Value) string {
	if v == nil {
		return "<nothing>"
	}
	switch x := v.(type) {
	case runtime.VInt:
		return "I" + strconv.FormatInt(x.V, 10)
	case runtime.VDecimal:
		enc, err := runtime.EncodeValueJSON(x)
		if err != nil {
			return "D?"
		}
		var m map[string]string
		if json.Unmarshal([]byte(enc), &m) != nil {
			return "D?"
		}
		return "D" + m["__dec"]
	case runtime.VString:
		return "S" + codePoints(x.V)
	case runtime.VBool:
		if x.V {
			return "B1"
		}
		return "B0"
	case runtime.VUnit:
		return "U"
	case runtime.VChar:
		return "Ch" + strconv.Itoa(int(x.V))
	case runtime.VTime:
		return "Tm" + strconv.FormatInt(x.Millis, 10)
	case runtime.VAngle:
		return "A" + strconv.FormatInt(x.Deci, 10)
	case runtime.VDuration:
		return "Du" + strconv.FormatInt(int64(math.Round(x.Seconds*1000)), 10)
	case runtime.VList:
		return "L" + dumpAll(x.Elements)
	case runtime.VTuple:
		return "Tu" + dumpAll(x.Members)
	case runtime.VRecord:
		keys := make([]string, 0, len(x.Fields))
		for k := range x.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + Dump(x.Fields[k])
		}
		return "R{" + strings.Join(parts, ",") + "}"
	case runtime.VCtor:
		return "K" + x.Tag + dumpAll(x.Args)
	case runtime.VDict:
		parts := make([]string, len(x.Pairs))
		for i, p := range x.Pairs {
			parts[i] = "(" + Dump(p.Key) + ">" + Dump(p.Value) + ")"
		}
		return "Di[" + strings.Join(parts, ",") + "]"
	case runtime.VSet:
		return "Se" + dumpAll(x.Items)
	}
	return fmt.Sprintf("?%T", v)
}

func dumpAll(vs []runtime.Value) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = Dump(v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func codePoints(s string) string {
	parts := make([]string, 0, len(s))
	for _, r := range s {
		parts = append(parts, strconv.Itoa(int(r)))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// CallShape finds the wire shape the checker stamped on the Service.call
// inside the declaration `decl` of a serialized program: the shape the
// browser reads off the node, and the one the phone builds its caller from.
// Taken from the serialized bytes, not recomputed, so a shape lost in
// serialization is a failure here too.
func CallShape(t testing.TB, programJSON []byte, decl string) json.RawMessage {
	t.Helper()
	var prog struct {
		Modules []struct {
			Decls []map[string]json.RawMessage `json:"decls"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(programJSON, &prog); err != nil {
		t.Fatalf("program: %v", err)
	}
	for _, m := range prog.Modules {
		for _, d := range m.Decls {
			var name string
			_ = json.Unmarshal(d["name"], &name)
			if name != decl {
				continue
			}
			var body any
			_ = json.Unmarshal(d["body"], &body)
			if w := findCallWire(body); w != nil {
				out, _ := json.Marshal(w)
				return out
			}
			t.Fatalf("%s has no Service.call carrying a shape", decl)
		}
	}
	t.Fatalf("%s is not declared", decl)
	return nil
}

func findCallWire(n any) any {
	switch x := n.(type) {
	case map[string]any:
		if x["name"] == "call" {
			if mod, ok := x["module"].([]any); ok && len(mod) == 1 && mod[0] == "Service" && x["wire"] != nil {
				return x["wire"]
			}
		}
		for _, v := range x {
			if w := findCallWire(v); w != nil {
				return w
			}
		}
	case []any:
		for _, v := range x {
			if w := findCallWire(v); w != nil {
				return w
			}
		}
	}
	return nil
}

// Matrix collects one verdict per (case, direction) so a run reports the
// whole table rather than stopping at the first broken kind: the point is to
// see WHICH kinds fail in WHICH directions, and the shape of that table is
// usually the diagnosis.
//
// A cell parityfixture.WireOpen names is a known failure waiting for a
// decision: it shows as "open" and does not fail the run. When it PASSES, the
// run fails, so the list cannot outlive the bug it describes.
type Matrix struct {
	cols    []string
	rows    []string
	cells   map[[2]string]string
	details map[[2]string]string
}

func NewMatrix(cols ...string) *Matrix {
	return &Matrix{cols: cols, cells: map[[2]string]string{}, details: map[[2]string]string{}}
}

// Check records a comparison: `got` must equal `want`.
func (m *Matrix) Check(row, col, want, got string) {
	if got == want {
		m.set(row, col, "ok")
		return
	}
	m.set(row, col, "FAIL")
	m.details[[2]string{row, col}] = fmt.Sprintf("want %s\n      got  %s", want, got)
}

// Fail records a failure that is not a value comparison (a 422, a throw).
func (m *Matrix) Fail(row, col, why string) {
	m.set(row, col, "FAIL")
	m.details[[2]string{row, col}] = why
}

func (m *Matrix) set(row, col, v string) {
	if _, seen := m.cells[[2]string{row, ""}]; !seen {
		m.cells[[2]string{row, ""}] = "x"
		m.rows = append(m.rows, row)
	}
	m.cells[[2]string{row, col}] = v
}

// Report logs the table and fails the test once per failing cell that is not
// open, and once per open cell that passes.
func (m *Matrix) Report(t testing.TB) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%-20s", "")
	for _, c := range m.cols {
		fmt.Fprintf(&b, " %-10s", c)
	}
	b.WriteString("\n")
	failing, open := 0, 0
	var errs []string
	for _, r := range m.rows {
		fmt.Fprintf(&b, "%-20s", r)
		for _, c := range m.cols {
			v := m.cells[[2]string{r, c}]
			reason := parityfixture.WireOpen(r, c)
			switch {
			case v == "":
				v = "-"
			case v == "FAIL" && reason != "":
				v = "open"
				open++
			case v == "FAIL":
				failing++
				errs = append(errs, fmt.Sprintf("%s / %s\n      %s", r, c, m.details[[2]string{r, c}]))
			case reason != "":
				errs = append(errs, fmt.Sprintf("%s / %s passes now: take it out of parityfixture.WireOpen, whose reason was\n      %s", r, c, reason))
			}
			fmt.Fprintf(&b, " %-10s", v)
		}
		b.WriteString("\n")
	}
	t.Logf("wire round trip, %d failing cells, %d open (parityfixture.WireOpen):\n%s", failing, open, b.String())
	for _, e := range errs {
		t.Errorf("%s", e)
	}
}

// Compare is Check with the bytes that crossed, which a failure has to show:
// the dump says WHAT arrived, the bytes say why.
func (m *Matrix) Compare(row, col, want, got, crossed string) {
	if got == want {
		m.set(row, col, "ok")
		return
	}
	m.set(row, col, "FAIL")
	m.details[[2]string{row, col}] = fmt.Sprintf("want %s\n      got  %s\n      %s", want, got, crossed)
}

// FieldV is the `v` field of a request record, or nil.
func FieldV(v runtime.Value) runtime.Value {
	if r, ok := v.(runtime.VRecord); ok {
		return r.Fields["v"]
	}
	return nil
}

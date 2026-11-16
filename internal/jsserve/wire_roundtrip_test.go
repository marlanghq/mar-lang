package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
	"mar/internal/wireparity"
)

// The browser half of the wire round trip (parityfixture.WireCases).
//
// Every value in the table goes browser -> server through the bytes the
// browser's REAL Service.call writes (fetch is stubbed only to capture them),
// into the server's REAL request path (wireparity.Server: the loader's
// stamped shape, CheckWire, CoerceWire), and must arrive as the value it left
// as. Then server -> browser: the bytes the server's response wrapper writes,
// through the browser's real Service.call response path with the shape the
// call site carries. And inside the browser, JSON.encode must write the
// server's bytes and JSON.decode must read its own.
//
// A value is compared by its typed dump (wireparity.Dump, and `dump` in the
// driver below), never by an encoder or by `==`.
func TestEveryWireValueSurvivesTheBrowserAndTheServer(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src := parityfixture.WireRoundTripSource()
	srv := wireparity.Load(t, src)

	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}

	type respPlan struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	plan := struct {
		Values    []string   `json:"values"`
		Requests  []string   `json:"requests"`
		Responses []respPlan `json:"responses"`
	}{}
	serverBody := map[string]string{}
	for _, c := range parityfixture.WireCases {
		plan.Values = append(plan.Values, "v"+c.Name, "enc"+c.Name)
		if c.NoContract {
			continue
		}
		plan.Values = append(plan.Values, "dec"+c.Name)
		for _, leg := range parityfixture.WireLegs {
			plan.Requests = append(plan.Requests, "call"+upper(leg.Prefix)+c.Name)
		}
		status, body := srv.Respond(t, "resp"+c.Name, "v"+c.Name)
		if status != 200 {
			t.Fatalf("the server could not answer resp%s: %d %s", c.Name, status, body)
		}
		serverBody[c.Name] = body
		plan.Responses = append(plan.Responses, respPlan{Name: "callResp" + c.Name, Body: body})
	}

	out := runWireDriver(t, node, pj, plan)

	m := wireparity.NewMatrix("value", "encode", "dec/srv", "dec/web",
		"post", "get", "postWhole", "getWhole", "response")
	for _, c := range parityfixture.WireCases {
		want := wireparity.Dump(srv.Value(t, "v"+c.Name))
		// Control: the browser computes the same value before anything is
		// sent, or every failure below would be blamed on the wire.
		m.Check(c.Name, "value", want, out.Values["v"+c.Name])
		m.Check(c.Name, "encode", wireparity.Dump(srv.Value(t, "enc"+c.Name)), out.Values["enc"+c.Name])
		if c.NoContract {
			continue
		}
		m.Check(c.Name, "dec/srv", "KOk["+want+"]", wireparity.Dump(srv.Value(t, "dec"+c.Name)))
		m.Check(c.Name, "dec/web", "KOk["+want+"]", out.Values["dec"+c.Name])
		for _, leg := range parityfixture.WireLegs {
			call := "call" + upper(leg.Prefix) + c.Name
			req, ok := out.Requests[call]
			if !ok || req.URL == "" {
				m.Fail(c.Name, leg.Prefix, "the browser sent nothing: "+req.Threw)
				continue
			}
			o := srv.Request(t, leg.Prefix+c.Name, req.Method, req.URL, req.Body)
			sent := req.Method + " " + req.URL
			if req.Body != nil {
				sent += " " + *req.Body
			}
			if o.Status != 200 || o.Input == nil {
				m.Fail(c.Name, leg.Prefix, "browser sent  "+sent+"\n      server said  "+strconv.Itoa(o.Status)+" "+o.Body)
				continue
			}
			got := o.Input
			if !leg.Whole {
				got = wireparity.FieldV(o.Input)
			}
			m.Compare(c.Name, leg.Prefix, want, wireparity.Dump(got), "browser sent "+sent)
		}
		m.Compare(c.Name, "response", "KOk["+want+"]", out.Responses["callResp"+c.Name],
			"server sent "+serverBody[c.Name])
	}
	m.Report(t)
}

type wireDriverOut struct {
	Values   map[string]string `json:"values"`
	Requests map[string]struct {
		URL    string  `json:"url"`
		Method string  `json:"method"`
		Body   *string `json:"body"`
		Threw  string  `json:"threw"`
	} `json:"requests"`
	Responses map[string]string `json:"responses"`
}

// The driver: stubbed fetch that RECORDS what Service.call sends and answers
// with the server's bytes, and `dump`, which must render exactly what
// wireparity.Dump renders for the same value.
const wireDriverJS = `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
let captured = null;
let respBody = 'null';
globalThis.fetch = (url, init) => {
  captured = { url: String(url), method: (init && init.method) || 'GET',
               body: (init && init.body !== undefined) ? init.body : null };
  return Promise.resolve({ ok: true, status: 200, headers: { get: () => null },
                           text: () => Promise.resolve(respBody) });
};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const plan = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));

function cps(s) { return '[' + Array.from(s).map(c => c.codePointAt(0)).join(',') + ']'; }
function decText(v) {
  const neg = v.coef < 0n;
  let digits = (neg ? -v.coef : v.coef).toString();
  let out;
  if (v.scale === 0) out = digits;
  else {
    if (digits.length <= v.scale) digits = '0'.repeat(v.scale - digits.length + 1) + digits;
    const cut = digits.length - v.scale;
    out = digits.slice(0, cut) + '.' + digits.slice(cut);
  }
  return neg ? '-' + out : out;
}
function all(xs) { return '[' + xs.map(dump).join(',') + ']'; }
function dump(v) {
  if (!v || typeof v !== 'object') return '?' + typeof v;
  switch (v.k) {
    case 'I':  return 'I' + String(v.n);
    case 'De': return 'D' + decText(v);
    case 'S':  return 'S' + cps(v.s);
    case 'B':  return v.b ? 'B1' : 'B0';
    case 'U':  return 'U';
    case 'Ch': return 'Ch' + v.c;
    case 'TM': return 'Tm' + v.millis;
    case 'A':  return 'A' + v.deci;
    case 'D':  return 'Du' + Math.round(v.seconds * 1000);
    case 'L':  return 'L' + all(v.xs);
    case 'T':  return 'Tu' + all(v.xs);
    case 'R':  return 'R{' + Object.keys(v.fields).sort().map(k => k + '=' + dump(v.fields[k])).join(',') + '}';
    case 'C':  return 'K' + v.tag + all(v.args || []);
    case 'M':  return 'Di[' + v.pairs.map(p => '(' + dump(p.key) + '>' + dump(p.value) + ')').join(',') + ']';
    case 'Se': return 'Se' + all(v.items);
  }
  return '?' + v.k;
}

(async () => {
  const out = { values: {}, requests: {}, responses: {} };
  for (const n of plan.values) {
    try { out.values[n] = dump(globalThis.__marEvalRaw(program, 'Main.' + n)); }
    catch (e) { out.values[n] = 'THREW ' + ((e && e.message) || e); }
  }
  for (const n of plan.requests) {
    captured = null; respBody = 'null';
    try { await globalThis.__marTestRunCmd(program, 'Main.' + n, true); out.requests[n] = captured || { threw: 'no fetch' }; }
    catch (e) { out.requests[n] = Object.assign({}, captured || {}, { threw: String((e && e.message) || e) }); }
  }
  for (const r of plan.responses) {
    respBody = r.body;
    try { out.responses[r.name] = dump(await globalThis.__marTestRunCmd(program, 'Main.' + r.name, true)); }
    catch (e) { out.responses[r.name] = 'THREW ' + ((e && e.message) || e); }
  }
  process.stdout.write(JSON.stringify(out));
})();
`

func runWireDriver(t *testing.T, node string, pj []byte, plan any) wireDriverOut {
	t.Helper()
	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	planJSON, _ := json.Marshal(plan)
	for name, b := range map[string][]byte{
		"runtime.js": rt, "program.json": pj, "plan.json": planJSON, "driver.js": []byte(wireDriverJS),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"),
		filepath.Join(dir, "program.json"), filepath.Join(dir, "plan.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var out wireDriverOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("driver output: %v\n%s", err, raw)
	}
	return out
}

func upper(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

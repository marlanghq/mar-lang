package iosbundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
	"mar/internal/wireparity"
)

// The phone half of the wire round trip (parityfixture.WireCases); the
// browser half is internal/jsserve/wire_roundtrip_test.go and both compare
// against the same server (wireparity).
//
// phone -> server: the URL and body buildServiceRequest writes (the function
// both of the phone's Service.call builtins call; what follows it hands the
// bytes to URLSession unchanged) go through the server's real request path.
// server -> phone: the server's response bytes go through makeServiceResult
// with the shape the call site's Service.call carries in the program the
// phone is shipped. Inside the phone: JSON.encode must write the server's
// bytes, and JSON.decode must read its own.
func TestEveryWireValueSurvivesThePhoneAndTheServer(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("assembles and compiles the Swift core; not for -short")
	}
	dir := t.TempDir()
	if err := assembleSwiftCore(t, dir); err != nil {
		t.Fatalf("assembling the core: %v", err)
	}
	bin := filepath.Join(dir, "driver")
	build := exec.Command(swiftc, "-o", bin)
	sources, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	build.Args = append(build.Args, sources...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
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
	pj, err := json.Marshal(map[string]any{"modules": []any{jsserve.SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}

	type reqPlan struct {
		Name    string `json:"name"`
		Service string `json:"service"`
		Value   string `json:"value"`
		Whole   bool   `json:"whole"`
	}
	type respPlan struct {
		Name  string          `json:"name"`
		Body  string          `json:"body"`
		Shape json.RawMessage `json:"shape"`
	}
	plan := struct {
		Values    []string   `json:"values"`
		Requests  []reqPlan  `json:"requests"`
		Responses []respPlan `json:"responses"`
	}{}
	serverBody := map[string]string{}
	for _, c := range parityfixture.WireCases {
		plan.Values = append(plan.Values, "v"+c.Name, "enc"+c.Name)
		plan.Values = append(plan.Values, "dec"+c.Name)
		for _, leg := range parityfixture.WireLegs {
			plan.Requests = append(plan.Requests, reqPlan{
				Name: leg.Prefix + c.Name, Service: leg.Prefix + c.Name, Value: "v" + c.Name, Whole: leg.Whole,
			})
		}
		status, body := srv.Respond(t, "resp"+c.Name, "v"+c.Name)
		if status != 200 {
			t.Fatalf("the server could not answer resp%s: %d %s", c.Name, status, body)
		}
		serverBody[c.Name] = body
		plan.Responses = append(plan.Responses, respPlan{
			Name: "callResp" + c.Name, Body: body, Shape: wireparity.CallShape(t, pj, "callResp"+c.Name),
		})
	}
	planJSON, _ := json.Marshal(plan)
	progPath, planPath := filepath.Join(dir, "program.json"), filepath.Join(dir, "plan.json")
	if err := os.WriteFile(progPath, pj, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, planJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--wire-batch", progPath, planPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, stderr.String())
	}
	var out struct {
		Values   map[string]string `json:"values"`
		Requests map[string]struct {
			URL    string  `json:"url"`
			Method string  `json:"method"`
			Body   *string `json:"body"`
			Threw  string  `json:"threw"`
		} `json:"requests"`
		Responses map[string]string `json:"responses"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("driver output: %v\n%s", err, raw)
	}

	m := wireparity.NewMatrix("value", "encode", "dec/srv", "dec/ios",
		"post", "get", "delete", "postWhole", "response")
	for _, c := range parityfixture.WireCases {
		want := wireparity.Dump(srv.Value(t, "v"+c.Name))
		m.Check(c.Name, "value", want, out.Values["v"+c.Name])
		m.Check(c.Name, "encode", wireparity.Dump(srv.Value(t, "enc"+c.Name)), out.Values["enc"+c.Name])
		m.Check(c.Name, "dec/srv", "KOk["+want+"]", wireparity.Dump(srv.Value(t, "dec"+c.Name)))
		m.Check(c.Name, "dec/ios", "KOk["+want+"]", out.Values["dec"+c.Name])
		for _, leg := range parityfixture.WireLegs {
			req := out.Requests[leg.Prefix+c.Name]
			if req.URL == "" {
				m.Fail(c.Name, leg.Prefix, "the phone sent nothing: "+req.Threw)
				continue
			}
			o := srv.Request(t, leg.Prefix+c.Name, req.Method, req.URL, req.Body)
			sent := req.Method + " " + req.URL
			if req.Body != nil {
				sent += " " + *req.Body
			}
			if o.Status != 200 || o.Input == nil {
				m.Fail(c.Name, leg.Prefix, "phone sent  "+sent+"\n      server said  "+strconv.Itoa(o.Status)+" "+o.Body)
				continue
			}
			got := o.Input
			if !leg.Whole {
				got = wireparity.FieldV(o.Input)
			}
			m.Compare(c.Name, leg.Prefix, want, wireparity.Dump(got), "phone sent "+sent)
		}
		m.Compare(c.Name, "response", "KOk["+want+"]", out.Responses["callResp"+c.Name],
			"server sent "+serverBody[c.Name])
	}
	m.Report(t)
}

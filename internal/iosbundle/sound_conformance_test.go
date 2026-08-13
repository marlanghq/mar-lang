package iosbundle

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"mar/internal/conformance"
	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Sound, compared across the two runtimes that actually make sound.
//
// Until this file existed, every sound test drove node and read the JS voice
// records; the Swift synth was covered by drift tests, which see that a builtin
// is DEFINED and not that it answers the same thing. That is precisely the hole
// the master level fell through: 0.35 in one runtime and 0.5 in the other, both
// perfectly defined, and the same game 3 dB louder on the phone.
//
// What is compared is the voice list each runtime derives from a Sound, not the
// audio. See internal/conformance/sound.go for why that is the honest layer and
// what it therefore cannot catch.

// soundDriverJS walks the Sound values the JS runtime builds. It reads the same
// `.voices` the renderer reads, so nothing here is a parallel decoding that
// could be right while the real one is wrong.
const soundDriverJS = `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const fields = JSON.parse(process.argv[4]);
const names = JSON.parse(process.argv[5]);

// "Unasked" has three spellings between the two runtimes: a missing key, a
// unit, and a materialised zero. They all MEAN the same thing, because both
// decoders read a missing numeric field as 0, so the comparison is of meaning
// and not of which spelling a builder happened to leave behind. Carrier shape
// is a difference this harness deliberately does not report.
const cell = (field, v) => {
  const absent = v === undefined || v === null;
  if (field === 'arp') {
    const xs = Array.isArray(v) ? v : (v && Array.isArray(v.xs) ? v.xs : []);
    return xs.length ? xs.map((x) => cell('', x)).join('/') : '-';
  }
  if (absent) return '0';
  if (typeof v === 'object') {
    if (v.tag) return v.tag;
    if (v.n !== undefined) return String(v.n);
    if (v.s !== undefined) return v.s;
  }
  return String(v);
};

const out = [];
for (const name of names) {
  const snd = globalThis.__marEvalRaw(program, 'SoundConform.' + name);
  const voices = (snd && snd.voices) || [];
  out.push(name + ' voices=' + voices.length);
  voices.forEach((v, i) => {
    out.push('  ' + i + ' ' + fields.map((f) => f + '=' + cell(f, v[f])).join(' '));
  });
}
process.stdout.write(out.join('\n'));
`

// soundDriverSwift is the same walk over the Swift value. It reads the MarValue
// directly rather than MarSound's own decoder, which is private: the point is
// to compare what the Sound CARRIES, and going through either side's decoder
// would compare a decoding instead.
const soundDriverSwift = `import Foundation

let data = try! Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1]))
let program = try! MarJSONCodec.decodeProgram(data)
let env = MarBuiltins.makeEnv()
do {
    for m in program.modules { try MarLoader.load(module: m, into: env) }
} catch {
    FileHandle.standardError.write("mar error: \(error)\n".data(using: .utf8)!)
    exit(2)
}

let fields = CommandLine.arguments[2].split(separator: ",").map(String.init)
let names = CommandLine.arguments[3].split(separator: ",").map(String.init)

// Mirrors the JS driver's rule: a missing field, a unit and a zero are one
// answer, "unasked", spelled "0". See the note there.
func cell(_ field: String, _ v: MarValue?) -> String {
    if field == "arp" {
        if case .list(let xs)? = v, !xs.isEmpty {
            return xs.map { cell("", $0) }.joined(separator: "/")
        }
        return "-"
    }
    guard let v = v else { return "0" }
    switch v {
    case .int(let n): return String(n)
    case .float(let d): return d == d.rounded() ? String(Int(d)) : String(d)
    case .string(let s): return s
    case .bool(let b): return b ? "True" : "False"
    case .ctor(let tag, _, _): return tag
    case .unit: return "0"
    case .list(let xs): return xs.isEmpty ? "-" : xs.map { cell("", $0) }.joined(separator: "/")
    default: return "?" + String(describing: v).prefix(40)
    }
}

var out: [String] = []
for name in names {
    guard let v = env.lookup("SoundConform." + name) else {
        FileHandle.standardError.write("unbound: \(name)\n".data(using: .utf8)!)
        exit(1)
    }
    var voices: [MarValue] = []
    if case .ctor(let tag, let args, _) = v, tag == "__Snd",
       let first = args.first, case .list(let recs) = first {
        voices = recs
    }
    out.append(name + " voices=\(voices.count)")
    for (i, rec) in voices.enumerated() {
        var f: [String: MarValue] = [:]
        if case .record(let fields, _) = rec { f = fields }
        let cells = fields.map { $0 + "=" + cell($0, f[$0]) }
        out.append("  \(i) " + cells.joined(separator: " "))
    }
}
FileHandle.standardOutput.write(out.joined(separator: "\n").data(using: .utf8)!)
`

func TestSoundVoicesMatchAcrossRuntimes(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the Swift runtime; skipped under -short")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the Swift runtime needs the Apple SDKs (Security, AVFoundation, Network)")
	}
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc not installed")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}

	mod, err := parser.Parse(conformance.SoundSource)
	if err != nil {
		t.Fatalf("parse the sound corpus: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck the sound corpus: %v", err)
	}
	programJSON, err := json.Marshal(map[string]any{
		"modules": []any{jsserve.SerializeModule(mod)},
	})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}

	fields := strings.Join(conformance.SoundFields, ",")
	names := strings.Join(conformance.SoundFixtures, ",")

	// The JS half.
	jsDir := t.TempDir()
	// The shipped source, not the minified production build: __marEvalRaw is a
	// test hook and the production pass drops it. What is compared either way
	// is the value the same builders produce.
	write(t, filepath.Join(jsDir, "runtime.js"), []byte(jsserve.RuntimeJS()))
	write(t, filepath.Join(jsDir, "program.json"), programJSON)
	write(t, filepath.Join(jsDir, "driver.js"), []byte(soundDriverJS))
	fieldsJSON, _ := json.Marshal(conformance.SoundFields)
	namesJSON, _ := json.Marshal(conformance.SoundFixtures)
	jsCmd := exec.Command(nodePath, filepath.Join(jsDir, "driver.js"),
		filepath.Join(jsDir, "runtime.js"), filepath.Join(jsDir, "program.json"),
		string(fieldsJSON), string(namesJSON))
	var jsErr strings.Builder
	jsCmd.Stderr = &jsErr
	jsOut, err := jsCmd.Output()
	if err != nil {
		t.Fatalf("node run: %v\n%s", err, jsErr.String())
	}

	// The Swift half, headless on the host: no simulator, no Xcode project.
	swDir := t.TempDir()
	shimmed := extractHeadlessSwift(t, swDir)
	assertShimsHideNoStdlib(t, shimmed)
	write(t, filepath.Join(swDir, "program.json"), programJSON)
	write(t, filepath.Join(swDir, "_headless_shims.swift"), []byte(swiftDisplayShims))
	write(t, filepath.Join(swDir, "main.swift"), []byte(soundDriverSwift))

	sources, err := filepath.Glob(filepath.Join(swDir, "*.swift"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no Swift sources to compile: %v", err)
	}
	bin := filepath.Join(swDir, "soundconform")
	if out, err := exec.Command(swiftc, append([]string{"-o", bin}, sources...)...).CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}
	swCmd := exec.Command(bin, filepath.Join(swDir, "program.json"), fields, names)
	var swErr strings.Builder
	swCmd.Stderr = &swErr
	swOut, err := swCmd.Output()
	if err != nil {
		t.Fatalf("swift run: %v\n%s", err, swErr.String())
	}

	web := strings.Split(strings.TrimSpace(string(jsOut)), "\n")
	ios := strings.Split(strings.TrimSpace(string(swOut)), "\n")
	if len(web) == 0 || web[0] == "" {
		t.Fatal("the JS driver produced nothing; the corpus never reached the runtime")
	}

	// Report every differing line rather than the first: a field that moved
	// shifts many lines at once, and seeing them together is what tells a
	// systematic difference from a single wrong number.
	diffs := 0
	for i := 0; i < len(web) || i < len(ios); i++ {
		w, s := "", ""
		if i < len(web) {
			w = web[i]
		}
		if i < len(ios) {
			s = ios[i]
		}
		if w != s {
			diffs++
			if diffs <= 20 {
				t.Errorf("voice %d differs:\n  web: %s\n  iOS: %s", i, w, s)
			}
		}
	}
	if diffs > 20 {
		t.Errorf("... and %d more differing lines", diffs-20)
	}
	if diffs == 0 {
		t.Logf("%d Sound fixtures resolve to the same voices in both runtimes", len(conformance.SoundFixtures))
	}
}

// The corpus is only a promise if it names everything. A Sound function with
// no fixture is the one free to drift, which is the same reason the string
// corpus has a coverage gate over its own modules.
//
// What has to appear is decided by the TYPE, not by a list somebody keeps up to
// date. Two kinds qualify: a combinator answers a `Sound`, and a note helper is
// the `Int -> Int` pitch table whose answer lands in a voice's frequency. What
// is left out is left out for a reason that is about the value, not about
// effort: the players answer a `Sub` or a `Cmd`, so they carry a Sound rather
// than building one, and the wave constructors are arguments to `tone`, already
// compared through every fixture that names one.
func TestSoundCorpusCoversTheModule(t *testing.T) {
	env := typecheck.BaseEnv()
	var missing []string
	for _, name := range env.Names() {
		if !strings.HasPrefix(name, "Sound.") {
			continue
		}
		scheme, ok := env.Lookup(name)
		if !ok {
			continue
		}
		sig := typecheck.Pretty(scheme)
		builds := strings.HasSuffix(sig, "Sound")
		note := sig == "Int -> Int"
		if !builds && !note {
			continue
		}
		fn := strings.TrimPrefix(name, "Sound.")
		// Word-bounded: `Sound.a` must not be satisfied by `Sound.arp`.
		used := regexp.MustCompile(`\bSound\.` + regexp.QuoteMeta(fn) + `\b`)
		if !used.MatchString(conformance.SoundSource) {
			missing = append(missing, name+" : "+sig)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d Sound functions have no fixture in SoundSource:\n  %s\n"+
			"Add one. A function nobody compares is a function free to mean two "+
			"different things on the two runtimes that make sound.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

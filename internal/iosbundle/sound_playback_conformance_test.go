package iosbundle

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"mar/internal/conformance"
	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Sound, compared by what the two runtimes PLAY.
//
// TestSoundVoicesMatchAcrossRuntimes, in the file next to this one, compares the
// voice list each runtime BUILDS from a Sound and says in its own header that it
// cannot see further. This is the other side of that line. What is compared, why
// those things and not others, and why the answers can be trusted are all in
// internal/conformance/sound_playback.go, next to the two drivers.
//
// Read that first: the value of this test is entirely in whether each side is
// ANSWERING or COMPUTING, and that argument lives with the drivers.

// One runtime's answer, parsed.
type playback struct {
	rest  map[string]bool    // "fixture/voice"
	span  map[string]float64 // "fixture/voice"
	gain  map[string]float64 // "fixture/voice/k"
	freq  map[string]float64 // "fixture/voice/k"
	order []string           // "fixture/voice", in the order printed
}

func parsePlayback(t *testing.T, side, out string) *playback {
	t.Helper()
	p := &playback{rest: map[string]bool{}, span: map[string]float64{},
		gain: map[string]float64{}, freq: map[string]float64{}}
	seen := map[string]bool{}
	num := func(s, line string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%s driver: unreadable number in %q", side, line)
		}
		return v
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		voice := f[0] + "/" + f[1]
		if !seen[voice] {
			seen[voice] = true
			p.order = append(p.order, voice)
		}
		switch f[2] {
		case "rest":
			p.rest[voice] = true
		case "nosource":
			t.Fatalf("%s driver: %s built no source for %s, so it cannot say what it plays", side, side, voice)
		case "span":
			p.span[voice] = num(f[3], line)
		default:
			if len(f) < 5 {
				t.Fatalf("%s driver: unreadable row %q", side, line)
			}
			p.gain[voice+"/"+f[2]] = num(f[3], line)
			p.freq[voice+"/"+f[2]] = num(f[4], line)
		}
	}
	return p
}

// Cents between two pitches, with the two ways a pitch can be absent handled
// rather than turned into an infinity: freq 0 is how a voice says "no pitch",
// and it is a real answer that both sides can give.
func pitchCents(a, b float64) (float64, bool) {
	if a <= 0 && b <= 0 {
		return 0, true
	}
	if a <= 0 || b <= 0 {
		return math.Inf(1), false
	}
	return 1200 * math.Log2(a/b), true
}

func gainClose(a, b float64) bool {
	return math.Abs(a-b) <= conformance.PlaybackGainAbsTol+
		conformance.PlaybackGainRelTol*math.Max(math.Abs(a), math.Abs(b))
}

func TestSoundPlaybackMatchesAcrossRuntimes(t *testing.T) {
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
	programJSON, err := json.Marshal(map[string]any{"modules": []any{jsserve.SerializeModule(mod)}})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	names := strings.Join(conformance.SoundFixtures, ",")
	namesJSON, _ := json.Marshal(conformance.SoundFixtures)
	samples := strconv.Itoa(conformance.PlaybackSamples)

	// --- the web half.
	jsDir := t.TempDir()
	write(t, filepath.Join(jsDir, "runtime.js"), []byte(jsserve.RuntimeJS()))
	write(t, filepath.Join(jsDir, "program.json"), programJSON)
	write(t, filepath.Join(jsDir, "driver.js"), []byte(conformance.PlaybackDriverJS))
	runNode := func(args ...string) string {
		cmd := exec.Command(nodePath, append([]string{filepath.Join(jsDir, "driver.js"),
			filepath.Join(jsDir, "runtime.js")}, args...)...)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("node run: %v\n%s", err, errBuf.String())
		}
		return strings.TrimSpace(string(out))
	}

	// The evaluator answers for the browser, so it is the one part of this
	// harness that is neither runtime. It checks itself against hand-computed
	// values BEFORE anything is compared: a wrong interpolation would otherwise
	// show up as a difference between two runtimes that actually agree.
	for _, line := range strings.Split(runNode("--selftest"), "\n") {
		if strings.HasPrefix(line, "FAIL") {
			t.Errorf("the automation evaluator is wrong, so nothing it reports about the two runtimes means anything:\n  %s", line)
		}
	}
	if t.Failed() {
		return
	}

	jsOut := runNode(filepath.Join(jsDir, "program.json"), string(namesJSON), samples)
	jsCeiling := runNode("--ceiling")
	jsWave := runNode("--wavelevel", filepath.Join(jsDir, "program.json"), string(namesJSON))
	jsBed := runNode("--bed", filepath.Join(jsDir, "program.json"), string(namesJSON),
		fmt.Sprint(conformance.BedSettleSeconds), fmt.Sprint(conformance.BedWindowSeconds))

	// --- the iOS half, headless on the host: no simulator, no Xcode project.
	swDir := t.TempDir()
	shimmed := extractHeadlessSwift(t, swDir)
	assertShimsHideNoStdlib(t, shimmed)
	write(t, filepath.Join(swDir, "program.json"), programJSON)
	write(t, filepath.Join(swDir, "_headless_shims.swift"), []byte(swiftDisplayShims))
	write(t, filepath.Join(swDir, "main.swift"), []byte(conformance.PlaybackDriverSwift))
	sources, err := filepath.Glob(filepath.Join(swDir, "*.swift"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no Swift sources to compile: %v", err)
	}
	bin := filepath.Join(swDir, "playbackconform")
	if out, err := exec.Command(swiftc, append([]string{"-o", bin}, sources...)...).CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}
	runSwift := func(args ...string) string {
		cmd := exec.Command(bin, append([]string{filepath.Join(swDir, "program.json")}, args...)...)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("swift run: %v\n%s", err, errBuf.String())
		}
		return strings.TrimSpace(string(out))
	}
	swOut := runSwift(names, samples)
	swCeiling := runSwift("--ceiling")
	swWave := runSwift("--wavelevel", names)
	swBed := runSwift("--bed", names, fmt.Sprint(conformance.BedSettleSeconds), fmt.Sprint(conformance.BedWindowSeconds))

	// What a HELD source settles at. This is where the 9 dB noise-bed drift
	// lived, and where a tremolo that was computed and then dropped hid for a
	// day: neither is visible in a note's curves, because a bed has no note.
	compareBed(t, jsBed, swBed)

	// One cycle of Sound.loop. Not a curve and not per voice: it is the tempo,
	// and a whole soundtrack running at a different speed on the phone is a
	// larger failure than any single note's level.
	compareLoopPeriod(t, runNode("--loop", filepath.Join(jsDir, "program.json"), string(namesJSON)),
		runSwift("--loop", names))

	// How loud the raw wave is, before any envelope. The two runtimes are
	// allowed to disagree about a wave's SHAPE and not about its LEVEL, and the
	// gain curves below cannot tell the two apart: a wave that comes out of the
	// browser quieter than it comes out of the phone passes through identical
	// gain nodes on both.
	compareWaveLevel(t, jsWave, swWave)

	// The master transfer function, which is not a per-voice curve and so is
	// compared on its own. It is swept PAST the web table's domain on purpose:
	// below the knee both runtimes are the identity and between the knee and the
	// span both bend by the same tanh, so the only place they can differ is
	// where one of them runs out of table. That is exactly where they did.
	compareCeiling(t, jsCeiling, swCeiling)

	web := parsePlayback(t, "web", jsOut)
	ios := parsePlayback(t, "iOS", swOut)
	if len(web.order) == 0 {
		t.Fatal("the web driver produced nothing; the corpus never reached the runtime")
	}

	// --- the comparison.
	//
	// Report the WORST divergence per fixture rather than the first sample that
	// crosses the line: a curve that has come apart is wrong at fifty points,
	// and the largest gap is the one that says how badly.
	type worst struct {
		gainAt           string
		gainWeb, gainIOS float64
		gainGap          float64
		freqAt           string
		freqWeb, freqIOS float64
		cents            float64
	}
	worstBy := map[string]*worst{}
	fixtureOf := func(voice string) string { return strings.SplitN(voice, "/", 2)[0] }
	note := func(fixture string) *worst {
		if worstBy[fixture] == nil {
			worstBy[fixture] = &worst{}
		}
		return worstBy[fixture]
	}

	voices := map[string]bool{}
	for _, v := range web.order {
		voices[v] = true
	}
	for _, v := range ios.order {
		voices[v] = true
	}
	keys := make([]string, 0, len(voices))
	for v := range voices {
		keys = append(keys, v)
	}
	sort.Strings(keys)

	structural := 0
	for _, voice := range keys {
		fixture := fixtureOf(voice)
		if web.rest[voice] != ios.rest[voice] {
			structural++
			t.Errorf("%s: one runtime plays this voice and the other treats it as a rest (web rest=%v, iOS rest=%v)",
				voice, web.rest[voice], ios.rest[voice])
			continue
		}
		if web.rest[voice] {
			continue
		}
		ws, wok := web.span[voice]
		is, iok := ios.span[voice]
		if !wok || !iok {
			structural++
			t.Errorf("%s: missing from the %s driver's answer", voice,
				map[bool]string{true: "iOS", false: "web"}[wok])
			continue
		}
		if math.Abs(ws-is) > conformance.PlaybackSpanTol {
			structural++
			t.Errorf("%s: the note lasts %.6f s on the web and %.6f s on iOS", voice, ws, is)
		}
		for k := 0; k <= conformance.PlaybackSamples; k++ {
			key := fmt.Sprintf("%s/%d", voice, k)
			wg, wgok := web.gain[key]
			ig, igok := ios.gain[key]
			if !wgok || !igok {
				structural++
				t.Errorf("%s: sample %d missing from one side", voice, k)
				break
			}
			if !gainClose(wg, ig) {
				gap := math.Abs(wg - ig)
				if w := note(fixture); gap > w.gainGap {
					w.gainGap, w.gainAt, w.gainWeb, w.gainIOS = gap, key, wg, ig
				}
			}
			wf, iff := web.freq[key], ios.freq[key]
			c, ok := pitchCents(wf, iff)
			if !ok || math.Abs(c) > conformance.PlaybackCentsTol {
				if w := note(fixture); math.Abs(c) > math.Abs(w.cents) || math.IsInf(c, 0) {
					w.cents, w.freqAt, w.freqWeb, w.freqIOS = c, key, wf, iff
				}
			}
		}
	}

	bad := make([]string, 0, len(worstBy))
	for f := range worstBy {
		bad = append(bad, f)
	}
	sort.Strings(bad)
	for _, f := range bad {
		w := worstBy[f]
		if w.gainGap > 0 {
			t.Errorf("%s: the LEVEL differs. Worst at %s: web %.9g, iOS %.9g (%s)",
				f, w.gainAt, w.gainWeb, w.gainIOS, dbGap(w.gainWeb, w.gainIOS))
		}
		if w.freqAt != "" {
			if math.IsInf(w.cents, 0) {
				t.Errorf("%s: the PITCH differs. At %s one runtime sounds %.4f Hz and the other has no pitch at all (%.4f)",
					f, w.freqAt, math.Max(w.freqWeb, w.freqIOS), math.Min(w.freqWeb, w.freqIOS))
			} else {
				t.Errorf("%s: the PITCH differs. Worst at %s: web %.6f Hz, iOS %.6f Hz (%.2f cents)",
					f, w.freqAt, w.freqWeb, w.freqIOS, w.cents)
			}
		}
	}
	if len(bad) > 0 || structural > 0 {
		t.Errorf("\nThe two runtimes build the same voices and play them differently, which is the gap "+
			"this test exists to close: the same app sounds different in the browser and on the phone, "+
			"and the voice-record corpus next door cannot see it.\n"+
			"%d of %d fixtures diverge.", len(bad), len(conformance.SoundFixtures))
		return
	}
	t.Logf("%d Sound fixtures play the same way in both runtimes, over %d samples each",
		len(conformance.SoundFixtures), conformance.PlaybackSamples+1)
}

// A level difference in the unit levels are argued in, with the one case that
// has no dB spelled out rather than printed as an infinity.
func dbGap(a, b float64) string {
	if a <= 0 || b <= 0 {
		return fmt.Sprintf("one side is silent: %.9g against %.9g", a, b)
	}
	return fmt.Sprintf("%.2f dB", 20*math.Log10(a/b))
}

// compareCeiling reads the two sweeps of the master transfer function.
//
// The tolerance is looser than the curve tolerances and deliberately so: the web
// answers from a 8192-entry lookup table read at its NEAREST entry, and iOS
// evaluates the tanh. A table step near the knee is worth about 0.0005, so
// anything under a thousandth is the table's resolution rather than a
// difference of behaviour.
//
// What this does NOT compare, and should not be read as covering: the shaper
// runs at oversample 2x, so the web filters around the bend to keep the
// harmonics it creates from aliasing, and iOS applies the curve per sample. The
// transfer functions agreeing does not make the two outputs identical above the
// knee; it makes them the same SHAPE.
func compareCeiling(t *testing.T, jsOut, swOut string) {
	t.Helper()
	read := func(out string) map[string][2]float64 {
		m := map[string][2]float64{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Fields(line)
			if len(f) != 4 {
				continue
			}
			x, _ := strconv.ParseFloat(f[2], 64)
			y, _ := strconv.ParseFloat(f[3], 64)
			m[f[1]] = [2]float64{x, y}
		}
		return m
	}
	w, i := read(jsOut), read(swOut)
	if len(w) == 0 || len(i) == 0 {
		t.Fatal("a ceiling sweep came back empty")
	}
	const tol = 1e-3
	worstGap, worstAt := 0.0, 0.0
	for k, wv := range w {
		iv, ok := i[k]
		if !ok {
			t.Errorf("the ceiling sweep has point %s on the web and not on iOS", k)
			continue
		}
		if math.Abs(wv[0]-iv[0]) > 1e-9 {
			t.Fatalf("the two ceiling sweeps are not over the same inputs (%g against %g)", wv[0], iv[0])
		}
		if gap := math.Abs(wv[1] - iv[1]); gap > worstGap {
			worstGap, worstAt = gap, wv[0]
		}
	}
	if worstGap > tol {
		wv, iv := w[fmt.Sprint(int(math.Round((worstAt+4)/0.1)))], i[fmt.Sprint(int(math.Round((worstAt+4)/0.1)))]
		t.Errorf("the master ceiling differs between the runtimes. Worst at input %.2f: web %.6f, iOS %.6f (%.2f dB).\n"+
			"Every sound in the app passes through this, and above the knee a difference here is not "+
			"loudness but harmonics: the same loud passage is a different kind of loud on the two platforms.",
			worstAt, wv[1], iv[1], 20*math.Log10(math.Max(wv[1], 1e-12)/math.Max(iv[1], 1e-12)))
	}
}

// compareWaveLevel reads the two answers to "how loud is this wave".
//
// The tolerance is 0.5 dB, which is far wider than a level comparison would
// normally want and is set by what the two runtimes legitimately differ by: the
// browser's oscillators are band-limited to Nyquist and this side's polyBLEP
// rounds the edges by roughly one sample, so the same square is not quite the
// same square. It is still twenty times narrower than the difference it is here
// to find.
func compareWaveLevel(t *testing.T, jsOut, swOut string) {
	t.Helper()
	read := func(out string) map[string][2]float64 {
		m := map[string][2]float64{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || f[2] != "wave" {
				continue
			}
			rms, _ := strconv.ParseFloat(f[3], 64)
			dc, _ := strconv.ParseFloat(f[4], 64)
			m[f[0]+"/"+f[1]] = [2]float64{rms, dc}
		}
		return m
	}
	w, i := read(jsOut), read(swOut)
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		iv, ok := i[k]
		if !ok {
			t.Errorf("%s: the web measures this wave and iOS skips it", k)
			continue
		}
		wv := w[k]
		if known, ok := conformance.KnownWaveLevelGaps[strings.SplitN(k, "/", 2)[0]]; ok {
			moved := math.Abs(wv[0]-known.WebRMS) > 0.01 || math.Abs(iv[0]-known.IOSRMS) > 0.01 ||
				math.Abs(wv[1]-known.WebDC) > 0.01 || math.Abs(iv[1]-known.IOSDC) > 0.01
			if moved {
				t.Errorf("%s: a KNOWN divergence moved.\n"+
					"  recorded: web rms %.4f dc %+.4f | iOS rms %.4f dc %+.4f\n"+
					"  now:      web rms %.4f dc %+.4f | iOS rms %.4f dc %+.4f\n%s\n"+
					"If it was fixed, delete the entry in conformance.KnownWaveLevelGaps. If it moved for "+
					"another reason, that reason is new.",
					k, known.WebRMS, known.WebDC, known.IOSRMS, known.IOSDC, wv[0], wv[1], iv[0], iv[1], known.Why)
			}
			continue
		}
		if gap := 20 * math.Log10(math.Max(wv[0], 1e-12)/math.Max(iv[0], 1e-12)); math.Abs(gap) > 0.5 {
			t.Errorf("%s: the WAVE is a different level on the two runtimes: web rms %.4f, iOS rms %.4f (%.2f dB).\n"+
				"Nothing in the gain curves can see this - the same envelope multiplies a louder wave on one "+
				"platform - so it is heard as the whole voice being louder there.", k, wv[0], iv[0], gap)
		}
		if math.Abs(wv[1]-iv[1]) > 0.01 {
			t.Errorf("%s: the wave carries a different DC OFFSET: web %+.4f, iOS %+.4f.\n"+
				"An offset is headroom spent on nothing, and it makes a note cross the ceiling knee on one "+
				"polarity before the other.", k, wv[1], iv[1])
		}
	}
}

// compareBed reads what a held source settles at on each runtime: the mean level
// over a window once it has settled, and the extremes inside that window.
//
// The mean is the level. The SPREAD is whether anything is still moving - a
// tremolo, a vibrato that reaches the gain - and it is there because a
// modulator that is computed and then not applied leaves the mean untouched and
// the spread flat.
func compareBed(t *testing.T, jsOut, swOut string) {
	t.Helper()
	read := func(out string) map[string][3]float64 {
		m := map[string][3]float64{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 6 || f[2] != "bed" {
				continue
			}
			mean, _ := strconv.ParseFloat(f[3], 64)
			lo, _ := strconv.ParseFloat(f[4], 64)
			hi, _ := strconv.ParseFloat(f[5], 64)
			m[f[0]+"/"+f[1]] = [3]float64{mean, lo, hi}
		}
		return m
	}
	w, i := read(jsOut), read(swOut)
	if len(w) == 0 {
		t.Error("the web driver started no held sources; the bed comparison is not running")
		return
	}
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		iv, ok := i[k]
		if !ok {
			t.Errorf("%s: the web holds this voice and iOS does not", k)
			continue
		}
		wv := w[k]
		if !gainClose(wv[0], iv[0]) {
			t.Errorf("%s: a HELD source settles at a different level: web %.6f, iOS %.6f (%s).\n"+
				"An ambient bed is a wash that plays for as long as the screen is up, so a level that "+
				"differs here is the difference between hearing the weather and not.",
				k, wv[0], iv[0], dbGap(wv[0], iv[0]))
		}
		wSpread, iSpread := wv[2]-wv[1], iv[2]-iv[1]
		if math.Abs(wSpread-iSpread) > 0.01*math.Max(wv[0], iv[0])+1e-6 {
			t.Errorf("%s: a held source MOVES differently once settled: the web swings %.6f and iOS swings %.6f "+
				"around means of %.6f and %.6f.\nA modulator that is computed and not applied looks exactly "+
				"like this: the level is right and nothing wobbles.",
				k, wSpread, iSpread, wv[0], iv[0])
		}
	}
}

// compareLoopPeriod reads the cycle length each scheduler would use.
func compareLoopPeriod(t *testing.T, jsOut, swOut string) {
	t.Helper()
	read := func(out string) map[string]string {
		m := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Fields(line)
			if len(f) == 4 && f[2] == "period" {
				m[f[0]] = f[3]
			}
		}
		return m
	}
	w, i := read(jsOut), read(swOut)
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		iv, ok := i[k]
		if !ok {
			continue
		}
		if w[k] == "none" || iv == "none" {
			// Both refusing is agreement: a Sound with nothing to sound is not a
			// loop on either runtime.
			if w[k] != iv {
				t.Errorf("%s: one runtime loops this Sound and the other refuses to (web %s, iOS %s).\n"+
					"Silence against a repeating buzz is the same disagreement about a zero length that "+
					"the note floor had, one layer up.", k, w[k], iv)
			}
			continue
		}
		wv, _ := strconv.ParseFloat(w[k], 64)
		ivf, _ := strconv.ParseFloat(iv, 64)
		if math.Abs(wv-ivf) > 1e-6 {
			t.Errorf("%s: one cycle of Sound.loop is %.6f s on the web and %.6f s on iOS.\n"+
				"The whole track runs at a different speed on one platform.", k, wv, ivf)
		}
	}
}

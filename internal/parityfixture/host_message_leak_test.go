package parityfixture

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A message the host wrote, handed to a Mar program.
//
// Foundation's `localizedDescription` is not just the wrong wording — it is
// LOCALIZED. The same dropped connection reads one way on an English phone and
// another way on a Portuguese one, so a program that shows it shows different
// text to different users, and a program that matches on it matches on one
// device and not another. It was fixed once in MarWire, and was still alive in
// the phone's wire port and in every auth path beside it.
//
// The rule: `localizedDescription` may be LOGGED (print) or shown on a
// developer error screen (record), because neither is a value a program
// receives. Anywhere else it has to be listed here with a reason.
func TestNoHostMessageReachesAMarProgram(t *testing.T) {
	// path -> why this one is allowed to carry the host's words.
	allowed := map[string]string{
		"APIClient.swift":     "the template's own sample API client, which is app code and not the runtime",
		"AppViewModel.swift":  "the app shell's own error display, not a Mar value",
		"MarAppContext.swift": "a builder failure on the developer's screen, at dev time",
	}

	files, err := filepath.Glob("../iosbundle/template/Sources/*.swift")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Swift sources: %v", err)
	}
	// Every way Foundation will hand you its own words about an error, not one
	// of them. Measured: a leak written as `localizedFailureReason` slipped
	// through the first version of this test without a sound, because the
	// pattern named a single accessor.
	hostWords := regexp.MustCompile(`localizedDescription|localizedFailureReason|` +
		`localizedRecoverySuggestion|failureReason|recoverySuggestion|\\\(error\)`)
	// A line that only LOGS or only records is fine; anything else that names
	// the host's message is a value going somewhere.
	logging := regexp.MustCompile(`(print\(|record\()`)

	// The population, pinned. Classifying the leaks is only half the job: a
	// leak REWORDED out of the pattern leaves no trace unless the number it
	// belonged to is watched. Measured -- a new leak written with a different
	// Foundation accessor passed this test in silence until the count existed.
	population := map[string]int{
		"APIClient.swift":      2,
		"AppViewModel.swift":   3,
		"MarAppContext.swift":  1,
		"MarHTTP.swift":        4,
		"MarPageRuntime.swift": 5,
		"MarSharedStore.swift": 2,
	}

	var problems []string
	used := map[string]bool{}
	counted := map[string]int{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if !hostWords.MatchString(trimmed) {
				continue
			}
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "///") {
				continue
			}
			counted[name]++
			if logging.MatchString(trimmed) {
				continue
			}
			if _, ok := allowed[name]; ok {
				used[name] = true
				continue
			}
			problems = append(problems, name+":"+itoa(i+1)+
				" hands the host's own words about an error somewhere that is not a log.\n"+
				"    It is localized: the same failure reads differently depending on the\n"+
				"    language the phone is set to. Answer with Mar's own sentence (see\n"+
				"    MarHTTP.transportMessage and MarHTTP.notJSONMessage), or add the file\n"+
				"    here with the reason it is not a value a program receives.")
		}
	}
	for name := range allowed {
		if !used[name] {
			problems = append(problems, "the list allows "+name+", which no longer carries one; drop the row")
		}
	}
	for name, n := range counted {
		want, listed := population[name]
		if !listed {
			problems = append(problems, name+" names the host's words "+itoa(n)+
				" time(s) and is not in the population.\n"+
				"    Add it, so a later rewording cannot take it back out of view.")
			continue
		}
		if want != n {
			problems = append(problems, name+": the population says "+itoa(want)+
				", the file names the host's words "+itoa(n)+" time(s).\n"+
				"    A leak that moves out of the pattern leaves no other trace; adjust the count deliberately.")
		}
	}
	for name, want := range population {
		if _, ok := counted[name]; !ok {
			problems = append(problems, name+" is in the population with "+itoa(want)+
				" and now names the host's words nowhere; drop the row")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// The two sentences a transport failure gets. The phone answers with them
// because the browser does; this checks the two sources still spell them the
// same, which is what would silently drift. It proves the WORDING, not the
// branch — no harness can drop a real connection on both runtimes.
func TestTheTransportSentencesAreSpelledTheSameOnBothClients(t *testing.T) {
	sentences := []string{
		"You appear to be offline. Check your connection and try again.",
		"Couldn't reach the server. Try again in a moment.",
	}
	for _, f := range []struct{ label, path string }{
		{"browser", "../jsserve/runtime.js"},
		{"phone", "../iosbundle/template/Sources/MarHTTP.swift"},
	} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("%s: %v", f.label, err)
		}
		for _, s := range sentences {
			if !strings.Contains(string(b), s) {
				t.Errorf("the %s runtime no longer says %q; the two clients have drifted apart",
					f.label, s)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

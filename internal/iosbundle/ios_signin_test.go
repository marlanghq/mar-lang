package iosbundle

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Signing in on the iPhone, against a real server, across relaunches.
//
// Sign-in on the phone was broken several ways at once, and every check
// stopped short of all of them. The phone asked GET /_auth/me, a route the
// server never had (the server answered with the app's HTML shell and a 200,
// read as "nobody"); a relaunch opened on the first declared page, which in
// every example with sign-in is the sign-in page; and when a DEBUG build
// found its `mar dev` after launch, the protected page had already asked the
// baked URL and kept that answer. Each client was tested against fixtures and
// each half passed. What a person saw was the sign-in screen after a correct
// code, and again at every launch.
//
// So this runs what the person runs: `mar dev` on examples/hello-auth, the app
// `mar build --target ios` makes for it, and the simulator. The driver inside
// the app (AppViewModel.signInCheck) types and taps through the program's own
// messages and prints the screen it comes to rest on; the code is read from
// the server's log, where `mar dev` prints it, and handed to the app through a
// file in its container.
//
// It costs a build of mar, two Xcode builds and a simulator, so it skips under
// -short and on a machine without them.
func TestSignInOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("runs mar dev, builds an iOS app and drives a simulator")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not found (needs Xcode)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not found (builds the mar CLI)")
	}
	udid, err := bootedSimulator()
	if err != nil {
		t.Skip("no iOS simulator available: " + err.Error())
	}

	work := t.TempDir()
	start := time.Now()
	mar := buildMarCLI(t, work)
	server := startSignInServer(t, mar, work, testPort(t))
	t.Logf("mar built and mar dev serving at %s in %s", server.url, time.Since(start).Round(time.Millisecond))

	signIn := `/sign-in "Sign in"`
	hello := func(email string) string { return `/ "Hello, ` + email + `"` }
	signedIn := func(email string) string {
		return "cold=" + signIn +
			// The route carries the email as Nav.pushTo encodes it.
			` | code-sent=/sign-in/verify/` + strings.ReplaceAll(email, "@", "%40") + ` "Enter your code"` +
			" | verified=" + hello(email)
	}

	// The app as it ships: the server's URL baked in, and nothing else.
	t.Run("BakedServer", func(t *testing.T) {
		app := installSignInApp(t, mar, work, "baked", server.url, udid)
		const email = "baked@example.com"
		runSignInSteps(t, app, server, nil, []signInStep{
			{
				name: "FreshInstallSignsIn",
				step: "sign-in:" + email,
				want: signedIn(email),
				hint: "verified=" + signIn + "  → a correct code, and the gate still heard \"nobody\": " +
					"the phone asks a session route the server does not serve (MarHTTP, /_auth/whoami), " +
					"or Auth.completeSignIn kept a user the gate cached before sign-in.\n" +
					"verified=/ checking (still moving ...)  → the session check never answered.",
			},
			{
				name: "RelaunchStaysSignedInAndSignsOut",
				step: "sign-out",
				want: "cold=" + hello(email) + " | signed-out=" + signIn,
				hint: "cold=" + signIn + "  → a signed-in person was shown sign-in at launch: the app " +
					"opened on the first declared page instead of \"/\" (startPath), or the stored " +
					"session was not asked about.\n" +
					"signed-out=" + hello(email) + "  → sign-out left the person signed in.",
			},
			{
				name: "RelaunchAfterSignOutShowsSignIn",
				step: "look",
				want: "cold=" + signIn,
				hint: "cold=" + hello(email) + "  → the session outlived sign-out.",
			},
		})
	})

	// A DEBUG build whose baked URL has nothing behind it in time, the way a
	// phone on the Wi-Fi finds `mar dev` a moment after launch: the protected
	// page has already asked the baked URL when the server turns up. The
	// driver hands the server over at that moment, as the Bonjour browse
	// would; mDNS itself is not part of the check, because whether it works
	// is a fact about the machine's network rather than about Mar.
	t.Run("DevServerFoundAfterLaunch", func(t *testing.T) {
		silent := startSilentServer(t, testPort(t))
		app := installSignInApp(t, mar, work, "late", silent.url, udid)
		const email = "late@example.com"
		runSignInSteps(t, app, server, &signInSwitch{baked: silent, dev: server.url}, []signInStep{
			{
				name: "FreshInstallSignsIn",
				step: "sign-in:" + email,
				want: signedIn(email),
				hint: "cold=/ checking (still moving ...)  → the app switched servers and kept waiting " +
					"for the old one's answer (AppViewModel.adoptDevServer starts over at startPath).\n" +
					"verified=" + signIn + "  → a correct code, and the gate still heard \"nobody\": " +
					"an answer from before the switch was kept (the ProtectedRoute generation " +
					"guard) and Auth.completeSignIn did not forget it.",
			},
			{
				name: "RelaunchStaysSignedIn",
				step: "look",
				want: "cold=" + hello(email),
				hint: "cold=" + signIn + "  → the question the app asked the baked URL was cancelled " +
					"by the switch, came back as \"nobody\", and overwrote the dev server's answer: " +
					"the ProtectedRoute generation guard.\n" +
					"cold=/ checking (still moving ...)  → the app switched servers and kept waiting " +
					"for the old one's answer.",
			},
		})
		if len(silent.asked()) == 0 {
			t.Fatal("the app never asked its baked URL, so nothing here was found late")
		}
		t.Logf("the baked URL was asked: %v", silent.asked())
	})
}

// signInSwitch hands the app a dev server once its protected page's question
// has reached the baked URL.
type signInSwitch struct {
	baked *silentServer
	dev   string
}

type signInStep struct {
	name string
	step string
	want string
	hint string
}

// signInApp is a built app, installed fresh.
type signInApp struct {
	udid     string
	bundleID string
}

// runSignInSteps runs one person's afternoon, in order: each launch starts
// where the last one left the app, so the first step that fails ends it.
// sw, when set, hands the app a dev server found after launch.
func runSignInSteps(t *testing.T, app signInApp, server *signInServer, sw *signInSwitch, steps []signInStep) {
	t.Helper()
	for _, s := range steps {
		ok := t.Run(s.name, func(t *testing.T) {
			start := time.Now()
			got, log := runSignInStep(t, app, s.step, sw, server)
			t.Logf("%s (%s)", got, time.Since(start).Round(time.Millisecond))
			if got != s.want {
				t.Fatalf("the phone came to rest somewhere else.\n got: %s\nwant: %s\n\n%s\n\napp log:\n%s",
					got, s.want, s.hint, tailLines(log, 25))
			}
		})
		if !ok {
			break
		}
	}
}

// buildMarCLI builds the mar command from this checkout, so the server and
// the iOS template under test are the ones in the tree, not whatever mar is
// installed.
func buildMarCLI(t *testing.T, work string) string {
	t.Helper()
	bin := filepath.Join(work, "mar")
	build := exec.Command("go", "build", "-o", bin, "./cmd/mar")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building mar: %v\n%s", err, out)
	}
	return bin
}

// testPort is a port nothing is listening on, from the system's ephemeral
// range, so it never lands on a port someone's own server uses.
func testPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// copyExample copies examples/hello-auth's program to dir, with its own
// mar.json. The program is the example's; the manifest is the only thing each
// side changes.
func copyExample(t *testing.T, dir string, manifest map[string]any) {
	t.Helper()
	src := filepath.Join("..", "..", "examples", "hello-auth")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		// The example's own dev state (database, session secret, a dist/)
		// stays behind: a run starts with no users and no sessions.
		if d.IsDir() && (rel == ".mar" || rel == "dist") {
			return filepath.SkipDir
		}
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !strings.HasSuffix(path, ".mar") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the example: %v", err)
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "mar.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// signInServer is a running `mar dev`, and everything it printed.
type signInServer struct {
	url string
	mu  sync.Mutex
	out strings.Builder
}

func (s *signInServer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *signInServer) log() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// code is the last sign-in code `mar dev` printed for email, where it puts the
// email it would have sent when no mail is configured.
func (s *signInServer) code(email string) string {
	re := regexp.MustCompile(`To:\s+` + regexp.QuoteMeta(email) + `\s[\s\S]*?Your sign-in code is (\d{6})`)
	all := re.FindAllStringSubmatch(s.log(), -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

// startSignInServer runs `mar dev` on a copy of hello-auth. Its name is not
// the app's, on purpose: a DEBUG build adopts a `mar dev` advertising its own
// name over Bonjour, and every server this check talks to is one it chose.
func startSignInServer(t *testing.T, mar, work string, port int) *signInServer {
	t.Helper()
	dir := filepath.Join(work, "server")
	copyExample(t, dir, map[string]any{
		"name":   "signin-check-server",
		"locale": "en",
		"server": map[string]any{"port": port},
	})
	srv := &signInServer{url: fmt.Sprintf("http://127.0.0.1:%d", port)}
	cmd := exec.Command(mar, "dev", dir)
	cmd.Dir = dir
	cmd.Stdout = srv
	cmd.Stderr = srv
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting mar dev: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})

	// Ready when it answers the question the phone asks first.
	whoami := srv.url + "/_auth/whoami"
	deadline := time.Now().Add(90 * time.Second)
	for {
		select {
		case <-exited:
			t.Fatalf("mar dev exited before serving:\n%s", tailLines(srv.log(), 30))
		default:
		}
		if resp, err := http.Get(whoami); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "null" {
				return srv
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("mar dev did not answer %s in 90s:\n%s", whoami, tailLines(srv.log(), 30))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// silentServer accepts every request and answers none of them while the test
// runs: a baked URL with nothing behind it in time.
type silentServer struct {
	url  string
	mu   sync.Mutex
	seen []string
}

func (s *silentServer) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func startSilentServer(t *testing.T, port int) *silentServer {
	t.Helper()
	s := &silentServer{url: fmt.Sprintf("http://127.0.0.1:%d", port)}
	stop := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		select {
		case <-stop:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listening on %d: %v", port, err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		close(stop)
		_ = srv.Close()
	})
	return s
}

// installSignInApp builds the app the way a person does, `mar build --target
// ios` with baseURL baked in, compiles it for the simulator and installs it
// fresh. Each variant has its own bundle id, so its keychain holds only its
// own session.
func installSignInApp(t *testing.T, mar, work, variant, baseURL, udid string) signInApp {
	t.Helper()
	start := time.Now()
	bundleID := "dev.marlang.signincheck." + variant
	dir := filepath.Join(work, "app-"+variant)
	copyExample(t, dir, map[string]any{
		"name":   "SignInCheck",
		"locale": "en",
		"ios": map[string]any{
			"bundleId":         bundleID,
			"displayName":      "SignInCheck",
			"marketingVersion": "1.0.0",
			"buildNumber":      "1",
			"serverUrl":        baseURL,
		},
	})
	dist := filepath.Join(dir, "dist")
	if out, err := exec.Command(mar, "build", "--target", "ios", "--out", dist, dir).CombinedOutput(); err != nil {
		t.Fatalf("mar build --target ios: %v\n%s", err, out)
	}
	dd := filepath.Join(dir, "dd")
	build := exec.Command("xcodebuild",
		"-project", filepath.Join(dist, "SignInCheck", "SignInCheck.xcodeproj"),
		"-scheme", "SignInCheck",
		"-sdk", "iphonesimulator",
		"-destination", "platform=iOS Simulator,id="+udid,
		"-derivedDataPath", dd,
		"build")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the iOS template does not compile:\n%s", swiftErrors(string(out)))
	}
	built := filepath.Join(dd, "Build", "Products", "Debug-iphonesimulator", "SignInCheck.app")
	if _, err := os.Stat(built); err != nil {
		t.Fatal("the build produced no SignInCheck.app")
	}

	// A fresh install: nothing of an earlier run in the container. A token an
	// earlier run left in the keychain belongs to a server that is gone (each
	// run has its own database and session secret), so it signs no one in.
	_ = exec.Command("xcrun", "simctl", "terminate", udid, bundleID).Run()
	_ = exec.Command("xcrun", "simctl", "uninstall", udid, bundleID).Run()
	if out, err := exec.Command("xcrun", "simctl", "install", udid, built).CombinedOutput(); err != nil {
		t.Fatalf("simctl install: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("xcrun", "simctl", "terminate", udid, bundleID).Run()
		_ = exec.Command("xcrun", "simctl", "uninstall", udid, bundleID).Run()
	})
	t.Logf("app %s built and installed in %s", bundleID, time.Since(start).Round(time.Millisecond))
	return signInApp{udid: udid, bundleID: bundleID}
}

// runSignInStep launches the app (a relaunch, from the second step on) with
// one step of the driver selected, and returns what it reported, joined, plus
// the app's log.
func runSignInStep(t *testing.T, app signInApp, step string, sw *signInSwitch, server *signInServer) (string, string) {
	t.Helper()
	_ = exec.Command("xcrun", "simctl", "terminate", app.udid, app.bundleID).Run()

	launch := exec.Command("xcrun", "simctl", "launch", "--console-pty", app.udid, app.bundleID)
	launch.Env = append(os.Environ(), "SIMCTL_CHILD_MAR_SIGNIN_CHECK="+step)
	devServer, askedBefore := "", 0
	if sw != nil {
		devServer, askedBefore = sw.dev, len(sw.baked.asked())
		launch.Env = append(launch.Env, "SIMCTL_CHILD_MAR_SIGNIN_SWITCH="+devServer)
	}
	stdout, err := launch.StdoutPipe()
	if err != nil {
		t.Fatalf("wiring the app's stdout: %v", err)
	}
	launch.Stderr = launch.Stdout
	if err := launch.Start(); err != nil {
		t.Fatalf("simctl launch: %v", err)
	}
	defer func() {
		_ = exec.Command("xcrun", "simctl", "terminate", app.udid, app.bundleID).Run()
		_ = launch.Process.Kill()
		_, _ = launch.Process.Wait()
	}()

	email := ""
	if rest, ok := strings.CutPrefix(step, "sign-in:"); ok {
		email = rest
	}
	type result struct {
		reported []string
		log      string
		problem  string
	}
	done := make(chan result, 1)
	go func() {
		var r result
		var log strings.Builder
		lines := bufio.NewReader(stdout)
		for {
			raw, err := lines.ReadString('\n')
			log.WriteString(raw)
			line := strings.TrimSpace(raw)
			// Any server but the one this step chose, and the answer would
			// be about some other server.
			if _, at, ok := strings.Cut(line, "connected to mar dev at "); ok {
				if devServer == "" || !strings.HasPrefix(at, devServer) {
					r.problem = "the app adopted a server this check did not hand it: " + line
				}
			}
			if after, ok := strings.CutPrefix(line, "[mar] SIGNIN "); ok {
				switch {
				case after == "DONE":
					r.log = log.String()
					done <- r
					return
				case after == "NEED-CODE":
					if p := handOverCode(app, server, email); p != "" {
						r.problem = p
					}
				case after == "NEED-ASKED" && sw != nil:
					if p := handOverAsked(app, sw.baked, askedBefore); p != "" {
						r.problem = p
					}
				default:
					r.reported = append(r.reported, after)
				}
			}
			if err != nil {
				r.log = log.String()
				done <- r
				return
			}
		}
	}()

	select {
	case r := <-done:
		if r.problem != "" {
			t.Fatalf("%s\n\napp log:\n%s", r.problem, tailLines(r.log, 25))
		}
		if len(r.reported) == 0 {
			t.Fatalf("the app reported nothing.\n%s", tailLines(r.log, 30))
		}
		return strings.Join(r.reported, " | "), r.log
	case <-time.After(150 * time.Second):
		t.Fatalf("timed out waiting for the sign-in driver (step %q)", step)
		return "", ""
	}
}

// handOverCode reads the code `mar dev` printed for email and writes it where
// the driver is waiting for it: a file in the app's own container. Returns a
// problem, or "".
func handOverCode(app signInApp, server *signInServer, email string) string {
	var code string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if code = server.code(email); code != "" {
			break
		}
	}
	if code == "" {
		return "the app asked for a code and mar dev printed none for " + email + ":\n" + tailLines(server.log(), 20)
	}
	return writeToApp(app, "mar-signin-code", code)
}

// handOverAsked tells the driver the protected page's question has reached
// the baked URL, once it has: a request under /_auth/ this launch made.
func handOverAsked(app signInApp, baked *silentServer, before int) string {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		for _, req := range baked.asked()[before:] {
			if strings.HasPrefix(req, "GET /_auth/") {
				return writeToApp(app, "mar-signin-asked", req)
			}
		}
	}
	return fmt.Sprintf("the protected page's question never reached the baked URL (it saw %v)", baked.asked()[before:])
}

// writeToApp writes a file where the driver looks for what the test hands it:
// the app's own container, which only this app reads.
func writeToApp(app signInApp, name, content string) string {
	out, err := exec.Command("xcrun", "simctl", "get_app_container", app.udid, app.bundleID, "data").Output()
	if err != nil {
		return "finding the app's container: " + err.Error()
	}
	tmp := filepath.Join(strings.TrimSpace(string(out)), "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err.Error()
	}
	if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
		return err.Error()
	}
	return ""
}

package jsserve

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mar/internal/runtime"
)

// A panic after a response started cannot become a 500: the status is gone.
// recoverPanic used to write anyway, appending "internal server error" to the
// body and letting the response finish as a 200. Now it sends what was
// written and breaks the connection: the client gets the response's start
// and then fails to read the rest.
func TestAPanicAfterTheResponseStartedBreaksIt(t *testing.T) {
	for _, size := range []int{100, 64 << 10} {
		for _, gz := range []bool{false, true} {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(bytes.Repeat([]byte("7"), size))
				w.(http.Flusher).Flush()
				panic("boom after the first byte")
			}
			var h http.Handler = http.HandlerFunc(handler)
			if gz {
				h = http.HandlerFunc(withCompression(handler))
			}
			srv := httptest.NewServer(recoverPanic(h))
			req, _ := http.NewRequest("GET", srv.URL, nil)
			if gz {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
			resp, err := client.Do(req)
			if err != nil {
				// Broken, but in the wrong way: what was written has to
				// reach the client first. Given nothing, a browser sends
				// the request again (a POST ran two or three times,
				// measured), so its side effects run again.
				t.Errorf("%d bytes, gzip %v: the client got no response at all: %v", size, gz, err)
				srv.Close()
				continue
			}
			var body io.Reader = resp.Body
			if gz && resp.Header.Get("Content-Encoding") == "gzip" {
				zr, zerr := gzip.NewReader(resp.Body)
				if zerr != nil {
					resp.Body.Close()
					srv.Close()
					continue // a stream that does not even start is broken
				}
				// One member only: the member itself has to be cut. A
				// finished gzip stream over a cut body is a complete,
				// shorter document to a client that stops at the member's
				// end (URLSession over HTTP/1.1 does, measured), whatever
				// the connection does after it.
				zr.Multistream(false)
				body = zr
			}
			got, readErr := io.ReadAll(body)
			resp.Body.Close()
			srv.Close()
			if readErr == nil {
				t.Errorf("%d bytes, gzip %v: the client read a complete %d response of %d bytes, ending %q",
					size, gz, resp.StatusCode, len(got), tail(got))
			}
			if bytes.Contains(got, []byte("internal server error")) {
				t.Errorf("%d bytes, gzip %v: the error text was written into a started response", size, gz)
			}
		}
	}
}

// Before anything went out, the 500 is clean: the handler's headers describe
// a response that will not be sent, so they go, all but the version headers
// outer middleware set.
func TestAPanicBeforeTheResponseIsACleanFiveHundred(t *testing.T) {
	stamp := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Mar-Program", "abc123")
			next.ServeHTTP(w, r)
		})
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1234")
		panic("boom before the first byte")
	}
	for _, gz := range []bool{false, true} {
		var inner http.Handler = http.HandlerFunc(handler)
		if gz {
			inner = http.HandlerFunc(withCompression(handler))
		}
		srv := httptest.NewServer(recoverPanic(stamp(inner)))
		req, _ := http.NewRequest("GET", srv.URL, nil)
		if gz {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("gzip %v: %v", gz, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		srv.Close()
		if readErr != nil {
			t.Errorf("gzip %v: reading the 500: %v", gz, readErr)
		}
		if resp.StatusCode != http.StatusInternalServerError || string(body) != "internal server error" {
			t.Errorf("gzip %v: %d %q, want 500 \"internal server error\"", gz, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Errorf("gzip %v: Content-Type %q on a plain-text error", gz, ct)
		}
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Errorf("gzip %v: Content-Encoding %q on a plain-text error", gz, enc)
		}
		if p := resp.Header.Get("X-Mar-Program"); p != "abc123" {
			t.Errorf("gzip %v: X-Mar-Program %q, want the version header kept", gz, p)
		}
	}
}

// The request log looks up who a request belonged to after the response is
// complete, on every signed-in request. A panic there used to reach
// recoverPanic and corrupt a response that was already right. Now it is
// logged, the response stays whole, and with the log off nothing is looked
// up at all.
func TestAPanicLoggingARequestLeavesItsResponseAlone(t *testing.T) {
	auth, cleanup := authTestServer(t)
	defer cleanup()
	client := auth.Client()
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, auth.URL+"/_auth/request-code", map[string]string{"email": "log@example.com"})
	})
	resp, _ := postJSON(t, client, auth.URL+"/_auth/verify-code",
		map[string]any{"email": "log@example.com", "code": extractSinkCode(t, out)})
	tok := resp.Header.Get(bearerTokenHeader)
	if tok == "" {
		t.Fatal("no session token")
	}

	looked := 0
	restoreLoad := userForRequestLog
	userForRequestLog = func(runtime.VAuth, int64) (runtime.Value, error) {
		looked++
		panic("boom after the response")
	}
	defer func() { userForRequestLog = restoreLoad }()
	restoreLogger := requestLogger
	defer func() { requestLogger = restoreLogger }()

	mux := http.NewServeMux()
	mux.HandleFunc("/thing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	srv := httptest.NewServer(recoverPanic(adminInstrument(mux)))
	defer srv.Close()
	get := func() (int, string, error) {
		req, _ := http.NewRequest("GET", srv.URL+"/thing", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := srv.Client().Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), err
	}

	SetAdminRequestBufferSize(16)
	status, body, err := get()
	if err != nil || status != http.StatusOK || body != `{"ok":true}` {
		t.Errorf("with the log on: %d %q %v, want 200 {\"ok\":true}", status, body, err)
	}
	if looked != 1 {
		t.Fatalf("the user lookup ran %d times; the panic this test is about never happened", looked)
	}

	SetAdminRequestBufferSize(0)
	looked = 0
	status, body, err = get()
	if err != nil || status != http.StatusOK || body != `{"ok":true}` {
		t.Errorf("with the log off: %d %q %v", status, body, err)
	}
	if looked != 0 {
		t.Errorf("with the log off the user was still looked up %d times", looked)
	}
}

// The reload stream lives behind recoverPanic's writer, asserts http.Flusher,
// and clears its write deadline through ResponseController. A writer that hid
// either broke it: no Flush answered 500 "streaming unsupported", and no
// Unwrap let the server's WriteTimeout cut the stream, losing the next event.
func TestTheReloadStreamOutlivesTheWriteTimeoutBehindRecoverPanic(t *testing.T) {
	hub := NewReloadHub(nil)
	srv := httptest.NewUnstartedServer(recoverPanic(http.HandlerFunc(hub.ServeReload)))
	srv.Config.WriteTimeout = 300 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want the stream", resp.StatusCode)
	}
	go func() {
		time.Sleep(900 * time.Millisecond) // three write timeouts later
		hub.Reload()
	}()
	lines := bufio.NewScanner(resp.Body)
	deadline := time.After(5 * time.Second)
	got := make(chan string, 1)
	go func() {
		for lines.Scan() {
			if strings.HasPrefix(lines.Text(), "data: ") {
				got <- lines.Text()
				return
			}
		}
		got <- "stream ended: " + errText(lines.Err())
	}()
	select {
	case line := <-got:
		if !strings.Contains(line, "reload") {
			t.Errorf("after the write timeout the stream gave %q, want the reload event", line)
		}
	case <-deadline:
		t.Fatal("no event within 5 s")
	}
}

// writeJSON encodes before the status goes out, so a value that does not
// encode is a 500 rather than its status with an empty body, and every other
// value goes out byte for byte as json.Encoder wrote it.
func TestWriteJSONEncodesBeforeItAnswers(t *testing.T) {
	bad := httptest.NewRecorder()
	writeJSON(bad, http.StatusOK, make(chan int))
	if bad.Code != http.StatusInternalServerError {
		t.Errorf("a value that does not encode answered %d %q", bad.Code, bad.Body.String())
	}
	good := httptest.NewRecorder()
	v := map[string]any{"error": "<rate_limited>", "n": 3}
	writeJSON(good, http.StatusTooManyRequests, v)
	var want bytes.Buffer
	_ = json.NewEncoder(&want).Encode(v)
	if good.Code != http.StatusTooManyRequests || good.Body.String() != want.String() {
		t.Errorf("got %d %q, want 429 %q", good.Code, good.Body.String(), want.String())
	}
}

func tail(b []byte) string {
	if len(b) > 24 {
		b = b[len(b)-24:]
	}
	return string(b)
}

func errText(err error) string {
	if err == nil {
		return "EOF"
	}
	return err.Error()
}

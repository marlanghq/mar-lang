package jsserve

import "net/http/cookiejar"

// cookieJar wraps net/http/cookiejar.New so the tests don't need to
// import that package directly (keeps the main test file's imports
// light).
func cookieJar() (*cookiejar.Jar, error) {
	return cookiejar.New(nil)
}

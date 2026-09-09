package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Describe being right is half of it; the editor reaches it through the
// protocol, and a handler that is written and not routed answers nothing while
// every unit test passes. Today's lesson, in the same shape as the hover one:
// the lookup can be correct and the wiring still drop it.
//
// So this drives the server the way the extension does -- a framed JSON-RPC
// request in, a framed response out.
func TestTheServerAnswersMarDescribeOverTheProtocol(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":7,"method":"mar/describe","params":{"name":"Decimal.toCents"}}`
	var out bytes.Buffer
	s := &Server{
		in:   bufio.NewReader(strings.NewReader("")),
		out:  &out,
		docs: map[string]string{},
		idx:  map[string]*DocIndex{},
	}
	var msg message
	if err := json.Unmarshal([]byte(req), &msg); err != nil {
		t.Fatal(err)
	}
	s.handle(&msg)

	body := out.String()
	if body == "" {
		t.Fatal("mar/describe produced no response: the method is not routed")
	}
	i := strings.Index(body, "\r\n\r\n")
	if i < 0 {
		t.Fatalf("response is not LSP-framed:\n%s", body)
	}
	var resp struct {
		ID     int            `json:"id"`
		Result describeResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(body[i+4:]), &resp); err != nil {
		t.Fatalf("response body: %v\n%s", err, body[i+4:])
	}
	if resp.ID != 7 {
		t.Errorf("answered request %d, asked %d", resp.ID, 7)
	}
	if resp.Result.Exact == nil {
		t.Fatal("no exact answer came back over the protocol")
	}
	if resp.Result.Exact.Type != "Decimal -> Int" {
		t.Errorf("type came back as %q", resp.Result.Exact.Type)
	}
	if len(resp.Result.Exact.Examples) == 0 {
		t.Error("the examples did not survive the round trip, " +
			"and they are the half that makes the answer a checked fact")
	}
}

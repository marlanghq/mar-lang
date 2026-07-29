package main

import (
	"errors"
	"testing"
)

// topologyNeedsDB decides whether `mar dev` provisions a SQLite database.
// The regression it guards: a frontend app whose main has a transient
// compile error (topology unknown) used to fall through to provisioning
// and leak a stray <name>.db / .db.lock next to the source. Only a KNOWN
// server app should ever provision.
func TestTopologyNeedsDB(t *testing.T) {
	boom := errors.New("type error: unknown identifier")
	cases := []struct {
		name string
		topo string
		err  error
		want bool
	}{
		{"frontend gets no db", "frontend", nil, false},
		{"backend provisions", "backend", nil, true},
		{"fullstack provisions", "fullstack", nil, true},
		{"unknown topology (compile error) provisions nothing", "", boom, false},
		{"error wins even with a stale topo string", "backend", boom, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := topologyNeedsDB(c.topo, c.err); got != c.want {
				t.Errorf("topologyNeedsDB(%q, %v) = %v, want %v", c.topo, c.err, got, c.want)
			}
		})
	}
}

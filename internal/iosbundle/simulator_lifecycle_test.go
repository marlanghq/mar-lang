package iosbundle

import (
	"os"
	"os/exec"
	"sync"
	"testing"
)

// The iOS tests need a booted simulator, and nothing ever turned one off.
//
// bootedSimulator reuses a device that is already running and boots one when
// none is, which is the right behaviour and was only half a policy: a machine
// where nobody had opened Simulator.app got a headless device started by the
// first `go test ./...` and then kept it — launchd_sim, SimRenderServer, three
// SimMetalHosts — for as long as the machine stayed up. Found at 57 hours.
//
// The missing half is knowing WHOSE it is. A device this package started is
// this package's to put back; a device that was already running belongs to
// whoever was using it, and shutting that one down would take their simulator
// out from under them mid-session. That is the whole distinction here, and it
// is why the shutdown is conditional rather than unconditional.
//
// It happens once per package run rather than per test, because the tests
// deliberately share one device: TestSurfaceParity alone asks for it twice.
var simulator struct {
	mu     sync.Mutex
	udid   string // non-empty only when WE booted it
	booted bool
}

// claimBootedSimulator records that this run turned a device on.
func claimBootedSimulator(udid string) {
	simulator.mu.Lock()
	defer simulator.mu.Unlock()
	if !simulator.booted {
		simulator.udid = udid
		simulator.booted = true
	}
}

// releaseBootedSimulator shuts down a device this run booted, and does
// nothing otherwise. Best effort: a simulator that will not shut down is not
// a reason to fail a green suite, and the next run reuses it either way.
func releaseBootedSimulator() {
	simulator.mu.Lock()
	udid, booted := simulator.udid, simulator.booted
	simulator.udid, simulator.booted = "", false
	simulator.mu.Unlock()
	if !booted {
		return
	}
	_ = exec.Command("xcrun", "simctl", "shutdown", udid).Run()
}

func TestMain(m *testing.M) {
	code := m.Run()
	releaseBootedSimulator()
	os.Exit(code)
}

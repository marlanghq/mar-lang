package jsserve

import (
	"fmt"
	"os"

	"github.com/grandcat/zeroconf"
)

// Bonjour service type. Native iOS clients (and any other LAN-aware
// tooling) browse for this. Keep in sync with the
// `NSBonjourServices` entry in the iOS template's Info.plist.
const bonjourServiceType = "_mar._tcp"

// publishBonjour advertises the running mar server on the local
// network via mDNS. Lets a freshly installed iOS app discover the
// backend on the same LAN without typing in an IP address.
//
// `instance` is the human-readable name shown in pickers — defaults
// to the hostname when the program title is empty (avoids two
// instances on the same LAN colliding).
//
// Returns the actual instance name registered (so the caller can
// print it in the startup banner) and an unregister function.
// Returns ("", noop) on failure — the server keeps running, just
// without LAN discovery.
func publishBonjour(instance string, port int) (string, func()) {
	if instance == "" {
		instance, _ = os.Hostname()
		if instance == "" {
			instance = "mar"
		}
	}
	server, err := zeroconf.Register(
		instance,
		bonjourServiceType,
		"local.",
		port,
		nil, // no TXT records (yet — could carry app version, schema URL, etc.)
		nil, // all interfaces
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mar] mDNS publish failed (LAN discovery disabled): %v\n", err)
		return "", func() {}
	}
	return instance, server.Shutdown
}

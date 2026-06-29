package jsserve

import (
	"net"
	"sync/atomic"
)

// proxyTrust decides whether the X-Forwarded-* headers on a request are
// believed. configured=false means mar.json set no server.trustedProxies,
// so the loopback + private-range default applies. configured=true means
// the operator set the list; nets holds the parsed CIDRs (empty = trust
// no proxy, the paranoid mode for a directly-exposed listener).
type proxyTrust struct {
	configured bool
	nets       []*net.IPNet
}

var trustedProxyCfg atomic.Pointer[proxyTrust]

// SetTrustedProxies installs the trusted-proxy policy from
// mar.json["server"]["trustedProxies"]. nil means "unset" — the default
// of loopback + RFC1918/RFC4193 private ranges. A non-nil slice (empty
// included) is authoritative: empty trusts no proxy. Invalid CIDRs are
// skipped here; the manifest validator rejects them earlier, so this is
// only the defensive net for callers that bypass validation.
func SetTrustedProxies(cidrs []string) {
	cfg := &proxyTrust{}
	if cidrs != nil {
		cfg.configured = true
		for _, c := range cidrs {
			if _, n, err := net.ParseCIDR(c); err == nil {
				cfg.nets = append(cfg.nets, n)
			}
		}
	}
	trustedProxyCfg.Store(cfg)
}

// isTrustedProxy reports whether a connecting peer is allowed to set the
// X-Forwarded-For / X-Forwarded-Proto headers the server honors. Default
// (unset config): loopback or a private address — covers reverse-proxy-
// on-host, sidecars, the Docker bridge, and private cloud networks.
// Configured: membership in one of the operator-supplied CIDRs.
func isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	cfg := trustedProxyCfg.Load()
	if cfg == nil || !cfg.configured {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	for _, n := range cfg.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// hostOnly strips the :port from a RemoteAddr ("1.2.3.4:5678" ->
// "1.2.3.4"), returning the input unchanged when there is no port.
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

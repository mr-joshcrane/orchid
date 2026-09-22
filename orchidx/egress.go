package orchidx

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/mr-joshcrane/orchid"
)

// Allowlist is the set of hosts an agent may reach. An empty or nil Allowlist
// permits nothing. Entries match case-insensitively, port ignored:
//
//	"example.com"    that host only, not its subdomains
//	"*.example.com"  any subdomain at any depth, not the bare domain
//	"10.0.0.1"       that address exactly
type Allowlist struct {
	exact  map[string]bool
	suffix []string // each stored with its leading dot, e.g. ".example.com"
}

// normalizeHost lowercases and strips space, port and IPv6 brackets.
// Both entries and lookups go through it.
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if bare, _, err := net.SplitHostPort(h); err == nil {
		h = bare
	}
	return strings.Trim(h, "[]")
}

// NewAllowlist builds an allowlist from host patterns.
func NewAllowlist(hosts ...string) *Allowlist {
	a := &Allowlist{exact: make(map[string]bool, len(hosts))}
	for _, h := range hosts {
		if rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(h)), "*."); ok {
			if rest = normalizeHost(rest); rest != "" {
				a.suffix = append(a.suffix, "."+rest)
			}
			continue
		}
		if n := normalizeHost(h); n != "" {
			a.exact[n] = true
		}
	}
	return a
}

// Permits reports whether host may be reached. host may carry a port.
func (a *Allowlist) Permits(host string) bool {
	if a == nil {
		return false
	}
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	if a.exact[h] {
		return true
	}
	for _, suffix := range a.suffix {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

func (a *Allowlist) String() string {
	if a == nil {
		return "(none)"
	}
	out := make([]string, 0, len(a.exact)+len(a.suffix))
	for h := range a.exact {
		out = append(out, h)
	}
	for _, s := range a.suffix {
		out = append(out, "*"+s)
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, ", ")
}

// EgressPolicy refuses a call whose arguments name an http or https host outside
// the allowlist. It inspects arguments only, so it is not a perimeter — a
// handler that ignores its arguments is unconstrained by it. Use Transport to
// enforce; use this to record the attempt in the event log.
func EgressPolicy(allowed *Allowlist) orchid.EffectPolicy {
	return func(p *orchid.Process, e *orchid.Effect) (bool, string) {
		for name, value := range e.Args {
			host, ok := httpHost(value)
			if !ok {
				continue
			}
			if !allowed.Permits(host) {
				return false, fmt.Sprintf(
					"egress denied: argument %q names host %q, which is not in the allowlist (%s)",
					name, host, allowed,
				)
			}
		}
		return true, ""
	}
}

// httpHost returns the host of an http or https URL.
func httpHost(value string) (string, bool) {
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return "", false
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return "", false
	}
	return u.Host, true
}

// ErrEgressDenied is returned by Transport for a request outside the allowlist.
var ErrEgressDenied = fmt.Errorf("orchidx: egress denied")

// Transport wraps base so requests outside the allowlist fail before connecting.
// A nil base uses http.DefaultTransport.
func Transport(allowed *Allowlist, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &allowlistTransport{allowed: allowed, base: base}
}

type allowlistTransport struct {
	allowed *Allowlist
	base    http.RoundTripper
}

func (t *allowlistTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if !t.allowed.Permits(host) {
		return nil, fmt.Errorf("%w: %s is not in the allowlist (%s)", ErrEgressDenied, host, t.allowed)
	}
	return t.base.RoundTrip(req)
}

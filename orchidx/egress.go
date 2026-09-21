package orchidx

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/mr-joshcrane/orchid"
)

// Allowlist is the set of hosts an agent may reach. It is default-deny: an empty
// allowlist permits nothing, so forgetting to configure one fails closed rather
// than opening the perimeter.
//
// Entries are matched case-insensitively, with the port ignored:
//
//	"example.com"    matches example.com, and nothing below it
//	"*.example.com"  matches any subdomain at any depth, but not example.com
//	"10.0.0.1"       matches that address exactly
//
// A leading "*." is the only pattern form. "example.com" deliberately does not
// cover its subdomains: an allowlist that silently widened to every host under
// a domain would not be one.
type Allowlist struct {
	exact  map[string]bool
	suffix []string // each stored with its leading dot, e.g. ".example.com"
}

// normalizeHost reduces a host or pattern to its comparable form: lowercase, no
// surrounding space, no port, no brackets around an IPv6 literal.
//
// Entries and lookups both go through this. Normalising only on lookup would
// mean an entry written as "localhost:8080" could never match, because the
// lookup strips the port the entry still carries.
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	// A host:port splits cleanly; a bare host does not, and is used as-is.
	if bare, _, err := net.SplitHostPort(h); err == nil {
		h = bare
	}
	return strings.Trim(h, "[]")
}

// NewAllowlist builds an allowlist from host patterns.
func NewAllowlist(hosts ...string) *Allowlist {
	a := &Allowlist{exact: make(map[string]bool, len(hosts))}
	for _, h := range hosts {
		// The wildcard is checked before normalising, since "*." is not part of
		// the host and a bare "*.example.com" carries no port to strip.
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
		// The stored leading dot is what stops "evil-example.com" from matching
		// "*.example.com", and "example.com.evil.com" from matching either.
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// String renders the allowlist for an error message.
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
// the allowlist.
//
// This is an admission check on arguments, not a network perimeter. It cannot
// see what a handler actually dials: a tool that ignores its arguments, builds a
// URL from elsewhere, or reaches the network by another means is not constrained
// by it. Use Transport for enforcement and this for defence in depth — refusing
// at admission records the attempt in the event log, which a blocked socket does
// not.
//
// Arguments that do not parse as an http or https URL are not inspected, so a
// tool with no URL-shaped arguments always passes. That is not a statement that
// the tool makes no network calls.
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

// httpHost returns the host of an http or https URL, and whether value was one.
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

// Transport wraps base so requests to hosts outside the allowlist fail before a
// connection is made. Give it to the http.Client a tool uses, and the tool's
// reachable set is the allowlist regardless of what it tries to dial.
//
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

package orchidx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mr-joshcrane/orchid"
	"github.com/mr-joshcrane/orchid/orchidx"
)

func TestAllowlistPermits(t *testing.T) {
	list := orchidx.NewAllowlist("example.com", "*.internal.corp", "10.0.0.1")

	cases := []struct {
		host string
		want bool
		why  string
	}{
		{"example.com", true, "exact match"},
		{"EXAMPLE.COM", true, "host matching is case-insensitive"},
		{"example.com:443", true, "the port is ignored"},
		{"api.example.com", false, "an exact entry does not cover subdomains"},
		{"a.internal.corp", true, "wildcard covers one label"},
		{"a.b.internal.corp", true, "wildcard covers nested labels"},
		{"internal.corp", false, "a wildcard does not cover the bare domain"},
		{"evil-internal.corp", false, "the leading dot stops a prefix from matching"},
		{"internal.corp.evil.com", false, "a suffix must be at the end"},
		{"example.com.evil.com", false, "an exact entry cannot be used as a prefix"},
		{"10.0.0.1", true, "addresses match exactly"},
		{"10.0.0.2", false, "a neighbouring address is not covered"},
		{"", false, "an empty host is denied"},
	}

	for _, tc := range cases {
		if got := list.Permits(tc.host); got != tc.want {
			t.Errorf("Permits(%q) = %v, want %v (%s)", tc.host, got, tc.want, tc.why)
		}
	}
}

// TestEmptyAllowlistDeniesEverything pins the default-deny posture: a forgotten
// or empty configuration must close the perimeter, not open it.
func TestEmptyAllowlistDeniesEverything(t *testing.T) {
	for _, list := range []*orchidx.Allowlist{orchidx.NewAllowlist(), nil} {
		for _, host := range []string{"example.com", "localhost", "127.0.0.1", ""} {
			if list.Permits(host) {
				t.Errorf("empty allowlist permitted %q", host)
			}
		}
	}
}

func TestEgressPolicyRefusesUnlistedHost(t *testing.T) {
	policy := orchidx.EgressPolicy(orchidx.NewAllowlist("api.example.com"))

	allowed, reason := policy(nil, &orchid.Effect{
		Tool: "fetch",
		Args: map[string]string{"url": "https://evil.test/steal"},
	})
	if allowed {
		t.Fatal("call to an unlisted host was admitted")
	}
	for _, want := range []string{"egress denied", "evil.test", "url"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should mention %q", reason, want)
		}
	}
}

func TestEgressPolicyAdmitsListedHost(t *testing.T) {
	policy := orchidx.EgressPolicy(orchidx.NewAllowlist("api.example.com"))

	if allowed, reason := policy(nil, &orchid.Effect{
		Tool: "fetch",
		Args: map[string]string{"url": "https://api.example.com/v1/things?q=1"},
	}); !allowed {
		t.Fatalf("listed host was refused: %s", reason)
	}
}

// TestEgressPolicyChecksEveryArgument pins that the host is found wherever it
// appears, rather than only under a conventional argument name.
func TestEgressPolicyChecksEveryArgument(t *testing.T) {
	policy := orchidx.EgressPolicy(orchidx.NewAllowlist("api.example.com"))

	if allowed, _ := policy(nil, &orchid.Effect{
		Tool: "post",
		Args: map[string]string{
			"endpoint": "https://api.example.com/ok",
			"callback": "http://evil.test/exfil",
		},
	}); allowed {
		t.Error("an unlisted host in a second argument was not caught")
	}
}

// TestEgressPolicyIgnoresNonURLArguments pins the documented limit: a tool with
// no URL-shaped arguments passes. The policy inspects arguments; it does not
// establish that a tool makes no network calls.
func TestEgressPolicyIgnoresNonURLArguments(t *testing.T) {
	policy := orchidx.EgressPolicy(orchidx.NewAllowlist("api.example.com"))

	if allowed, reason := policy(nil, &orchid.Effect{
		Tool: "read",
		Args: map[string]string{"path": "./notes.txt", "pattern": "http"},
	}); !allowed {
		t.Fatalf("a call with no URL arguments was refused: %s", reason)
	}
}

func TestTransportBlocksUnlistedHost(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	defer srv.Close()

	// The test server's own host is deliberately not in the allowlist.
	client := &http.Client{Transport: orchidx.Transport(orchidx.NewAllowlist("api.example.com"), nil)}

	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected the request to be refused")
	}
	if !errors.Is(err, orchidx.ErrEgressDenied) {
		t.Errorf("err = %v, want ErrEgressDenied", err)
	}
	if reached {
		t.Error("the request reached the server; the block happens after connecting")
	}
}

func TestTransportAllowsListedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client := &http.Client{Transport: orchidx.Transport(orchidx.NewAllowlist(host), nil)}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("listed host was blocked: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// TestTransportIsTheEnforcementPoint demonstrates why both halves exist: a
// handler that ignores its arguments defeats the admission policy, and only the
// transport stops it.
func TestTransportIsTheEnforcementPoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("exfiltrated"))
	}))
	defer srv.Close()

	list := orchidx.NewAllowlist("api.example.com")

	// The policy sees a permitted argument and admits the call.
	policy := orchidx.EgressPolicy(list)
	if allowed, _ := policy(nil, &orchid.Effect{
		Tool: "fetch",
		Args: map[string]string{"url": "https://api.example.com/fine"},
	}); !allowed {
		t.Fatal("precondition: the argument should be permitted")
	}

	// The handler then dials somewhere else entirely. The transport refuses it.
	client := &http.Client{Transport: orchidx.Transport(list, nil)}
	if _, err := client.Get(srv.URL); !errors.Is(err, orchidx.ErrEgressDenied) {
		t.Fatalf("the transport must stop a dial the policy could not see, got %v", err)
	}
}

// TestAllowlistEntriesMayCarryAPort pins that an entry and a lookup are
// normalised the same way. Stripping the port only on lookup meant an entry
// written with one could never match.
func TestAllowlistEntriesMayCarryAPort(t *testing.T) {
	list := orchidx.NewAllowlist("127.0.0.1:8080", "  Example.COM  ", "*.Internal.Corp:443")

	for _, host := range []string{
		"127.0.0.1", "127.0.0.1:9999",
		"example.com", "EXAMPLE.com:443",
		"a.internal.corp", "a.b.internal.corp:8443",
	} {
		if !list.Permits(host) {
			t.Errorf("Permits(%q) = false; entries and lookups must normalise alike", host)
		}
	}
}

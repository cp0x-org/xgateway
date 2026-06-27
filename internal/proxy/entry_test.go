package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

// TestBuildUpstreamProxyRewritesAbsoluteRedirectToPublicOrigin guards against a
// regression where MPP payment clients refuse to follow a redirect: they treat
// it as a cross-origin leak of payment credentials. If the upstream issues an
// absolute redirect back to its own host (e.g. an http->https canonicalisation
// redirect), the proxy must rewrite Location to the gateway's public host, or
// paid retries fail with "refusing cross-origin redirect".
func TestBuildUpstreamProxyRewritesAbsoluteRedirectToPublicOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+r.Host+"/health")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer upstream.Close()

	rule := &rules.Rule{InboundPath: "/health", Target: upstream.URL}
	rp, err := buildUpstreamProxy(rule, "/health")
	if err != nil {
		t.Fatalf("buildUpstreamProxy() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://xpaywall-gate.example.com/health", nil)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	want := "http://xpaywall-gate.example.com/health"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// TestBuildUpstreamProxyInjectsAuthHeader verifies that a route configured with
// an auth header (from project_routes_settings via control-api, or the file
// provider's outbound.auth_header) has that header injected on the upstream
// request. This is what lets an operator put xgateway in front of an API that
// itself requires a bearer token: clients pay the gateway, the gateway adds the
// upstream credential.
func TestBuildUpstreamProxyInjectsAuthHeader(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	rule := &rules.Rule{
		InboundPath:     "/protected",
		Target:          upstream.URL,
		AuthHeaderName:  "Authorization",
		AuthHeaderValue: "Bearer demo-secret-token",
	}
	rp, err := buildUpstreamProxy(rule, "/protected")
	if err != nil {
		t.Fatalf("buildUpstreamProxy() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://xpaywall-gate.example.com/protected", nil)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	if want := "Bearer demo-secret-token"; gotAuth != want {
		t.Fatalf("upstream Authorization = %q, want %q", gotAuth, want)
	}
}

// TestBuildUpstreamProxyOmitsAuthHeaderWhenUnset ensures the gateway forwards
// the request untouched when no auth header is configured (the common case),
// rather than injecting an empty header.
func TestBuildUpstreamProxyOmitsAuthHeaderWhenUnset(t *testing.T) {
	var hasAuth bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	rule := &rules.Rule{InboundPath: "/open", Target: upstream.URL}
	rp, err := buildUpstreamProxy(rule, "/open")
	if err != nil {
		t.Fatalf("buildUpstreamProxy() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://xpaywall-gate.example.com/open", nil)
	rp.ServeHTTP(httptest.NewRecorder(), req)

	if hasAuth {
		t.Fatal("upstream received an Authorization header, want none")
	}
}

// TestBuildUpstreamProxyLeavesThirdPartyRedirectUntouched ensures the rewrite
// above is scoped to the upstream's own host: redirects to an unrelated host
// (e.g. an OAuth provider) must pass through unchanged.
func TestBuildUpstreamProxyLeavesThirdPartyRedirectUntouched(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://other.example.com/login")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	rule := &rules.Rule{InboundPath: "/health", Target: upstream.URL}
	rp, err := buildUpstreamProxy(rule, "/health")
	if err != nil {
		t.Fatalf("buildUpstreamProxy() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://xpaywall-gate.example.com/health", nil)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	want := "https://other.example.com/login"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

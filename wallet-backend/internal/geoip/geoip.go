// Package geoip resolves an IP address to a country code, feeding the
// registration risk fields described in PLAN.md §4.13 ("ipapi integration
// feeding registration risk fields"). Kept as a small interface with a
// free/local default (NoopProvider), matching this port's established
// pattern for optional vendor integrations (internal/kyc, internal/fiat) -
// swap in a real provider by implementing Provider.
package geoip

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider resolves an IP address to an ISO 3166-1 alpha-2 country code.
// An empty result with a nil error means "unknown" (e.g. a private/
// loopback address in local development) - callers must treat that as
// "no signal," never as a specific country.
type Provider interface {
	Lookup(ctx context.Context, ip string) (countryCode string, err error)
}

// NoopProvider always reports "unknown" - the default when no geo-IP
// vendor is configured, so registration works identically with or without
// one (the risk fields it would have populated simply stay empty).
type NoopProvider struct{}

func NewNoopProvider() Provider { return NoopProvider{} }

func (NoopProvider) Lookup(context.Context, string) (string, error) { return "", nil }

// IPAPIProvider looks up a country code via ipapi.co's free plain-text
// endpoint (GET {baseURL}/{ip}/country/ -> a bare 2-letter code). No API
// key is required on the free tier; pass a different baseURL to point at
// a paid/self-hosted equivalent with the same path shape.
type IPAPIProvider struct {
	baseURL    string
	httpClient *http.Client
}

func NewIPAPIProvider(baseURL string) Provider {
	return &IPAPIProvider{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (p *IPAPIProvider) Lookup(ctx context.Context, ip string) (string, error) {
	if ip == "" || isPrivateOrLoopback(ip) {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s/country/", p.baseURL, ip), nil)
	if err != nil {
		return "", err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("geoip: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16))
	if err != nil {
		return "", err
	}
	code := strings.ToUpper(strings.TrimSpace(string(body)))
	if len(code) != 2 {
		// A vendor error page or rate-limit notice, not a country code -
		// treat as "unknown" rather than propagating vendor-specific text.
		return "", nil
	}
	return code, nil
}

// isPrivateOrLoopback reports whether ip is obviously not a public
// address (127.0.0.1, ::1, a bare empty string from a stripped proxy
// header) - looking these up would either fail or return the geo-IP
// vendor's own location, neither useful for a registration risk signal.
func isPrivateOrLoopback(ip string) bool {
	switch {
	case ip == "127.0.0.1", ip == "::1", ip == "localhost":
		return true
	case strings.HasPrefix(ip, "10."), strings.HasPrefix(ip, "192.168."), strings.HasPrefix(ip, "172."):
		return true
	default:
		return false
	}
}

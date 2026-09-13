package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoopProvider_AlwaysReturnsUnknown(t *testing.T) {
	provider := NewNoopProvider()
	code, err := provider.Lookup(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != "" {
		t.Fatalf("expected an empty country code, got %q", code)
	}
}

func TestIPAPIProvider_ParsesCountryCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("NG\n"))
	}))
	defer server.Close()

	provider := NewIPAPIProvider(server.URL)
	code, err := provider.Lookup(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if code != "NG" {
		t.Fatalf("expected NG, got %q", code)
	}
}

func TestIPAPIProvider_SkipsPrivateAddresses(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("US"))
	}))
	defer server.Close()

	provider := NewIPAPIProvider(server.URL)
	code, err := provider.Lookup(context.Background(), "192.168.1.1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if code != "" {
		t.Fatalf("expected an empty country code for a private address, got %q", code)
	}
	if called {
		t.Fatalf("expected the provider to never call out for a private address")
	}
}

func TestIPAPIProvider_TreatsNonCodeResponseAsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Rate limit exceeded"))
	}))
	defer server.Close()

	provider := NewIPAPIProvider(server.URL)
	code, err := provider.Lookup(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatalf("expected no error for a vendor error page, got %v", err)
	}
	if code != "" {
		t.Fatalf("expected an empty country code for a non-code response, got %q", code)
	}
}

func TestIPAPIProvider_PropagatesHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewIPAPIProvider(server.URL)
	if _, err := provider.Lookup(context.Background(), "8.8.8.8"); err == nil {
		t.Fatalf("expected an error for a non-200 response")
	}
}

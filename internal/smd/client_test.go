package smd

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenCHAMI/kea-sync/internal/httpjson"
)

func newIPv4Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable in this environment: %v", err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

func TestProbeUsesServiceReadyEndpoint(t *testing.T) {
	t.Parallel()

	server := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got, want := r.URL.Path, "/hsm/v2/service/ready"; got != want {
			t.Fatalf("unexpected path: got %q want %q", got, want)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"message":"HSM is healthy"}`))
	}))
	defer server.Close()

	client := NewClient(httpjson.NewClient(httpjson.Config{BaseURL: server.URL}))

	if err := client.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestProbeReturnsServiceReadyFailures(t *testing.T) {
	t.Parallel()

	server := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":503}`))
	}))
	defer server.Close()

	client := NewClient(httpjson.NewClient(httpjson.Config{BaseURL: server.URL}))

	if err := client.Probe(context.Background()); err == nil {
		t.Fatal("Probe() error = nil, want non-nil")
	}
}

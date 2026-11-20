package pprofserver

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAddrFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "disabled", value: "", want: ""},
		{name: "blank", value: "   ", want: ""},
		{name: "loopback", value: " 127.0.0.1:6060 ", want: "127.0.0.1:6060"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := AddrFromEnv(func(key string) string {
				if key != EnvAddr {
					t.Fatalf("getenv called with %q, want %q", key, EnvAddr)
				}
				return test.value
			})
			if got != test.want {
				t.Fatalf("AddrFromEnv() = %q, want %q", got, test.want)
			}
		})
	}
	if got := AddrFromEnv(nil); got != "" {
		t.Fatalf("AddrFromEnv(nil) = %q, want empty", got)
	}
}

func TestStartServesHeapProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	response, err := http.Get("http://" + addr.String() + "/debug/pprof/heap?debug=1")
	if err != nil {
		t.Fatalf("GET heap profile: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("heap profile status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read heap profile: %v", err)
	}
	if !strings.Contains(string(body), "heap profile") {
		t.Fatalf("heap profile body = %q, want a heap profile header", string(body))
	}
}

func TestStartRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{"", "0.0.0.0:0", "192.0.2.1:6060", ":6060", "localhost:6060", "not-an-address"} {
		t.Run(addr, func(t *testing.T) {
			if _, err := Start(context.Background(), addr); err == nil {
				t.Fatalf("Start(%q) error = nil, want a rejection", addr)
			}
		})
	}
}

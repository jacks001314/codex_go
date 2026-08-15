package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecordProtocolDiscoveryMetricsObserverLikeRust(t *testing.T) {
	var captured ProtocolDiscoveryMetrics
	SetProtocolDiscoveryMetricsObserver(func(metrics ProtocolDiscoveryMetrics) {
		captured = metrics
	})
	defer SetProtocolDiscoveryMetricsObserver(nil)

	recordProtocolDiscoveryMetrics("auto", "modern", 1250*time.Millisecond)
	if captured.Mode != "auto" || captured.Outcome != "modern" || captured.DurationMS != 1250 {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestHTTPClientInitializeRecordsProtocolDiscoveryMetricsLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method == "initialize" {
			w.Header().Set(mcpHTTPSessionIDHeader, "session-metrics")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": modernMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "metrics", "version": "test"},
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	var captured ProtocolDiscoveryMetrics
	SetProtocolDiscoveryMetricsObserver(func(metrics ProtocolDiscoveryMetrics) {
		captured = metrics
	})
	defer SetProtocolDiscoveryMetricsObserver(nil)

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocolLegacy})
	if _, err := client.initialize(context.Background()); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}
	if captured.Mode != "legacy" || captured.Outcome != "modern" {
		t.Fatalf("captured = %#v, want mode=legacy outcome=modern", captured)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer failing.Close()
	client = newMCPHTTPClient(&ServerConfig{URL: failing.URL, ProtocolMode: MCPProtocol20260728})
	if _, err := client.initialize(context.Background()); err == nil {
		t.Fatal("initialize() error = nil, want remote error")
	}
	if captured.Mode != "auto" || captured.Outcome != "failure" {
		t.Fatalf("captured failure = %#v, want mode=auto outcome=failure", captured)
	}
}

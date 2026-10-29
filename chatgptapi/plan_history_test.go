package chatgptapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudClientPlanLimitHistory404IsNoReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/usage/plan_limit_history" || r.URL.Query().Get("days") != "7" {
			t.Fatalf("request = %s, want GET /api/codex/usage/plan_limit_history?days=7", r.URL.String())
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	history, err := client.GetPlanLimitHistory(context.Background())
	if err != nil || history != nil {
		t.Fatalf("GetPlanLimitHistory() = (%#v, %v), want (nil, nil)", history, err)
	}
}

func TestCloudClientPlanLimitHistoryDecodesUnknownDimensionAndApproximateDefault(t *testing.T) {
	const payload = `{"data_as_of":"2026-09-15T18:44:59Z","coverage_complete":true,"periods":[` +
		`{"id":"p1","window_minutes":300,"plan_type":"plus","starts_at":"2026-09-15T12:00:00Z",` +
		`"ends_at":"2026-09-15T17:00:00Z","accounting_complete":true,"used_basis_points":4250,` +
		`"breakdowns":[{"dimension":"model","rows":[{"key":"gpt-6.1-sol","basis_points":3000}]},` +
		`{"dimension":"new_dimension","rows":[{"key":"x","basis_points":1}]}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	history, err := client.GetPlanLimitHistory(context.Background())
	if err != nil {
		t.Fatalf("GetPlanLimitHistory() error = %v", err)
	}
	if history == nil || len(history.Periods) != 1 {
		t.Fatalf("history = %#v", history)
	}
	if !history.ApproximateOrDefault() {
		t.Fatal("omitted approximate must default to true")
	}
	if got := history.Periods[0].UsedBasisPoints; got == nil || *got != 4250 {
		t.Fatalf("used_basis_points = %v", got)
	}
	if got := history.Periods[0].Breakdowns[1].Dimension; got != PlanLimitDimensionUnknown {
		t.Fatalf("unknown dimension = %q", got)
	}
}

func TestCloudClientPlanLimitHistoryInvalidBodyIsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"periods":"not-an-array"}`))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	_, err := client.GetPlanLimitHistory(context.Background())
	if err == nil || !errors.Is(err, errInvalidPlanHistoryResponse) {
		t.Fatalf("error = %v, want invalid plan history response", err)
	}
}

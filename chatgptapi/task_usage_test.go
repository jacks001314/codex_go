package chatgptapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// These tests mirror Rust #45768's backend-client coverage: exact decimal
// preservation and ordering for consumer balance credits, plus request and
// response validation for the query_v2 contract.

func TestTaskUsageCreditsPreservePrecisionAndOrder(t *testing.T) {
	values := []string{
		"1.000000000000000002",
		"-0.00025",
		"0",
		"1.000000000000000001",
		"1e-20",
		"-1E+3",
	}
	credits := make([]TaskUsageCredits, 0, len(values))
	for _, value := range values {
		parsed, err := ParseTaskUsageCredits(value)
		if err != nil {
			t.Fatalf("ParseTaskUsageCredits(%q) error = %v", value, err)
		}
		if parsed.String() != value {
			t.Fatalf("credits string = %q, want %q", parsed.String(), value)
		}
		credits = append(credits, parsed)
	}
	sort.Slice(credits, func(left, right int) bool { return credits[left].Less(credits[right]) })
	got := make([]string, 0, len(credits))
	for _, credit := range credits {
		got = append(got, credit.String())
	}
	want := []string{
		"-1E+3",
		"-0.00025",
		"0",
		"1e-20",
		"1.000000000000000001",
		"1.000000000000000002",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ordered credits = %v, want %v", got, want)
		}
	}
	zero, err := ParseTaskUsageCredits("0.00")
	if err != nil {
		t.Fatalf("ParseTaskUsageCredits(0.00) error = %v", err)
	}
	plainZero, err := ParseTaskUsageCredits("0")
	if err != nil {
		t.Fatalf("ParseTaskUsageCredits(0) error = %v", err)
	}
	if zero.Compare(plainZero) != 0 {
		t.Fatalf("0.00 compared unequal to 0")
	}
	for _, invalid := range []string{"NaN", "inf", "1..0", "", "1e999999999999"} {
		if _, err := ParseTaskUsageCredits(invalid); err == nil {
			t.Fatalf("ParseTaskUsageCredits(%q) accepted an invalid value", invalid)
		}
	}
}

func TestGetTaskUsageValidatesRequestGroupsAndResponseOwnership(t *testing.T) {
	query := TaskUsageThread{
		ThreadID:            "root",
		DescendantThreadIDs: []string{"child"},
	}
	task := map[string]any{
		"thread_id":               "root",
		"data_status":             "available",
		"usage_source":            "plan_and_credits",
		"five_hour_limit_percent": 150.0,
		"weekly_limit_percent":    0.0,
		"balance_usage_credits":   "-0.000000000000000001",
		"groups": []any{map[string]any{
			"product_experience":      "codex",
			"model":                   "gpt-5.5",
			"reasoning_effort":        "high",
			"speed":                   "standard",
			"five_hour_limit_percent": 150.25,
			"weekly_limit_percent":    0.125,
			"balance_usage_credits":   "0",
		}},
	}
	rowShape := ""
	var requests int
	var lastBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/usage/thread_usage/query_v2" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		requests++
		lastBody = nil
		if err := json.NewDecoder(r.Body).Decode(&lastBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if rowShape == "missing-threads" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data_as_of": nil})
			return
		}
		var rows []any
		switch rowShape {
		case "other":
			other := cloneTaskRow(task)
			other["thread_id"] = "other"
			rows = append(rows, other)
		case "duplicate":
			rows = append(rows, cloneTaskRow(task), cloneTaskRow(task))
		case "no-groups":
			noGroups := cloneTaskRow(task)
			delete(noGroups, "groups")
			rows = append(rows, noGroups)
		case "invalid-status":
			bad := cloneTaskRow(task)
			bad["data_status"] = "unknown"
			rows = append(rows, bad)
		case "invalid-credits":
			bad := cloneTaskRow(task)
			bad["balance_usage_credits"] = "NaN"
			rows = append(rows, bad)
		default:
			rows = append(rows, cloneTaskRow(task))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data_as_of": nil, "threads": rows})
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL})

	response, err := client.GetTaskUsage(context.Background(), []TaskUsageThread{query})
	if err != nil {
		t.Fatalf("GetTaskUsage error = %v", err)
	}
	if response.DataAsOf != nil || len(response.Threads) != 1 {
		t.Fatalf("response = %+v", response)
	}
	row := response.Threads[0]
	if row.ThreadID != "root" || row.DataStatus != TaskUsageStatusAvailable || row.UsageSource != "plan_and_credits" {
		t.Fatalf("row = %+v", row)
	}
	if row.Amounts.FiveHourLimitPercent == nil || *row.Amounts.FiveHourLimitPercent != 150.0 ||
		row.Amounts.WeeklyLimitPercent == nil || *row.Amounts.WeeklyLimitPercent != 0.0 {
		t.Fatalf("amounts = %+v", row.Amounts)
	}
	if row.Amounts.BalanceUsageCredits == nil || row.Amounts.BalanceUsageCredits.String() != "-0.000000000000000001" {
		t.Fatalf("balance credits = %+v", row.Amounts.BalanceUsageCredits)
	}
	if len(row.Groups) != 1 || row.Groups[0].Amounts.FiveHourLimitPercent == nil ||
		*row.Groups[0].Amounts.FiveHourLimitPercent != 150.25 ||
		row.Groups[0].Amounts.WeeklyLimitPercent == nil || *row.Groups[0].Amounts.WeeklyLimitPercent != 0.125 {
		t.Fatalf("groups = %+v", row.Groups)
	}
	wantBody := map[string]any{
		"threads": []any{map[string]any{
			"thread_id":             "root",
			"created_at":            nil,
			"descendant_thread_ids": []any{"child"},
		}},
	}
	if fmt.Sprint(lastBody) != fmt.Sprint(wantBody) {
		t.Fatalf("request body = %v, want %v", lastBody, wantBody)
	}

	for _, shape := range []string{"other", "duplicate", "missing-threads", "no-groups", "invalid-status", "invalid-credits"} {
		rowShape = shape
		response, err := client.GetTaskUsage(context.Background(), []TaskUsageThread{query})
		if err == nil {
			t.Fatalf("%s response was accepted: %+v", shape, response)
		}
		if err.Error() != taskUsageResponseMessage {
			t.Fatalf("%s error = %v, want %q", shape, err, taskUsageResponseMessage)
		}
	}
	rowShape = ""

	// Invalid request groups are rejected before any HTTP request.
	before := requests
	for _, queries := range [][]TaskUsageThread{
		nil,
		{query, query},
		{{ThreadID: "root", DescendantThreadIDs: thousandChildren()}},
		{{ThreadID: " ", DescendantThreadIDs: []string{"child"}}},
	} {
		if _, err := client.GetTaskUsage(context.Background(), queries); err == nil {
			t.Fatalf("GetTaskUsage(%d queries) accepted an invalid request", len(queries))
		} else if err.Error() != taskUsageRequestMessage {
			t.Fatalf("request error = %v, want %q", err, taskUsageRequestMessage)
		}
	}
	if requests != before {
		t.Fatalf("invalid requests reached the server: %d -> %d", before, requests)
	}
}

func cloneTaskRow(row map[string]any) map[string]any {
	clone := make(map[string]any, len(row))
	for key, value := range row {
		clone[key] = value
	}
	return clone
}

func thousandChildren() []string {
	children := make([]string, 0, 1000)
	for index := 0; index < 1000; index++ {
		children = append(children, fmt.Sprintf("child-%d", index))
	}
	return children
}

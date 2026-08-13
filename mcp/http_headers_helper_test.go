package mcp

import (
	"strings"
	"testing"
)

func TestParseMCPHTTPHeadersHelperOutput(t *testing.T) {
	headers, err := parseMCPHTTPHeadersHelperOutput([]byte(`{"X-Auth":"secret","X-Custom":"value"}`))
	if err != nil {
		t.Fatalf("parse valid headers: %v", err)
	}
	if headers.Get("X-Auth") != "secret" || headers.Get("X-Custom") != "value" {
		t.Fatalf("headers = %#v", headers)
	}

	if _, err := parseMCPHTTPHeadersHelperOutput([]byte(`{"Authorization":"Bearer secret"}`)); err == nil || !strings.Contains(err.Error(), "reserved header") {
		t.Fatalf("reserved header error = %v", err)
	}
	if _, err := parseMCPHTTPHeadersHelperOutput([]byte(`{"Bad Header":"value"}`)); err == nil {
		t.Fatalf("invalid header name was accepted")
	}
	if _, err := parseMCPHTTPHeadersHelperOutput([]byte(`{"X-A":"one","x-a":"two"}`)); err == nil {
		t.Fatalf("duplicate header names were accepted")
	}
	if _, err := parseMCPHTTPHeadersHelperOutput([]byte(`not-json`)); err == nil {
		t.Fatalf("non-JSON output was accepted")
	}
}

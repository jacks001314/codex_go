package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func stringPtrForResourceTarget(value string) *string { return &value }

// TestMCPResourceReadTargetValidationLikeRust pins Rust #47248: direct targets
// require codex_apps, a connector id, and an explicit real linkId or null.
func TestMCPResourceReadTargetValidationLikeRust(t *testing.T) {
	link := "link_calendar"
	cases := []struct {
		name   string
		server string
		params *MCPResourceReadParams
		want   bool
	}{
		{
			name:   "no target",
			server: "codex_apps",
			params: &MCPResourceReadParams{ServerName: "codex_apps"},
			want:   true,
		},
		{
			name:   "account link",
			server: "codex_apps",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{ConnectorID: "calendar", LinkID: &link, LinkIDSet: true}},
			want:   true,
		},
		{
			name:   "explicit no-auth",
			server: "codex_apps",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{ConnectorID: "calendar", LinkIDSet: true}},
			want:   true,
		},
		{
			name:   "other server",
			server: "memory",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{ConnectorID: "calendar", LinkID: &link, LinkIDSet: true}},
		},
		{
			name:   "empty connector",
			server: "codex_apps",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{LinkID: &link, LinkIDSet: true}},
		},
		{
			name:   "missing link",
			server: "codex_apps",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{ConnectorID: "calendar"}},
		},
		{
			name:   "empty link",
			server: "codex_apps",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{ConnectorID: "calendar", LinkID: stringPtrForResourceTarget("  "), LinkIDSet: true}},
		},
		{
			name:   "synthetic link",
			server: "codex_apps",
			params: &MCPResourceReadParams{Target: &MCPResourceReadTarget{ConnectorID: "calendar", LinkID: stringPtrForResourceTarget("synthetic_link::calendar"), LinkIDSet: true}},
		},
		{
			name:   "origin scope wins",
			server: "codex_apps",
			params: &MCPResourceReadParams{OriginCallID: "call-1", Target: &MCPResourceReadTarget{}},
			want:   true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateMCPResourceTarget(test.params, test.server)
			if test.want && err != nil {
				t.Fatalf("validateMCPResourceTarget() error = %v", err)
			}
			if !test.want {
				if err == nil {
					t.Fatal("validateMCPResourceTarget() = nil, want rejection")
				}
				if !strings.Contains(err.Error(), "target requires codex_apps") {
					t.Fatalf("error = %v", err)
				}
			}
		})
	}
}

func TestMCPResourceReadTargetWireShapeLikeRust(t *testing.T) {
	encoded, err := json.Marshal(MCPResourceReadParams{
		Server: "codex_apps",
		URI:    "file://demo",
		Target: &MCPResourceReadTarget{ConnectorID: "calendar", LinkIDSet: true},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"linkId":null`) {
		t.Fatalf("no-auth target must keep linkId null: %s", encoded)
	}

	var decoded MCPResourceReadParams
	if err := json.Unmarshal([]byte(`{"server":"codex_apps","uri":"file://demo","target":{"connectorId":"calendar","linkId":"link_calendar"}}`), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Target == nil || !decoded.Target.LinkIDSet || decoded.Target.LinkID == nil || *decoded.Target.LinkID != "link_calendar" {
		t.Fatalf("decoded target = %#v", decoded.Target)
	}

	var missing MCPResourceReadParams
	if err := json.Unmarshal([]byte(`{"server":"codex_apps","uri":"file://demo","target":{"connectorId":"calendar"}}`), &missing); err != nil {
		t.Fatalf("Unmarshal(missing) error = %v", err)
	}
	if missing.Target == nil || missing.Target.LinkIDSet {
		t.Fatalf("missing linkId must be distinguishable from null: %#v", missing.Target)
	}
}

// TestMCPResourceReadForwardsTargetMetaLikeRust pins the wire contract: the
// MCP server receives `connector_id`/`link_id` for direct targets, the legacy
// connector id as turn metadata, and different targets never share cache
// contents.
func TestMCPResourceReadForwardsTargetMetaLikeRust(t *testing.T) {
	var mu sync.Mutex
	var reads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mcpHTTPEnvelopeForTargetTest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "target-session")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("decode params: %v", err)
				return
			}
			mu.Lock()
			reads = append(reads, params)
			mu.Unlock()
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"contents": []map[string]string{{"uri": "file://demo", "text": "demo"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	t.Cleanup(server.Close)
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		RuntimeCodexAppsMCPServerName: {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	link := "link_calendar"
	if _, err := service.ReadResource(&MCPResourceReadParams{
		ServerName: RuntimeCodexAppsMCPServerName,
		URI:        "file://demo",
		Target:     &MCPResourceReadTarget{ConnectorID: "calendar", LinkID: &link, LinkIDSet: true},
	}); err != nil {
		t.Fatalf("ReadResource(account) error = %v", err)
	}
	mu.Lock()
	accountReads := len(reads)
	mu.Unlock()
	if accountReads != 1 {
		t.Fatalf("account read reached the server %d times, want 1", accountReads)
	}
	if _, err := service.ReadResource(&MCPResourceReadParams{
		ServerName:  RuntimeCodexAppsMCPServerName,
		URI:         "file://demo",
		ConnectorID: "calendar",
		Target:      &MCPResourceReadTarget{ConnectorID: "calendar", LinkIDSet: true},
	}); err != nil {
		t.Fatalf("ReadResource(no-auth) error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	meta, _ := reads[0]["_meta"].(map[string]any)
	if meta["connector_id"] != "calendar" || meta["link_id"] != "link_calendar" {
		t.Fatalf("account target meta = %#v", meta)
	}
	noAuthMeta, _ := reads[1]["_meta"].(map[string]any)
	if noAuthMeta["link_id"] != nil {
		t.Fatalf("no-auth target link_id = %#v, want null", noAuthMeta["link_id"])
	}
	if _, present := noAuthMeta["link_id"]; !present {
		t.Fatalf("no-auth target must include link_id: %#v", noAuthMeta)
	}
	turnMeta, _ := noAuthMeta[MCPToolTurnMetadataMetaKey].(map[string]any)
	requestMeta, _ := turnMeta["mcp_request_meta"].(map[string]any)
	selected, _ := requestMeta["selected_connector_ids"].([]any)
	if len(selected) != 1 || selected[0] != "calendar" {
		t.Fatalf("selected connector metadata = %#v", noAuthMeta[MCPToolTurnMetadataMetaKey])
	}
}

type mcpHTTPEnvelopeForTargetTest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

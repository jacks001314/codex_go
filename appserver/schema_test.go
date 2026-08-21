package appserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"codex_go/apps"
	authpkg "codex_go/auth"
	configpkg "codex_go/config"
	"codex_go/mcp"
	sandboxpkg "codex_go/sandbox"
	"github.com/klauspost/compress/zstd"
)

func TestGenerateJSONSchemaIncludesExperimentalMethods(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateJSONSchema(&SchemaGenerateOptions{OutDir: dir, Experimental: true}); err != nil {
		t.Fatalf("GenerateJSONSchema() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ClientRequest.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"initialize"`) || !strings.Contains(output, `"thread/realtime/start"`) {
		t.Fatalf("schema output = %q", output)
	}
}

func TestGenerateTypeScriptWritesProtocolSchema(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateTypeScript(&SchemaGenerateOptions{OutDir: dir}); err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ClientRequest.ts"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	output := string(data)
	if !strings.HasPrefix(output, generatedTypeScriptHeader) ||
		!strings.Contains(output, `"initialize"`) ||
		!strings.Contains(output, `"getAuthStatus"`) {
		t.Fatalf("typescript output = %q", output)
	}
}

func TestPrecomputedExportsAreWrittenToDiskLikeRust(t *testing.T) {
	exports, err := loadPrecomputedProtocolExports(false)
	if err != nil {
		t.Fatal(err)
	}
	typescriptDir := t.TempDir()
	jsonDir := t.TempDir()
	internalJSONDir := t.TempDir()
	if err := GenerateTypeScript(&SchemaGenerateOptions{OutDir: typescriptDir}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateJSONSchema(&SchemaGenerateOptions{OutDir: jsonDir}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateJSONSchema(&SchemaGenerateOptions{OutDir: internalJSONDir, Internal: true}); err != nil {
		t.Fatal(err)
	}
	if got := collectGeneratedExports(t, typescriptDir); !reflect.DeepEqual(got, exports.TypeScript) {
		t.Fatalf("TypeScript export tree differs: got %d files, want %d", len(got), len(exports.TypeScript))
	}
	if got := collectGeneratedExports(t, jsonDir); !reflect.DeepEqual(got, exports.JSONSchema) {
		t.Fatalf("JSON export tree differs: got %d files, want %d", len(got), len(exports.JSONSchema))
	}
	if got := collectGeneratedExports(t, internalJSONDir); !reflect.DeepEqual(got, exports.InternalJSONSchema) {
		t.Fatalf("internal JSON export tree differs: got %d files, want %d", len(got), len(exports.InternalJSONSchema))
	}
}

func TestPrecomputedExperimentalExportsAreWrittenToDiskLikeRust(t *testing.T) {
	exports, err := loadPrecomputedProtocolExports(true)
	if err != nil {
		t.Fatal(err)
	}
	typescriptDir := t.TempDir()
	jsonDir := t.TempDir()
	if err := GenerateTypeScript(&SchemaGenerateOptions{OutDir: typescriptDir, Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateJSONSchema(&SchemaGenerateOptions{OutDir: jsonDir, Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if got := collectGeneratedExports(t, typescriptDir); !reflect.DeepEqual(got, exports.TypeScript) {
		t.Fatalf("experimental TypeScript export tree differs: got %d files, want %d", len(got), len(exports.TypeScript))
	}
	if got := collectGeneratedExports(t, jsonDir); !reflect.DeepEqual(got, exports.JSONSchema) {
		t.Fatalf("experimental JSON export tree differs: got %d files, want %d", len(got), len(exports.JSONSchema))
	}
}

func TestPrecomputedTypeScriptOptionsPreserveRustBehavior(t *testing.T) {
	options := DefaultGenerateTypeScriptOptions()
	options.GenerateIndices = false
	options.EnsureHeaders = false
	options.RunPrettier = false
	dir := t.TempDir()
	if err := GenerateTypeScriptWithOptions(dir, "", options); err != nil {
		t.Fatal(err)
	}
	files := collectGeneratedExports(t, dir)
	for path, contents := range files {
		if filepath.Base(filepath.FromSlash(path)) == "index.ts" {
			t.Fatalf("generated disabled index %s", path)
		}
		if strings.HasPrefix(contents, generatedTypeScriptHeader) {
			t.Fatalf("generated header for %s", path)
		}
	}
}

func TestPrecomputedExportPathsRejectTraversalLikeRust(t *testing.T) {
	for _, path := range []string{"", "/absolute.json", "../escape.json", "v2/../escape.json", "v2//empty.json", "./local.json"} {
		if _, err := writePrecomputedExport(t.TempDir(), path, "{}"); err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func TestPrecomputedExportArtifactsMatchTargetRustCommit(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"stable", stablePrecomputedExports, "fd46d1f89d223e6c415f548df285fdffc4e0d95854d4101524b21fa067856f49"},
		{"experimental", experimentalPrecomputedExports, "31e8dfc1df42b08eb1d2e737915a41328e782cb7104d9d67a096834c59613c32"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sum := sha256.Sum256(test.data)
			if got := fmt.Sprintf("%x", sum[:]); got != test.want {
				t.Fatalf("artifact SHA-256 = %s, want %s", got, test.want)
			}
		})
	}
}

func collectGeneratedExports(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBuildProtocolSchemaIndexesRPCSurface(t *testing.T) {
	stable := BuildProtocolSchema(false, false)
	requireProtocolMethod(t, stable.ClientRequests, string(MethodCommandExec), false)
	requireProtocolMethod(t, stable.ClientRequests, string(MethodMCPServerToolCall), false)
	requireProtocolMethod(t, stable.Notifications, string(NotificationCommandExecOutputDelta), false)
	requireProtocolMethod(t, stable.Notifications, string(NotificationServerRequestResolved), false)
	requireProtocolSignature(t, stable.ClientRequests, string(MethodCommandExec), "CommandExecParams", "CommandExecResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodAppList), "AppsListParams", "AppsListResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodAppRead), "AppsReadParams", "AppsReadResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodAppInstalled), "AppsInstalledParams", "AppsInstalledResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodConsumeAccountRateLimitResetCredit), "ConsumeAccountRateLimitResetCreditParams", "ConsumeAccountRateLimitResetCreditResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodGetWorkspaceMessages), "", "GetWorkspaceMessagesResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodConfigRequirementsRead), "", "ConfigRequirementsReadResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodExternalAgentConfigImportHistoryRecord), "ExternalAgentConfigImportHistoryRecordParams", "ExternalAgentConfigImportHistoryRecordResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodExternalAgentConfigImportHistoriesRead), "", "ExternalAgentConfigImportHistoriesReadResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodHooksList), "HooksListParams", "HooksListResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodMCPServerToolCall), "McpServerToolCallParams", "McpServerToolCallResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodThreadGoalSet), "ThreadGoalSetParams", "ThreadGoalSetResponse")
	requireProtocolSignature(t, stable.ClientRequests, string(MethodWindowsSandboxReadiness), "", "WindowsSandboxReadinessResponse")
	requireProtocolSignature(t, stable.ServerRequests, string(ServerRequestChatGPTAuthTokensRefresh), "ChatgptAuthTokensRefreshParams", "ChatgptAuthTokensRefreshResponse")
	requireProtocolSignature(t, stable.Notifications, string(NotificationAppListUpdated), "AppListUpdatedNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationCommandExecOutputDelta), "CommandExecOutputDeltaNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationConfigWarning), "ConfigWarningNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationGuardianWarning), "GuardianWarningNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationItemGuardianApprovalReviewStarted), "ItemGuardianApprovalReviewStartedNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationItemGuardianApprovalReviewCompleted), "ItemGuardianApprovalReviewCompletedNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationStrictReviewRequired), "StrictReviewRequiredNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationMCPServerStatusUpdated), "McpServerStatusUpdatedNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationModelSafetyBufferingUpdated), "ModelSafetyBufferingUpdatedNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationThreadEnvironmentConnected), "EnvironmentConnectionNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationThreadEnvironmentDisconnected), "EnvironmentConnectionNotification", "")
	requireProtocolSignature(t, stable.Notifications, string(NotificationThreadRealtimeStarted), "ThreadRealtimeStartedNotification", "")
	requireProtocolMethodAbsent(t, stable.ClientRequests, string(MethodProcessSpawn))
	requireProtocolMethodAbsent(t, stable.ClientRequests, string(MethodAccountSessionsList))
	requireProtocolMethodAbsent(t, stable.ClientRequests, string(MethodMCPServerOauthCancel))
	requireProtocolMethodAbsent(t, stable.ClientRequests, string(MethodMCPServerRefresh))
	requireProtocolMethodAbsent(t, stable.ServerRequests, string(ServerRequestCurrentTimeRead))
	requireProtocolMethod(t, stable.Notifications, string(NotificationProcessOutputDelta), false)
	requireProtocolMethod(t, stable.Notifications, string(NotificationThreadRealtimeStarted), false)
	requireProtocolMethod(t, stable.Notifications, string(NotificationTurnModerationMetadata), false)

	experimental := BuildProtocolSchema(true, false)
	requireProtocolSignature(t, experimental.ClientRequests, string(MethodThreadSearchOccurrences), "ThreadSearchOccurrencesParams", "ThreadSearchOccurrencesResponse")
	requireProtocolMethod(t, experimental.ClientRequests, string(MethodThreadIncrementElicitation), true)
	requireProtocolMethod(t, experimental.ClientRequests, string(MethodThreadRealtimeStart), true)
	requireProtocolMethod(t, experimental.ClientRequests, string(MethodProcessSpawn), true)
	requireProtocolMethod(t, experimental.ServerRequests, string(ServerRequestCurrentTimeRead), true)
	requireProtocolMethod(t, experimental.Notifications, string(NotificationProcessOutputDelta), false)
	requireProtocolMethod(t, experimental.Notifications, string(NotificationThreadRealtimeStarted), false)
	requireProtocolSignature(t, experimental.ClientRequests, string(MethodMemoryReset), "", "MemoryResetResponse")
	requireProtocolSignature(t, experimental.ServerRequests, string(ServerRequestCurrentTimeRead), "CurrentTimeReadParams", "CurrentTimeReadResponse")
	requireProtocolSignature(t, experimental.Notifications, string(NotificationProcessOutputDelta), "ProcessOutputDeltaNotification", "")

	internal := BuildProtocolSchema(false, true)
	requireProtocolMethod(t, internal.ClientRequests, string(MethodAccountSessionsList), false)
}

func TestBuildProtocolSchemaMatchesRustStableFixtures(t *testing.T) {
	root := rustAppServerProtocolSchemaRoot(t)
	stable := BuildProtocolSchema(false, false)

	requireProtocolMethodSetMatchesRustFixture(t, filepath.Join(root, "ClientRequest.json"), stable.ClientRequests)
	requireProtocolMethodSetMatchesRustFixture(t, filepath.Join(root, "ServerRequest.json"), stable.ServerRequests)
	requireProtocolMethodSetMatchesRustFixture(t, filepath.Join(root, "ServerNotification.json"), stable.Notifications)
	requireProtocolSchemaTypesHaveRustFixtures(t, root, stable.ClientRequests)
	requireProtocolSchemaTypesHaveRustFixtures(t, root, stable.ServerRequests)
	requireProtocolSchemaTypesHaveRustFixtures(t, root, stable.Notifications)
}

func TestRustProtocolMethodSurfaceIsCoveredByGoSchemas(t *testing.T) {
	root := rustAppServerProtocolRustRoot(t)
	rustMethods := rustMethodsFromCommon(t, filepath.Join(root, "app-server-protocol", "src", "protocol", "common.rs"))
	stable := BuildProtocolSchema(false, false)
	experimental := BuildProtocolSchema(true, false)

	covered := make(map[string]bool)
	for _, method := range append(append(append([]ProtocolMethod{}, stable.ClientRequests...), experimental.ClientRequests...), append(append([]ProtocolMethod{}, stable.ServerRequests...), experimental.ServerRequests...)...) {
		covered[method.Method] = true
	}
	for _, method := range append(append([]ProtocolMethod{}, stable.Notifications...), experimental.Notifications...) {
		covered[method.Method] = true
	}

	var missing []string
	for _, method := range rustMethods {
		if !covered[method] {
			missing = append(missing, method)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("Go protocol schemas do not cover Rust methods: %v", missing)
	}
}

func TestBuildTypeScriptProtocolSchemaMatchesRustFixtures(t *testing.T) {
	root := rustAppServerProtocolTypeScriptRoot(t)
	stable := BuildTypeScriptProtocolSchema(false, false)

	requireTypeScriptMethodSetMatchesRustFixture(t, filepath.Join(root, "ClientRequest.ts"), stable.ClientRequests)
	requireTypeScriptMethodSetMatchesRustFixture(t, filepath.Join(root, "ServerRequest.ts"), stable.ServerRequests)
	requireTypeScriptMethodSetMatchesRustFixture(t, filepath.Join(root, "ServerNotification.ts"), stable.Notifications)
	requireProtocolMethod(t, stable.ClientRequests, string(MethodGetAuthStatus), false)
	requireProtocolMethod(t, stable.ClientRequests, string(MethodGetConversationSummary), false)
	requireProtocolMethod(t, stable.ClientRequests, string(MethodGitDiffToRemote), false)
	requireProtocolMethod(t, stable.Notifications, string(NotificationRawResponseItemCompleted), false)
	requireProtocolSignature(t, stable.Notifications, string(NotificationRawResponseItemCompleted), "RawResponseItemCompletedNotification", "")
	requireProtocolSignature(t, BuildTypeScriptProtocolSchema(false, false).Notifications, string(NotificationRawResponseCompleted), "RawResponseCompletedNotification", "")
}

func TestRustAppServerProtocolSchemaTreeIsCoveredByGoGenerators(t *testing.T) {
	jsonRoot := rustAppServerProtocolSchemaRoot(t)
	tsRoot := filepath.Dir(jsonRoot)
	jsonFiles := rustSchemaFixtureNames(t, jsonRoot)
	tsFiles := rustSchemaFixtureNames(t, tsRoot)

	stable := BuildProtocolSchema(false, false)
	tsStable := BuildTypeScriptProtocolSchema(false, false)

	if missing := missingSchemaFixtureCoverage(jsonFiles, stable); len(missing) > 0 {
		t.Fatalf("Go JSON schema generators do not cover Rust json fixture tree: %v", missing)
	}
	if missing := missingSchemaFixtureCoverage(tsFiles, tsStable); len(missing) > 0 {
		t.Fatalf("Go TypeScript schema generators do not cover Rust typescript fixture tree: %v", missing)
	}
}

func TestProtocolPayloadsValidateAgainstRustSchemas(t *testing.T) {
	root := rustAppServerProtocolSchemaRoot(t)
	searchToken := "search-schema"
	contextWindow := int64(128000)
	permissionProfile := "trusted"
	mcpThreadID := "thread-schema"
	mcpFailureReason := MCPServerStartupFailureReauthenticationRequired
	mcpStatusLimit := uint32(25)
	mcpResourceSize := int64(512)
	commandOutputCap := 4096
	commandTimeoutMS := int64(1000)
	commandDelta := "aGkK"
	cases := []struct {
		typeName string
		value    any
	}{
		{"FuzzyFileSearchParams", &FuzzyFileSearchParams{Query: "readme", Roots: []string{"D:/workspace"}, CancellationToken: &searchToken}},
		{"InitializeResponse", NewInitializeResponse("D:/codex-home", "codex_cli_rs/0.0.0 (windows; amd64) go")},
		{"JSONRPCResponse", OK(IntID(7), map[string]any{"ok": true})},
		{"JSONRPCError", ErrorResponse(IntID(7), JSONRPCInternalErrorCode, "boom", map[string]any{"type": "schema_error"})},
		{"JSONRPCNotification", NewNotification(NotificationConfigWarning, map[string]any{"summary": "queued", "details": nil})},
		{"JSONRPCMessage", OK(StringID("request-schema"), map[string]any{"ok": true})},
		{"JSONRPCMessage", ErrorResponse(StringID("request-schema"), JSONRPCInvalidParamsErrorCode, "invalid params", nil)},
		{"JSONRPCMessage", NewNotification(NotificationWarning, &WarningNotification{Message: "careful"})},
		{"JSONRPCMessage", &Request{ID: StringID("request-schema"), Method: MethodThreadRead, Params: json.RawMessage(`{"threadId":"thread-schema"}`)}},
		{"ThreadStartResponse", sampleRustSchemaThreadStartResponse()},
		{"ThreadReadResponse", &ThreadReadResponse{Thread: sampleRustSchemaThreadWithTurns()}},
		{"ThreadListResponse", sampleRustSchemaThreadListResponse()},
		{"ThreadLoadedListResponse", sampleRustSchemaThreadLoadedListResponse()},
		{"ThreadResumeResponse", sampleRustSchemaThreadResumeResponse()},
		{"ThreadForkResponse", sampleRustSchemaThreadForkResponse()},
		{"ThreadRollbackResponse", &ThreadRollbackResponse{Thread: sampleRustSchemaThreadWithTurns()}},
		{"ThreadMetadataUpdateResponse", &ThreadMetadataUpdateResponse{Thread: sampleRustSchemaThread()}},
		{"ThreadStartedNotification", &ThreadStartedNotification{Thread: sampleRustSchemaThread()}},
		{"ThreadNameUpdatedNotification", &ThreadNameUpdatedNotification{ThreadID: "thread-schema"}},
		{"ThreadGoalUpdatedNotification", &GoalUpdatedNotification{ThreadID: "thread-schema", Goal: sampleRustSchemaGoal()}},
		{"ThreadGoalClearedNotification", &GoalClearedNotification{ThreadID: "thread-schema"}},
		{"ThreadSettingsUpdatedNotification", &SettingsUpdatedNotification{ThreadID: "thread-schema", ThreadSettings: Settings{CWD: "D:/workspace", SandboxPolicy: "read-only", ActivePermissionProfile: &permissionProfile, Model: "gpt-5", ModelProvider: "openai"}}},
		{"ThreadTokenUsageUpdatedNotification", &ThreadTokenUsageUpdatedNotification{ThreadID: "thread-schema", TurnID: "turn-schema", TokenUsage: TokenUsage{InputTokens: 10, CachedInputTokens: 2, OutputTokens: 5, ReasoningOutputTokens: 1, ModelContextWindow: &contextWindow}}},
		{"AppsReadParams", &apps.AppsReadParams{AppIDs: []string{"calendar"}, IncludeTools: true}},
		{"AppsReadResponse", &apps.AppsReadResponse{Apps: []apps.ConnectorMetadata{{ID: "calendar", Name: "Calendar", ToolSummaries: []apps.AppToolSummary{{Name: "search", Description: "Search events"}}, ToolsRequested: true}}, MissingAppIDs: []string{"missing"}}},
		{"AppsInstalledParams", &apps.AppsInstalledParams{ForceRefresh: true}},
		{"AppsInstalledResponse", &apps.AppsInstalledResponse{Apps: []apps.InstalledApp{{ID: "calendar", Enabled: true, Callable: true}}}},
		{"ThreadSearchOccurrencesParams", &ThreadSearchOccurrencesParams{ThreadID: "thread-1", SearchTerm: "needle"}},
		{"ThreadSearchOccurrencesResponse", &ThreadSearchOccurrencesResponse{Data: []ThreadSearchOccurrence{{TurnID: "turn-1", ItemID: "item-1", Snippet: "needle", SnippetMatchRange: ThreadSearchTextRange{Start: 0, End: 6}, TurnCursor: "0"}}}},
		{"EnvironmentStatusParams", &EnvironmentStatusParams{EnvironmentID: "environment-schema"}},
		{"EnvironmentStatusResponse", &EnvironmentStatusResponse{Status: EnvironmentStatusDisconnected, Error: stringPtr("connection closed")}},
		{"EnvironmentConnectionNotification", &EnvironmentConnectionNotification{ThreadID: "thread-schema", EnvironmentID: "environment-schema"}},
		{"ExternalAgentConfigDetectResponse", &configpkg.ExternalAgentConfigDetectResponse{Connectors: []configpkg.ExternalAgentDetectedConnectorCandidate{{Name: "Google Drive", SessionCount: 3, Source: configpkg.ExternalAgentConnectorSessionToolUse}}}},
		{"GetAccountResponse", &authpkg.GetAccountResponse{Account: &authpkg.Account{Type: authpkg.AccountChatGPT, PlanType: authpkg.PlanEnterpriseCBPAutomation}, RequiresOpenAIAuth: true}},
		{"RawResponseCompletedNotification", &RawResponseCompletedNotification{ThreadID: "thread-schema", TurnID: "turn-schema", ResponseID: "resp-schema", Usage: &TokenUsageBreakdown{TotalTokens: 15, InputTokens: 10, CachedInputTokens: 2, CacheWriteInputTokens: 3, OutputTokens: 5, ReasoningOutputTokens: 1}}},
		{"TurnStartedNotification", &TurnStartedNotification{ThreadID: "thread-schema", Turn: sampleRustSchemaTurn(TurnStatusInProgress)}},
		{"TurnCompletedNotification", &TurnCompletedNotification{ThreadID: "thread-schema", Turn: sampleRustSchemaTurn(TurnStatusCompleted)}},
		{"TurnCompletedNotification", &TurnCompletedNotification{ThreadID: "thread-schema", Turn: sampleRustSchemaTurnWithAllThreadItems()}},
		{"ItemStartedNotification", &ItemStartedNotification{ThreadID: "thread-schema", TurnID: "turn-schema", Item: sampleRustSchemaThreadItemPayload(sampleRustSchemaThreadItems()[5]), StartedAtMS: 1700000000000}},
		{"ItemCompletedNotification", &ItemCompletedNotification{ThreadID: "thread-schema", TurnID: "turn-schema", Item: sampleRustSchemaThreadItemPayload(sampleRustSchemaThreadItems()[7]), CompletedAtMS: 1700000000000}},
		{"WarningNotification", &WarningNotification{Message: "careful"}},
		{"DeprecationNoticeNotification", &DeprecationNoticeNotification{Summary: "legacy summary"}},
		{"ModelSafetyBufferingUpdatedNotification", &ModelSafetyBufferingUpdatedNotification{ThreadID: "thread-schema", TurnID: "turn-schema", Model: "gpt-5"}},
		{"ThreadRealtimeStartedNotification", &ThreadRealtimeStartedNotification{ThreadID: "thread-schema", Version: "v2"}},
		{"ThreadRealtimeOutputAudioDeltaNotification", &ThreadRealtimeOutputAudioDeltaNotification{ThreadID: "thread-schema", Audio: ThreadRealtimeAudioChunk{Data: "AA==", NumChannels: 1, SampleRate: 24000}}},
		{"ServerRequestResolvedNotification", &ServerRequestResolvedNotification{ThreadID: "thread-schema", RequestID: StringID("request-schema")}},
		{"McpServerStatusUpdatedNotification", &MCPServerStatusUpdatedNotification{ThreadID: &mcpThreadID, Name: "server-schema", Status: "stopped", FailureReason: &mcpFailureReason}},
		{"ListMcpServerStatusParams", &mcp.MCPListServerStatusParams{Cursor: stringPtr("cursor-schema"), Limit: &mcpStatusLimit, Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull}, ThreadID: &mcpThreadID}},
		{"ListMcpServerStatusResponse", &mcp.MCPListServerStatusResponse{Data: []mcp.MCPServerStatus{{
			Server: mcp.MCPServerInfo{Name: "server-schema", Version: "1.0.0", Title: stringPtr("Schema Server"), Description: stringPtr("schema fixture"), Icons: []any{map[string]any{"src": "https://example.test/icon.png"}}, WebsiteURL: stringPtr("https://example.test")},
			Tools: []mcp.MCPToolInfo{{
				Name:         "echo",
				Title:        "Echo",
				Description:  "Echo input",
				InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}},
				OutputSchema: map[string]any{"type": "object"},
				Annotations:  map[string]any{"readOnlyHint": true},
				Meta:         map[string]any{"origin": "schema"},
			}},
			Resources: []mcp.MCPResource{{
				URI:         "file://schema/readme.md",
				Name:        "readme",
				Title:       "Readme",
				Description: "schema resource",
				MimeType:    "text/markdown",
				Size:        &mcpResourceSize,
				Annotations: map[string]any{"audience": []any{"assistant"}},
				Icons:       []any{map[string]any{"src": "https://example.test/resource.png"}},
				Meta:        map[string]any{"cached": false},
			}},
			ResourceTemplates: []mcp.MCPResourceTemplate{{
				URITemplate: "file://schema/{name}",
				Name:        "schema-template",
				Title:       "Schema Template",
				Description: "templated schema resource",
				MimeType:    "text/plain",
				Annotations: map[string]any{"priority": 1},
			}},
			AuthStatus: mcp.MCPAuthOAuth,
			State:      mcp.MCPServerReady,
		}, {
			Name:       "empty-schema",
			AuthStatus: mcp.MCPAuthUnsupported,
		}}, NextCursor: stringPtr("cursor-next")}},
		{"McpResourceReadParams", &mcp.MCPResourceReadParams{ThreadID: &mcpThreadID, Server: "server-schema", URI: "file://schema/readme.md"}},
		{"McpResourceReadResponse", &mcp.MCPResourceReadResponse{Contents: []mcp.MCPResourceContent{
			{URI: "file://schema/readme.md", MimeType: "text/markdown", Text: "# Schema", Meta: map[string]any{"source": "fixture"}},
			{URI: "file://schema/blob.bin", MimeType: "application/octet-stream", Blob: "AAEC", Meta: map[string]any{"binary": true}},
		}}},
		{"McpServerToolCallParams", &mcp.MCPToolCallParams{ThreadID: "thread-schema", TurnID: "turn-schema", ItemID: "item-schema", Server: "server-schema", Tool: "echo", Arguments: map[string]any{"message": "hello from schema"}, Meta: map[string]any{"calledBy": "schema-test"}}},
		{"McpServerToolCallResponse", &mcp.MCPToolCallResponse{Content: []mcp.MCPToolCallContent{{Type: "text", Text: "echo: hello from schema"}}, StructuredContent: map[string]any{"echoed": "hello from schema", "threadId": "thread-schema"}, IsError: boolPtr(false), Meta: map[string]any{"calledBy": "schema-test"}}},
		{"McpServerElicitationRequestParams", &MCPElicitationRequestParams{ThreadID: "thread-schema", TurnID: stringPtr("turn-schema"), ServerName: "server-schema", Mode: "form", Meta: map[string]any{"requestId": "elicitation-schema"}, Message: "Confirm schema action", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"confirmed": map[string]any{"type": "boolean", "title": "Confirm"}}, "required": []any{"confirmed"}}}},
		{"McpServerElicitationRequestParams", &MCPElicitationRequestParams{ThreadID: "thread-schema", TurnID: nil, ServerName: "server-schema", Mode: "url", Meta: nil, Message: "Open authorization", URL: "https://example.test/auth", ElicitationID: "elicitation-schema"}},
		{"McpServerElicitationRequestResponse", &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"confirmed": true}, Meta: map[string]any{"handledBy": "schema-test"}}},
		{"TerminalInteractionNotification", &TerminalInteractionNotification{ThreadID: "thread-schema", TurnID: "turn-schema", ItemID: "item-schema", ProcessID: "process-schema", Stdin: "echo hi"}},
		{"CommandExecParams", &CommandExecParams{Command: []string{"sh", "-lc", "printf hi"}, ProcessID: stringPtr("process-schema"), ThreadID: stringPtr("thread-schema"), TurnID: stringPtr("turn-schema"), ItemID: stringPtr("item-schema"), TTY: true, StreamStdin: true, StreamStdoutStderr: true, OutputBytesCap: &commandOutputCap, TimeoutMS: &commandTimeoutMS, CWD: stringPtr("D:/workspace"), Env: map[string]*string{"SCHEMA_ENV": stringPtr("1"), "UNSET_ENV": nil}, Size: &TerminalSize{Rows: 24, Cols: 80}}},
		{"CommandExecResponse", &CommandExecResponse{ExitCode: 0, Stdout: "ok\n", Stderr: ""}},
		{"CommandExecWriteParams", &CommandExecWriteParams{ProcessID: "process-schema", DeltaBase64: &commandDelta, CloseStdin: true}},
		{"CommandExecWriteResponse", &CommandExecWriteResponse{}},
		{"CommandExecTerminateParams", &CommandExecTerminateParams{ProcessID: "process-schema"}},
		{"CommandExecTerminateResponse", &CommandExecTerminateResponse{}},
		{"CommandExecResizeParams", &CommandExecResizeParams{ProcessID: "process-schema", Size: TerminalSize{Rows: 30, Cols: 120}}},
		{"CommandExecResizeResponse", &CommandExecResizeResponse{}},
		{"CommandExecOutputDeltaNotification", &CommandExecOutputDeltaNotification{ProcessID: "process-schema", Stream: StreamStdout, DeltaBase64: "b2sK", CapReached: false}},
		{"FsReadFileParams", &ReadFileParams{Path: "D:/workspace/note.txt"}},
		{"FsReadFileResponse", &ReadFileResponse{DataBase64: "aGVsbG8="}},
		{"FsWriteFileParams", &WriteFileParams{Path: "D:/workspace/note.txt", DataBase64: "aGVsbG8="}},
		{"FsWriteFileResponse", &WriteFileResponse{}},
		{"FsCreateDirectoryParams", &CreateDirectoryParams{Path: "D:/workspace/nested", Recursive: boolPtr(true)}},
		{"FsCreateDirectoryResponse", &CreateDirectoryResponse{}},
		{"FsGetMetadataParams", &GetMetadataParams{Path: "D:/workspace/note.txt"}},
		{"FsGetMetadataResponse", &GetMetadataResponse{IsDirectory: false, IsFile: true, IsSymlink: false, CreatedAtMS: 1700000000000, ModifiedAtMS: 1700000001000}},
		{"FsReadDirectoryParams", &ReadDirectoryParams{Path: "D:/workspace"}},
		{"FsReadDirectoryResponse", &ReadDirectoryResponse{Entries: []ReadDirectoryEntry{{FileName: "note.txt", IsDirectory: false, IsFile: true}, {FileName: "nested", IsDirectory: true, IsFile: false}}}},
		{"FsRemoveParams", &RemoveParams{Path: "D:/workspace/note.txt", Recursive: boolPtr(false), Force: boolPtr(true)}},
		{"FsRemoveResponse", &RemoveResponse{}},
		{"FsCopyParams", &CopyParams{SourcePath: "D:/workspace/note.txt", DestinationPath: "D:/workspace/copy.txt", Recursive: false}},
		{"FsCopyResponse", &CopyResponse{}},
		{"FsWatchParams", &WatchParams{WatchID: "watch-schema", Path: "D:/workspace"}},
		{"FsWatchResponse", &WatchResponse{Path: "D:/workspace"}},
		{"FsUnwatchParams", &UnwatchParams{WatchID: "watch-schema"}},
		{"FsUnwatchResponse", &UnwatchResponse{}},
		{"FsChangedNotification", &ChangedNotification{WatchID: "watch-schema", ChangedPaths: []string{"D:/workspace/note.txt"}}},
		{"WindowsSandboxReadinessResponse", &sandboxpkg.WindowsReadinessResponse{Status: sandboxpkg.WindowsReadinessNotConfigured}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.typeName, func(t *testing.T) {
			requireJSONMatchesRustSchema(t, root, tc.typeName, tc.value)
		})
	}
}

func sampleRustSchemaThreadItemPayload(item ThreadItem) ThreadItemPayload {
	data, err := json.Marshal(&item)
	if err != nil {
		panic(err)
	}
	var payload ThreadItemPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		panic(err)
	}
	return payload
}

func sampleRustSchemaTurnWithAllThreadItems() Turn {
	turn := sampleRustSchemaTurn(TurnStatusCompleted)
	turn.Items = sampleRustSchemaThreadItems()
	return turn
}

func sampleRustSchemaThreadItems() []ThreadItem {
	processID := "process-schema"
	exitCode := int64(0)
	durationMS := int64(42)
	movePath := "D:/workspace/old.txt"
	reasoningEffort := "medium"
	message := "ready"
	return []ThreadItem{
		{
			ID:   "item-user",
			Type: "userMessage",
			Data: map[string]any{
				"clientId": "client-schema",
				"content": []any{
					map[string]any{"type": "text", "text": "hello", "text_elements": []any{}},
					map[string]any{"type": "image", "url": "data:image/png;base64,AA==", "detail": "auto"},
					map[string]any{"type": "localImage", "path": "D:/workspace/image.png"},
				},
			},
		},
		{
			ID:   "item-hook",
			Type: "hookPrompt",
			Data: map[string]any{
				"fragments": []any{
					map[string]any{"text": "hook says hello", "hookRunId": "hook-run-schema"},
				},
			},
		},
		{
			ID:   "item-agent",
			Type: "agentMessage",
			Text: "answer",
			Data: map[string]any{
				"phase": "final_answer",
				"memoryCitation": map[string]any{
					"entries":   []any{map[string]any{"path": "AGENTS.md", "lineStart": float64(1), "lineEnd": float64(2), "note": "memory"}},
					"threadIds": []any{"thread-schema"},
				},
			},
		},
		{ID: "item-plan", Type: "plan", Text: "plan text"},
		{
			ID:   "item-reasoning",
			Type: "reasoning",
			Data: map[string]any{
				"summary": []any{"summary"},
				"content": []any{"private reasoning redacted"},
			},
		},
		{
			ID:   "item-command",
			Type: "commandExecution",
			Data: map[string]any{
				"command":          "rg schema",
				"cwd":              "D:/workspace",
				"processId":        processID,
				"source":           string(CommandExecutionSourceAgent),
				"status":           string(CommandExecutionCompleted),
				"commandActions":   []any{map[string]any{"type": "search", "command": "rg schema", "query": "schema", "path": "D:/workspace"}},
				"aggregatedOutput": "ok\n",
				"exitCode":         exitCode,
				"durationMs":       durationMS,
			},
		},
		{
			ID:   "item-file",
			Type: "fileChange",
			Data: map[string]any{
				"status": string(PatchApplyCompleted),
				"changes": []any{
					map[string]any{"path": "D:/workspace/new.txt", "kind": map[string]any{"type": "add"}, "diff": "+hello\n"},
					map[string]any{"path": "D:/workspace/old.txt", "kind": map[string]any{"type": "delete"}, "diff": "-old\n"},
					map[string]any{"path": "D:/workspace/move.txt", "kind": map[string]any{"type": "update", "move_path": movePath}, "diff": "@@\n"},
				},
			},
		},
		{
			ID:   "item-mcp",
			Type: "mcpToolCall",
			Data: map[string]any{
				"server":    "docs",
				"tool":      "search",
				"status":    "completed",
				"arguments": map[string]any{"query": "schema"},
				"appContext": map[string]any{
					"connectorId": "connector-schema",
					"linkId":      "link-schema",
					"resourceUri": "mcp://docs/schema",
					"appName":     "Docs",
					"templateId":  "template-schema",
					"actionName":  "search",
				},
				"pluginId":   "plugin-schema",
				"result":     map[string]any{"content": []any{map[string]any{"type": "text", "text": "result"}}, "structuredContent": map[string]any{"ok": true}, "_meta": map[string]any{"trace": "schema"}},
				"durationMs": durationMS,
			},
		},
		{
			ID:   "item-dynamic",
			Type: "dynamicToolCall",
			Data: map[string]any{
				"namespace":    "web",
				"tool":         "open",
				"arguments":    map[string]any{"url": "https://example.test"},
				"status":       "completed",
				"contentItems": []any{map[string]any{"type": "inputText", "text": "opened"}, map[string]any{"type": "inputImage", "imageUrl": "data:image/png;base64,AA=="}},
				"success":      true,
				"durationMs":   durationMS,
			},
		},
		{
			ID:   "item-collab",
			Type: "collabAgentToolCall",
			Data: map[string]any{
				"tool":              string(CollabAgentToolSpawnAgent),
				"status":            string(CollabAgentToolCallCompleted),
				"senderThreadId":    "thread-parent",
				"receiverThreadIds": []any{"thread-child"},
				"prompt":            "work on schema",
				"model":             "gpt-5",
				"reasoningEffort":   reasoningEffort,
				"agentsStates": map[string]any{
					"thread-child": map[string]any{"status": string(CollabAgentStatusCompleted), "message": message},
				},
			},
		},
		{
			ID:   "item-subagent",
			Type: "subAgentActivity",
			Data: map[string]any{"kind": "started", "agentThreadId": "thread-child", "agentPath": "agents/research"},
		},
		{ID: "item-web", Type: "webSearch", Text: "schema", Data: map[string]any{"action": map[string]any{"type": "search", "query": "schema", "queries": []any{"schema", "protocol"}}}},
		{ID: "item-image-view", Type: "imageView", Data: map[string]any{"path": "D:/workspace/image.png"}},
		{ID: "item-sleep", Type: "sleep", Data: map[string]any{"durationMs": durationMS}},
		{ID: "item-image-generation", Type: "imageGeneration", Data: map[string]any{"status": "completed", "revisedPrompt": "a sharper prompt", "result": "image-result", "transparentBackground": true, "savedPath": "D:/workspace/generated.png"}},
		{ID: "item-enter-review", Type: "enteredReviewMode", Text: "review started"},
		{ID: "item-exit-review", Type: "exitedReviewMode", Text: "review ended"},
		{ID: "item-compact", Type: "contextCompaction"},
	}
}

func sampleRustSchemaThreadStartResponse() *ThreadStartResponse {
	return &ThreadStartResponse{
		Thread:                sampleRustSchemaThread(),
		CWD:                   "D:/workspace",
		RuntimeWorkspaceRoots: []string{},
		InstructionSources:    []string{},
		Model:                 "gpt-5",
		ModelProvider:         "openai",
	}
}

func sampleRustSchemaThreadListResponse() *ThreadListResponse {
	next := "thread-next"
	backwards := "thread-backwards"
	return &ThreadListResponse{
		Data:            []Thread{*sampleRustSchemaThreadWithTurns()},
		NextCursor:      &next,
		BackwardsCursor: &backwards,
	}
}

func sampleRustSchemaThreadLoadedListResponse() *ThreadLoadedListResponse {
	next := "thread-next"
	return &ThreadLoadedListResponse{
		Data:       []string{"thread-schema"},
		NextCursor: &next,
	}
}

func sampleRustSchemaThreadResumeResponse() *ThreadResumeResponse {
	next := "turn-next"
	backwards := "turn-backwards"
	return &ThreadResumeResponse{
		Thread: sampleRustSchemaThreadWithTurns(),
		InitialTurnsPage: &TurnsPage{
			Data:            []Turn{sampleRustSchemaTurn(TurnStatusCompleted)},
			NextCursor:      &next,
			BackwardsCursor: &backwards,
		},
		CWD:                   "D:/workspace",
		RuntimeWorkspaceRoots: []string{},
		InstructionSources:    []string{},
		Model:                 "gpt-5",
		ModelProvider:         "openai",
	}
}

func sampleRustSchemaThreadForkResponse() *ThreadForkResponse {
	return &ThreadForkResponse{
		Thread:                sampleRustSchemaThreadWithTurns(),
		CWD:                   "D:/workspace",
		RuntimeWorkspaceRoots: []string{},
		InstructionSources:    []string{},
		Model:                 "gpt-5",
		ModelProvider:         "openai",
	}
}

func sampleRustSchemaThreadWithTurns() *Thread {
	thread := *sampleRustSchemaThread()
	thread.Turns = []Turn{sampleRustSchemaTurn(TurnStatusCompleted)}
	return &thread
}

func sampleRustSchemaThread() *Thread {
	now := int64(1700000000000)
	return &Thread{
		ID:            "thread-schema",
		SessionID:     "session-schema",
		Preview:       "schema sample",
		Ephemeral:     false,
		HistoryMode:   ThreadHistoryLegacy,
		ModelProvider: "openai",
		CreatedAt:     now,
		UpdatedAt:     now,
		RecencyAt:     &now,
		Status:        IdleStatus(),
		CWD:           "D:/workspace",
		CLIVersion:    "0.0.0",
		Source:        SessionSourceAppServer,
		Turns:         []Turn{},
	}
}

func sampleRustSchemaTurn(status TurnStatus) Turn {
	now := int64(1700000000000)
	return Turn{
		ID:          "turn-schema",
		Items:       []ThreadItem{},
		ItemsView:   TurnItemsFull,
		Status:      status,
		StartedAt:   &now,
		CompletedAt: &now,
		DurationMS:  &now,
	}
}

func sampleRustSchemaGoal() Goal {
	now := int64(1700000000)
	return Goal{
		ThreadID:        "thread-schema",
		Objective:       "finish schema parity",
		Status:          GoalActive,
		TokensUsed:      12,
		TimeUsedSeconds: 3,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func TestAppServerControlSocketPath(t *testing.T) {
	path := AppServerControlSocketPath("D:/codex-home")
	if !strings.Contains(path, "app-server-control") || !strings.Contains(path, "app-server-control.sock") {
		t.Fatalf("path = %q", path)
	}
}

func rustAppServerProtocolSchemaRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_APP_SERVER_PROTOCOL_SCHEMA"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs", "app-server-protocol", "schema", "json"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs", "app-server-protocol", "schema", "json"),
		filepath.Join("..", "codex-main", "codex-rs", "app-server-protocol", "schema", "json"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "ClientRequest.json")); err == nil {
			return abs
		}
	}
	t.Skip("Rust app-server-protocol schema fixtures not found; set CODEX_RUST_APP_SERVER_PROTOCOL_SCHEMA")
	return ""
}

func rustAppServerProtocolTypeScriptRoot(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("CODEX_RUST_APP_SERVER_PROTOCOL_TYPESCRIPT"); env != "" {
		abs, err := filepath.Abs(env)
		if err == nil {
			if _, statErr := os.Stat(filepath.Join(abs, "ClientRequest.ts")); statErr == nil {
				return abs
			}
		}
	}
	jsonRoot := rustAppServerProtocolSchemaRoot(t)
	candidate := filepath.Join(filepath.Dir(jsonRoot), "typescript")
	if _, err := os.Stat(filepath.Join(candidate, "ClientRequest.ts")); err == nil {
		return candidate
	}
	t.Skip("Rust app-server-protocol TypeScript fixtures not found; set CODEX_RUST_APP_SERVER_PROTOCOL_TYPESCRIPT")
	return ""
}

func rustAppServerProtocolRustRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "Cargo.toml")); err == nil {
			return abs
		}
	}
	t.Skip("Rust root not found; set CODEX_RUST_ROOT")
	return ""
}

func rustMethodsFromCommon(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	re := regexp.MustCompile(`Method[A-Za-z0-9_]+\s+Method\s+=\s+"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			values = append(values, match[1])
		}
	}
	sort.Strings(values)
	return values
}

type rustProtocolUnionSchema struct {
	OneOf []rustProtocolVariant `json:"oneOf"`
}

type rustProtocolVariant struct {
	Properties struct {
		Method struct {
			Enum []string `json:"enum"`
		} `json:"method"`
		Params *struct {
			Ref string `json:"$ref"`
		} `json:"params"`
	} `json:"properties"`
}

func requireProtocolMethodSetMatchesRustFixture(t *testing.T, path string, actual []ProtocolMethod) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var schema rustProtocolUnionSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	expected := map[string]string{}
	for _, variant := range schema.OneOf {
		if len(variant.Properties.Method.Enum) == 0 {
			continue
		}
		method := variant.Properties.Method.Enum[0]
		params := ""
		if variant.Properties.Params != nil && variant.Properties.Params.Ref != "" {
			params = filepath.Base(variant.Properties.Params.Ref)
		}
		expected[method] = params
	}
	actualByMethod := map[string]ProtocolMethod{}
	for _, method := range actual {
		actualByMethod[method.Method] = method
	}
	var diffs []string
	for method, params := range expected {
		found, ok := actualByMethod[method]
		if !ok {
			diffs = append(diffs, "missing method "+method)
			continue
		}
		if found.Params != params {
			diffs = append(diffs, "method "+method+" params = "+found.Params+", want "+params)
		}
	}
	for method := range actualByMethod {
		if _, ok := expected[method]; !ok {
			diffs = append(diffs, "extra method "+method)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%s differs from Rust fixture:\n%s", filepath.Base(path), strings.Join(diffs, "\n"))
	}
}

var rustTypeScriptMethodPattern = regexp.MustCompile(`\{\s*"method":\s*"([^"]+)"[^{}]*?(?:"params"|params)\??:\s*([^,\s}|]+)`)

func requireTypeScriptMethodSetMatchesRustFixture(t *testing.T, path string, actual []ProtocolMethod) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	expected := rustTypeScriptMethodParams(string(data))
	if len(expected) == 0 {
		t.Fatalf("%s did not contain TypeScript request/notification variants", path)
	}
	actualByMethod := map[string]ProtocolMethod{}
	for _, method := range actual {
		actualByMethod[method.Method] = method
	}
	var diffs []string
	for method, params := range expected {
		found, ok := actualByMethod[method]
		if !ok {
			diffs = append(diffs, "missing method "+method)
			continue
		}
		if found.Params != params {
			diffs = append(diffs, "method "+method+" params = "+found.Params+", want "+params)
		}
	}
	for method := range actualByMethod {
		if _, ok := expected[method]; !ok {
			diffs = append(diffs, "extra method "+method)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%s differs from Rust TypeScript fixture:\n%s", filepath.Base(path), strings.Join(diffs, "\n"))
	}
}

func rustTypeScriptMethodParams(content string) map[string]string {
	out := map[string]string{}
	for _, match := range rustTypeScriptMethodPattern.FindAllStringSubmatch(content, -1) {
		method := match[1]
		params := match[2]
		if params == "undefined" {
			params = ""
		}
		out[method] = params
	}
	return out
}

func requireProtocolSchemaTypesHaveRustFixtures(t *testing.T, root string, methods []ProtocolMethod) {
	t.Helper()
	var missing []string
	for _, method := range methods {
		for _, typeName := range []string{method.Params, method.Result} {
			if typeName == "" || rustSchemaFixtureExists(root, typeName) {
				continue
			}
			missing = append(missing, method.Method+" references missing fixture type "+typeName)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("schema references Rust fixture types that do not exist:\n%s", strings.Join(missing, "\n"))
	}
}

func rustSchemaFixtureExists(root string, typeName string) bool {
	if _, ok := rustSchemaFixturePath(root, typeName); ok {
		return true
	}
	_, _, ok := rustExperimentalSchemaFixture(root, typeName)
	return ok
}

func rustSchemaFixtureNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", root, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".ts") {
			names = append(names, strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".ts"))
		}
	}
	sort.Strings(names)
	return names
}

func missingSchemaFixtureCoverage(expected []string, schema *ProtocolSchema) []string {
	covered := map[string]bool{}
	for _, method := range append(append([]ProtocolMethod{}, schema.Methods...), append(append([]ProtocolMethod{}, schema.ServerRequests...), schema.Notifications...)...) {
		if method.Params != "" {
			covered[method.Params] = true
		}
		if method.Result != "" {
			covered[method.Result] = true
		}
	}
	for _, typ := range schema.Types {
		covered[typ.Name] = true
	}
	missing := []string{}
	for _, name := range expected {
		if strings.HasSuffix(name, ".schemas") || strings.HasSuffix(name, "Request") || strings.HasSuffix(name, "Response") || strings.HasSuffix(name, "Notification") || name == "RequestId" || name == "JSONRPCMessage" || name == "JSONRPCError" || name == "JSONRPCErrorError" || name == "ClientRequest" || name == "ClientNotification" || name == "ServerRequest" || name == "ServerNotification" {
			continue
		}
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

func rustSchemaFixturePath(root string, typeName string) (string, bool) {
	for _, dir := range []string{"", "v1", "v2"} {
		path := filepath.Join(root, dir, typeName+".json")
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func rustExperimentalSchemaFixture(root string, typeName string) ([]byte, string, bool) {
	path := filepath.Clean(filepath.Join(root, "..", "precomputed", "app-server-exports-experimental.json.zst"))
	compressed, err := os.ReadFile(path)
	if err != nil {
		return nil, "", false
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, "", false
	}
	decompressed, err := decoder.DecodeAll(compressed, nil)
	decoder.Close()
	if err != nil {
		return nil, "", false
	}
	var exports struct {
		JSONSchema map[string]string `json:"json_schema"`
	}
	if err := json.Unmarshal(decompressed, &exports); err != nil {
		return nil, "", false
	}
	for _, name := range []string{typeName + ".json", "v1/" + typeName + ".json", "v2/" + typeName + ".json"} {
		if schema, ok := exports.JSONSchema[name]; ok {
			return []byte(schema), path + ":" + name, true
		}
	}
	return nil, "", false
}

type rustJSONSchemaRequired struct {
	Required []string `json:"required"`
}

type rustJSONSchemaNode struct {
	Definitions          map[string]json.RawMessage `json:"definitions"`
	Ref                  string                     `json:"$ref"`
	Type                 json.RawMessage            `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	Enum                 []json.RawMessage          `json:"enum"`
	OneOf                []json.RawMessage          `json:"oneOf"`
	AnyOf                []json.RawMessage          `json:"anyOf"`
	AllOf                []json.RawMessage          `json:"allOf"`
	Items                json.RawMessage            `json:"items"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

func requireJSONMatchesRustSchema(t *testing.T, root string, typeName string, value any) {
	t.Helper()
	schema := rustSchemaForType(t, root, typeName)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", typeName, err)
	}
	decoded, err := decodeRustSchemaJSONValue(data)
	if err != nil {
		t.Fatalf("decode marshaled %s object error = %v; json = %s", typeName, err, string(data))
	}
	if problems := validateRustSchemaValue(schema, schema.Raw, decoded, "$"); len(problems) > 0 {
		t.Fatalf("%s JSON does not match Rust schema:\n%s\njson = %s", typeName, strings.Join(problems, "\n"), string(data))
	}
}

type rustSchemaDocument struct {
	Raw         json.RawMessage
	Definitions map[string]json.RawMessage
}

func rustSchemaForType(t *testing.T, root string, typeName string) *rustSchemaDocument {
	t.Helper()
	path, ok := rustSchemaFixturePath(root, typeName)
	var data []byte
	var err error
	if ok {
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
	} else {
		data, path, ok = rustExperimentalSchemaFixture(root, typeName)
		if !ok {
			t.Fatalf("Rust schema fixture for %s not found under %s or the experimental precomputed exports", typeName, root)
		}
	}
	var node rustJSONSchemaNode
	if err := json.Unmarshal(data, &node); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return &rustSchemaDocument{
		Raw:         append(json.RawMessage(nil), data...),
		Definitions: node.Definitions,
	}
}

func rustSchemaRequiredFields(t *testing.T, root string, typeName string) []string {
	t.Helper()
	path, ok := rustSchemaFixturePath(root, typeName)
	var data []byte
	var err error
	if ok {
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
	} else {
		data, path, ok = rustExperimentalSchemaFixture(root, typeName)
		if !ok {
			t.Fatalf("Rust schema fixture for %s not found under %s or the experimental precomputed exports", typeName, root)
		}
	}
	var schema rustJSONSchemaRequired
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return append([]string(nil), schema.Required...)
}

func validateRustSchemaValue(doc *rustSchemaDocument, raw json.RawMessage, value any, path string) []string {
	if len(raw) == 0 {
		return nil
	}
	switch strings.TrimSpace(string(raw)) {
	case "true":
		return nil
	case "false":
		return []string{fmt.Sprintf("%s: boolean schema false rejects all values", path)}
	}
	var node rustJSONSchemaNode
	if err := json.Unmarshal(raw, &node); err != nil {
		return []string{fmt.Sprintf("%s: invalid schema node: %v", path, err)}
	}
	if node.Ref != "" {
		refRaw, ok := resolveRustSchemaRef(doc, node.Ref)
		if !ok {
			return []string{fmt.Sprintf("%s: unresolved schema ref %s", path, node.Ref)}
		}
		return validateRustSchemaValue(doc, refRaw, value, path)
	}
	if len(node.AllOf) > 0 {
		var problems []string
		for _, child := range node.AllOf {
			problems = append(problems, validateRustSchemaValue(doc, child, value, path)...)
		}
		if len(problems) > 0 {
			return problems
		}
	}
	if len(node.OneOf) > 0 {
		if ok := rustSchemaAnyBranchMatches(doc, node.OneOf, value, path); !ok {
			return []string{fmt.Sprintf("%s: value does not match any oneOf branch", path)}
		}
	}
	if len(node.AnyOf) > 0 {
		if ok := rustSchemaAnyBranchMatches(doc, node.AnyOf, value, path); !ok {
			return []string{fmt.Sprintf("%s: value does not match any anyOf branch", path)}
		}
	}
	if len(node.Enum) > 0 && !rustSchemaEnumContains(node.Enum, value) {
		return []string{fmt.Sprintf("%s: value %s is not in enum", path, rustSchemaValueForMessage(value))}
	}
	types := rustSchemaTypes(node.Type)
	if len(types) > 0 {
		if !rustSchemaValueMatchesAnyType(value, types) {
			return []string{fmt.Sprintf("%s: value type %s does not match schema type %s", path, rustSchemaJSONType(value), strings.Join(types, "|"))}
		}
		if value == nil {
			return nil
		}
	}

	var problems []string
	object, isObject := value.(map[string]any)
	if len(node.Properties) > 0 || len(node.Required) > 0 {
		if !isObject {
			return []string{fmt.Sprintf("%s: value type %s does not match object properties", path, rustSchemaJSONType(value))}
		}
		for _, field := range node.Required {
			if _, ok := object[field]; !ok {
				problems = append(problems, fmt.Sprintf("%s.%s: missing required field", path, field))
			}
		}
		for field, propertySchema := range node.Properties {
			if fieldValue, ok := object[field]; ok {
				problems = append(problems, validateRustSchemaValue(doc, propertySchema, fieldValue, path+"."+field)...)
			}
		}
		problems = append(problems, validateRustSchemaAdditionalProperties(doc, node, object, path)...)
	}
	if len(node.Items) > 0 {
		array, ok := value.([]any)
		if !ok {
			return append(problems, fmt.Sprintf("%s: value type %s does not match array items", path, rustSchemaJSONType(value)))
		}
		for i, item := range array {
			problems = append(problems, validateRustSchemaValue(doc, node.Items, item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return problems
}

func validateRustSchemaAdditionalProperties(doc *rustSchemaDocument, node rustJSONSchemaNode, object map[string]any, path string) []string {
	if len(node.AdditionalProperties) == 0 {
		return nil
	}
	var allow bool
	if err := json.Unmarshal(node.AdditionalProperties, &allow); err == nil {
		if allow {
			return nil
		}
		var problems []string
		for field := range object {
			if _, ok := node.Properties[field]; !ok {
				problems = append(problems, fmt.Sprintf("%s.%s: additional property is not allowed", path, field))
			}
		}
		sort.Strings(problems)
		return problems
	}
	var problems []string
	for field, fieldValue := range object {
		if _, ok := node.Properties[field]; ok {
			continue
		}
		problems = append(problems, validateRustSchemaValue(doc, node.AdditionalProperties, fieldValue, path+"."+field)...)
	}
	return problems
}

func rustSchemaAnyBranchMatches(doc *rustSchemaDocument, branches []json.RawMessage, value any, path string) bool {
	for _, branch := range branches {
		if len(validateRustSchemaValue(doc, branch, value, path)) == 0 {
			return true
		}
	}
	return false
}

func resolveRustSchemaRef(doc *rustSchemaDocument, ref string) (json.RawMessage, bool) {
	const prefix = "#/definitions/"
	if doc == nil || !strings.HasPrefix(ref, prefix) {
		return nil, false
	}
	name := strings.TrimPrefix(ref, prefix)
	name = strings.ReplaceAll(name, "~1", "/")
	name = strings.ReplaceAll(name, "~0", "~")
	raw, ok := doc.Definitions[name]
	return raw, ok
}

func rustSchemaTypes(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err == nil {
		return multiple
	}
	return nil
}

func rustSchemaValueMatchesAnyType(value any, types []string) bool {
	for _, schemaType := range types {
		if rustSchemaValueMatchesType(value, schemaType) {
			return true
		}
	}
	return false
}

func rustSchemaValueMatchesType(value any, schemaType string) bool {
	switch schemaType {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		if _, err := number.Int64(); err == nil {
			return true
		}
		value, err := number.Float64()
		return err == nil && value == float64(int64(value))
	default:
		return true
	}
}

func rustSchemaJSONType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case json.Number:
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func rustSchemaEnumContains(values []json.RawMessage, value any) bool {
	for _, raw := range values {
		decoded, err := decodeRustSchemaJSONValue(raw)
		if err == nil && reflect.DeepEqual(decoded, value) {
			return true
		}
	}
	return false
}

func rustSchemaValueForMessage(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func decodeRustSchemaJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func requireProtocolMethod(t *testing.T, methods []ProtocolMethod, method string, experimental bool) {
	t.Helper()
	found := findProtocolMethod(methods, method)
	if found == nil {
		t.Fatalf("missing protocol method %q", method)
	}
	if found.Experimental != experimental {
		t.Fatalf("protocol method %q experimental = %v, want %v", method, found.Experimental, experimental)
	}
}

func requireProtocolMethodAbsent(t *testing.T, methods []ProtocolMethod, method string) {
	t.Helper()
	if found := findProtocolMethod(methods, method); found != nil {
		t.Fatalf("protocol method %q was present", method)
	}
}

func requireProtocolSignature(t *testing.T, methods []ProtocolMethod, method string, params string, result string) {
	t.Helper()
	found := findProtocolMethod(methods, method)
	if found == nil {
		t.Fatalf("missing protocol method %q", method)
	}
	if found.Params != params || found.Result != result {
		t.Fatalf("protocol method %q signature = (%q, %q), want (%q, %q)", method, found.Params, found.Result, params, result)
	}
}

func findProtocolMethod(methods []ProtocolMethod, method string) *ProtocolMethod {
	for i := range methods {
		if methods[i].Method == method {
			return &methods[i]
		}
	}
	return nil
}

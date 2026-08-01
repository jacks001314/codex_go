package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/tool"
)

func TestMCPRuntimeCoordinatorKeepsThreadConfigIndependent(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	cfgA := mcpRuntimeConfigForTest("alpha", "alpha-mcp")
	cfgB := mcpRuntimeConfigForTest("beta", "beta-mcp")

	serviceA := router.mcpServiceForThread("thread-a", cfgA)
	serviceB := router.mcpServiceForThread("thread-b", cfgB)
	if serviceA == nil || serviceB == nil || serviceA == serviceB {
		t.Fatalf("thread services = %p/%p, want independent non-nil services", serviceA, serviceB)
	}
	assertMCPConfiguredServerNames(t, serviceA, []string{"alpha"})
	assertMCPConfiguredServerNames(t, serviceB, []string{"beta"})

	serviceA2 := router.mcpServiceForThread("thread-a", cfgA)
	if serviceA2 != serviceA {
		t.Fatalf("unchanged thread runtime was replaced: old=%p new=%p", serviceA, serviceA2)
	}
	assertMCPConfiguredServerNames(t, serviceB, []string{"beta"})
}

func TestMCPToolCallsStayBoundToEachThread(t *testing.T) {
	firstServer := newThreadBoundMCPTestServer(t, "first-runtime")
	defer firstServer.Close()
	secondServer := newThreadBoundMCPTestServer(t, "second-runtime")
	defer secondServer.Close()

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	makeConfig := func(url string) *config.Config {
		return &config.Config{Values: map[string]any{"mcp_servers": map[string]any{
			"shared": map[string]any{"url": url, "enabled": true},
		}}}
	}
	firstConfig := makeConfig(firstServer.URL)
	secondConfig := makeConfig(secondServer.URL)
	firstService := router.mcpServiceForThread("thread-first", firstConfig)
	secondService := router.mcpServiceForThread("thread-second", secondConfig)
	if firstService == nil || secondService == nil || firstService == secondService {
		t.Fatalf("thread services = %p/%p, want independent services", firstService, secondService)
	}

	makeExecutor := func(threadID string, cfg *config.Config, service *mcp.MCPService) *mcp.ToolExecutor {
		t.Helper()
		tools, _ := router.mcpRuntimeInputsForService(threadID, cfg, service)
		if len(tools) != 1 || tools[0].ServerName != "shared" || tools[0].Tool.Name != "echo" {
			t.Fatalf("runtime tools for %s = %#v", threadID, tools)
		}
		return mcp.NewToolExecutor(&mcp.ToolExecutorOptions{
			Service: service, ServerName: "shared", ThreadID: threadID,
			ToolInfo: &mcp.MCPToolInfo{Name: "echo", InputSchema: map[string]any{"type": "object"}},
		})
	}
	firstExecutor := makeExecutor("thread-first", firstConfig, firstService)
	secondExecutor := makeExecutor("thread-second", secondConfig, secondService)

	calls := []struct {
		executor *mcp.ToolExecutor
		callID   string
		marker   string
	}{
		{firstExecutor, "first-call", "first-runtime"},
		{secondExecutor, "second-call", "second-runtime"},
		{firstExecutor, "first-again", "first-runtime"},
	}
	for _, call := range calls {
		output, err := call.executor.Execute(context.Background(), &tool.Invocation{
			CallID:  call.callID,
			Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"` + call.callID + `"}`},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", call.callID, err)
		}
		structured, _ := output.Data["structuredContent"].(map[string]any)
		if structured["marker"] != call.marker {
			t.Fatalf("Execute(%s) structured content = %#v, want marker %q", call.callID, structured, call.marker)
		}
	}
}

func newThreadBoundMCPTestServer(t *testing.T, marker string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var rpc struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("Decode(%s) error = %v", marker, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		write := func(result any) {
			writeRuntimeRouterMCPResponse(t, w, rpc.ID, result)
		}
		switch rpc.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", marker)
			write(map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": marker, "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			write(map[string]any{"tools": []any{map[string]any{
				"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"},
			}}})
		case "tools/call":
			write(map[string]any{
				"content":           []any{},
				"structuredContent": map[string]any{"marker": marker},
			})
		default:
			write(map[string]any{})
		}
	}))
}

func TestMCPRuntimeRequirementsChangeRefreshesActiveThreadLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	alphaCommand := "alpha-mcp"
	betaCommand := "beta-mcp"
	values := map[string]any{"mcp_servers": map[string]any{
		"alpha": map[string]any{"command": alphaCommand, "enabled": true},
		"beta":  map[string]any{"command": betaCommand, "enabled": true},
	}}
	alphaRequirements := &config.ConfigRequirements{MCPServers: map[string]config.MCPServerRequirement{
		"alpha": {Identity: &config.MCPServerIdentity{Command: &alphaCommand}},
	}}
	firstConfig := &config.Config{Values: values, Requirements: alphaRequirements}
	service := router.mcpServiceForThread("thread-a", firstConfig)
	assertMCPConfiguredServerNames(t, service, []string{"alpha"})

	betaRequirements := &config.ConfigRequirements{MCPServers: map[string]config.MCPServerRequirement{
		"beta": {Identity: &config.MCPServerIdentity{Command: &betaCommand}},
	}}
	secondConfig := &config.Config{Values: values, Requirements: betaRequirements}
	refreshed := router.mcpServiceForThread("thread-a", secondConfig)
	if refreshed != service {
		t.Fatalf("requirements refresh replaced service handle: old=%p new=%p", service, refreshed)
	}
	assertMCPConfiguredServerNames(t, refreshed, []string{"beta"})
}

func TestMCPRuntimeAuthRevisionReplacesServiceLikeRust(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	cfg := mcpRuntimeConfigForTest("alpha", "alpha-mcp")
	var creates atomic.Int32
	newService := func(*config.Config) *mcp.MCPService {
		creates.Add(1)
		return mcp.NewMCPService(nil)
	}
	var updates atomic.Int32
	updateService := func(*mcp.MCPService, *config.Config) {
		updates.Add(1)
	}

	first := coordinator.serviceForThread("thread-a", cfg, 1, newService, updateService)
	second := coordinator.serviceForThread("thread-a", cfg, 2, newService, updateService)
	if first == nil || second == nil || first == second {
		t.Fatalf("auth revision services = %p/%p, want replacement", first, second)
	}
	if creates.Load() != 2 || updates.Load() != 0 {
		t.Fatalf("creates=%d updates=%d, want 2/0", creates.Load(), updates.Load())
	}
	if got := coordinator.serviceForThread("thread-a", cfg, 2, newService, updateService); got != second {
		t.Fatalf("unchanged auth revision replaced service: current=%p got=%p", second, got)
	}
}

func TestMCPRuntimeAuthRevisionClosesPreviousHTTPRuntime(t *testing.T) {
	deleted := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deleted <- request.Header.Get("Mcp-Session-Id")
			w.WriteHeader(http.StatusOK)
			return
		}
		var rpc struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("Decode MCP request error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch rpc.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "auth-session-1")
			writeRuntimeRouterMCPResponse(t, w, rpc.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "auth-runtime", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRuntimeRouterMCPResponse(t, w, rpc.ID, map[string]any{"tools": []any{}})
		case "resources/list":
			writeRuntimeRouterMCPResponse(t, w, rpc.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeRuntimeRouterMCPResponse(t, w, rpc.ID, map[string]any{"resourceTemplates": []any{}})
		default:
			writeRuntimeRouterMCPResponse(t, w, rpc.ID, map[string]any{})
		}
	}))
	defer server.Close()

	coordinator := newMCPRuntimeCoordinator()
	cfg := mcpRuntimeConfigForTest("remote", "unused")
	newService := func(*config.Config) *mcp.MCPService {
		return mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
			"remote": {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
		}})
	}
	first := coordinator.serviceForThread("thread-a", cfg, 1, newService, nil)
	if _, err := first.ListStatusChecked(&mcp.MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	second := coordinator.serviceForThread("thread-a", cfg, 2, newService, nil)
	if first == second {
		t.Fatal("auth revision reused previous HTTP runtime")
	}
	select {
	case sessionID := <-deleted:
		if sessionID != "auth-session-1" {
			t.Fatalf("closed session ID = %q, want auth-session-1", sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("previous HTTP runtime was not closed after auth revision change")
	}
	_ = coordinator.close()
}

func TestMCPServerReloadRefreshesFileBackedRequirementsForLoadedThreadLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := `[mcp_servers.alpha]
command = "alpha-mcp"
enabled = true

[mcp_servers.beta]
command = "beta-mcp"
enabled = true
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	writeRequirements := func(server, command string) {
		t.Helper()
		body := "[mcp_servers." + server + ".identity]\ncommand = \"" + command + "\"\n"
		if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile requirements error = %v", err)
		}
	}
	writeRequirements("alpha", "alpha-mcp")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), ThreadStatus: NewThreadStatusManager()})
	router.requireThreadStatus().UpsertThread("thread-a", false)
	first := router.mcpServiceForThread("thread-a", nil)
	assertMCPConfiguredServerNames(t, first, []string{"alpha"})

	writeRequirements("beta", "beta-mcp")
	if _, err := router.handleMCPServerRefresh(&Request{Method: MethodConfigMCPServerReload}); err != nil {
		t.Fatalf("handleMCPServerRefresh() error = %v", err)
	}
	second := router.mcpServiceForThread("thread-a", nil)
	if second != first {
		t.Fatalf("requirements reload replaced service handle: old=%p new=%p", first, second)
	}
	assertMCPConfiguredServerNames(t, second, []string{"beta"})
}

func TestMCPRuntimeExplicitRefreshAdvancesEveryThreadGeneration(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	serviceA := router.mcpServiceForThread("thread-a", mcpRuntimeConfigForTest("alpha", "alpha-mcp"))
	serviceB := router.mcpServiceForThread("thread-b", mcpRuntimeConfigForTest("beta", "beta-mcp"))
	beforeA, beforeB := serviceA.Generation(), serviceB.Generation()

	if _, err := router.handleMCPServerRefresh(nil); err != nil {
		t.Fatalf("handleMCPServerRefresh() error = %v", err)
	}
	if serviceA.Generation() != beforeA+1 || serviceB.Generation() != beforeB+1 {
		t.Fatalf("generations = %d/%d, want %d/%d", serviceA.Generation(), serviceB.Generation(), beforeA+1, beforeB+1)
	}
}

func TestMCPRuntimeInvalidationDuringStartupIsNotLost(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	cfg := mcpRuntimeConfigForTest("alpha", "alpha-mcp")
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan *mcp.MCPService, 1)
	var creates atomic.Int32
	go func() {
		firstDone <- coordinator.serviceForThread("thread-a", cfg, 1, func(*config.Config) *mcp.MCPService {
			creates.Add(1)
			close(entered)
			<-release
			return mcp.NewMCPService(nil)
		}, nil)
	}()
	<-entered
	invalidated := make(chan struct{})
	go func() {
		coordinator.invalidateAll()
		close(invalidated)
	}()
	close(release)
	first := <-firstDone
	<-invalidated
	second := coordinator.serviceForThread("thread-a", cfg, 1, func(*config.Config) *mcp.MCPService {
		creates.Add(1)
		return mcp.NewMCPService(nil)
	}, func(*mcp.MCPService, *config.Config) {
		creates.Add(1)
	})
	if first == nil || second == nil || first != second || creates.Load() != 2 {
		t.Fatalf("startup invalidation first=%p second=%p creates=%d", first, second, creates.Load())
	}
}

func TestMCPRuntimeBindingRevisionPreventsOrchestratorCacheCollision(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	cfg := mcpRuntimeConfigForTest("alpha", "alpha-mcp")
	firstService := router.mcpServiceForThread("thread-a", cfg)
	firstKey, firstCache := router.orchestratorSkillCacheForRuntime("thread-a", firstService)

	router.mcpRuntimes.invalidateThread("thread-a")
	secondService := router.mcpServiceForThread("thread-a", cfg)
	secondKey, secondCache := router.orchestratorSkillCacheForRuntime("thread-a", secondService)

	if firstService != secondService {
		t.Fatal("thread runtime handle changed after invalidation")
	}
	if secondService.Generation() <= 1 {
		t.Fatalf("service generation = %d, want runtime config publication to advance it", secondService.Generation())
	}
	if firstKey == secondKey || firstCache == secondCache {
		t.Fatalf("orchestrator cache collided across runtime replacement: keys=%q/%q caches=%p/%p", firstKey, secondKey, firstCache, secondCache)
	}
}

func TestMCPRuntimeConcurrentSameThreadBuildIsCoalesced(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	cfg := mcpRuntimeConfigForTest("alpha", "alpha-mcp")
	entered := make(chan struct{})
	release := make(chan struct{})
	var creates atomic.Int32
	const callers = 16
	results := make(chan *mcp.MCPService, callers)
	for range callers {
		go func() {
			results <- coordinator.serviceForThread("thread-a", cfg, 1, func(*config.Config) *mcp.MCPService {
				if creates.Add(1) == 1 {
					close(entered)
				}
				<-release
				return mcp.NewMCPService(nil)
			}, nil)
		}()
	}
	<-entered
	close(release)
	var first *mcp.MCPService
	for range callers {
		service := <-results
		if first == nil {
			first = service
		} else if service != first {
			t.Fatalf("coalesced services differ: first=%p current=%p", first, service)
		}
	}
	if creates.Load() != 1 {
		t.Fatalf("service creates = %d, want 1", creates.Load())
	}
}

func TestMCPRuntimeDifferentThreadsBuildInParallel(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	entered := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan struct{}, 2)
	for _, threadID := range []string{"thread-a", "thread-b"} {
		threadID := threadID
		go func() {
			_ = coordinator.serviceForThread(threadID, mcpRuntimeConfigForTest(threadID, threadID+"-mcp"), 1, func(*config.Config) *mcp.MCPService {
				entered <- threadID
				<-release
				return mcp.NewMCPService(nil)
			}, nil)
			done <- struct{}{}
		}()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case threadID := <-entered:
			seen[threadID] = true
		case <-time.After(time.Second):
			t.Fatal("different thread build was serialized")
		}
	}
	close(release)
	<-done
	<-done
	if !seen["thread-a"] || !seen["thread-b"] {
		t.Fatalf("parallel entries = %#v", seen)
	}
}

func TestMCPRuntimePrewarmRequestsAreCoalesced(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	done := make(chan struct{})
	var runs atomic.Int32
	run := func() {
		if runs.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
			return
		}
		close(done)
	}
	coordinator.schedulePrewarm("thread-a", run)
	<-firstEntered
	for range 10 {
		coordinator.schedulePrewarm("thread-a", run)
	}
	close(releaseFirst)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coalesced pending prewarm did not run")
	}
	if runs.Load() != 2 {
		t.Fatalf("prewarm runs = %d, want 2", runs.Load())
	}
}

func TestMCPRuntimeAuthChangeDuringPrewarmPublishesLatestRuntime(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	cfg := mcpRuntimeConfigForTest("alpha", "alpha-mcp")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	latestPublished := make(chan struct{})
	var desired atomic.Uint64
	desired.Store(1)
	var mu sync.Mutex
	createdRevisions := []uint64{}
	var latest *mcp.MCPService
	run := func() {
		revision := desired.Load()
		service := coordinator.serviceForThread("thread-a", cfg, revision, func(*config.Config) *mcp.MCPService {
			mu.Lock()
			createdRevisions = append(createdRevisions, revision)
			mu.Unlock()
			if revision == 1 {
				close(firstEntered)
				<-releaseFirst
			}
			return mcp.NewMCPService(nil)
		}, func(*mcp.MCPService, *config.Config) {})
		if revision == 3 {
			latest = service
			close(latestPublished)
		}
	}

	coordinator.schedulePrewarm("thread-a", run)
	<-firstEntered
	desired.Store(2)
	coordinator.invalidateAll()
	coordinator.schedulePrewarm("thread-a", run)
	desired.Store(3)
	coordinator.invalidateAll()
	coordinator.schedulePrewarm("thread-a", run)
	close(releaseFirst)
	select {
	case <-latestPublished:
	case <-time.After(time.Second):
		t.Fatal("latest auth runtime was not published")
	}
	mu.Lock()
	gotRevisions := append([]uint64(nil), createdRevisions...)
	mu.Unlock()
	if len(gotRevisions) != 2 || gotRevisions[0] != 1 || gotRevisions[1] != 3 {
		t.Fatalf("created auth revisions = %v, want [1 3]", gotRevisions)
	}
	var unexpectedCreates atomic.Int32
	if got := coordinator.serviceForThread("thread-a", cfg, 3, func(*config.Config) *mcp.MCPService {
		unexpectedCreates.Add(1)
		return mcp.NewMCPService(nil)
	}, nil); got != latest || unexpectedCreates.Load() != 0 {
		t.Fatalf("latest runtime not stable: latest=%p got=%p creates=%d", latest, got, unexpectedCreates.Load())
	}
}

func TestMCPRuntimeCloseThreadDuringStartupDoesNotRepublish(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan *mcp.MCPService, 1)
	go func() {
		done <- coordinator.serviceForThread("thread-a", mcpRuntimeConfigForTest("alpha", "alpha-mcp"), 1, func(*config.Config) *mcp.MCPService {
			close(entered)
			<-release
			return mcp.NewMCPService(nil)
		}, nil)
	}()
	<-entered
	if err := coordinator.closeThread("thread-a"); err != nil {
		t.Fatalf("closeThread() error = %v", err)
	}
	close(release)
	if service := <-done; service != nil {
		t.Fatalf("startup published after unload: %p", service)
	}
	if revision := coordinator.bindingRevision("thread-a", mcp.NewMCPService(nil)); revision != 0 {
		t.Fatalf("binding revision after unload = %d", revision)
	}
}

func TestMCPRuntimeCoordinatorCloseIsConcurrentAndIdempotent(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	for _, threadID := range []string{"thread-a", "thread-b"} {
		_ = coordinator.serviceForThread(threadID, mcpRuntimeConfigForTest(threadID, threadID+"-mcp"), 1, func(*config.Config) *mcp.MCPService {
			return mcp.NewMCPService(nil)
		}, nil)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- coordinator.close()
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent close error = %v", err)
		}
	}
}

func TestMCPRuntimeOpenAIFormCapabilityBroadcastReconnectsThreads(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	serviceA := router.mcpServiceForThread("thread-a", mcpRuntimeConfigForTest("alpha", "alpha-mcp"))
	serviceB := router.mcpServiceForThread("thread-b", mcpRuntimeConfigForTest("beta", "beta-mcp"))
	beforeA, beforeB := serviceA.Generation(), serviceB.Generation()

	router.mcpRuntimes.setOpenAIFormElicitationEnabled(true)

	if serviceA.Generation() != beforeA+1 || serviceB.Generation() != beforeB+1 {
		t.Fatalf("generations after capability broadcast = %d/%d, want %d/%d", serviceA.Generation(), serviceB.Generation(), beforeA+1, beforeB+1)
	}
}

func TestMCPElicitationHandlerUsesCurrentThreadAuthorityLikeRust(t *testing.T) {
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir()), ThreadExtras: extras})
	var reviews atomic.Int32
	handler := &appserverMCPElicitationHandler{
		authority: router.currentMCPElicitationAuthority,
		reviewer: guardianReviewerFunc(func(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error) {
			reviews.Add(1)
			return state.DecisionApproved, "", nil
		}),
	}
	request := guardianMCPRequestForAuthorityTest()

	onRequest := string(sandbox.ApprovalOnRequest)
	autoReview := "auto_review"
	readOnly := string(sandbox.SandboxReadOnly)
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{ThreadID: request.ThreadID, ApprovalPolicy: &onRequest, ApprovalsReviewer: &autoReview, SandboxPolicy: &readOnly}); err != nil {
		t.Fatalf("UpdateSettings(on-request) error = %v", err)
	}
	response, err := handler.HandleMCPElicitation(context.Background(), request)
	if err != nil || response.Action != mcp.MCPElicitationActionAccept || reviews.Load() != 1 {
		t.Fatalf("on-request response=%#v reviews=%d error=%v", response, reviews.Load(), err)
	}

	never := string(sandbox.ApprovalNever)
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{ThreadID: request.ThreadID, ApprovalPolicy: &never}); err != nil {
		t.Fatalf("UpdateSettings(never) error = %v", err)
	}
	response, err = handler.HandleMCPElicitation(context.Background(), request)
	if err != nil || response.Action != mcp.MCPElicitationActionDecline || reviews.Load() != 1 {
		t.Fatalf("never/read-only response=%#v reviews=%d error=%v", response, reviews.Load(), err)
	}

	fullAccess := string(sandbox.SandboxDangerFullAccess)
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{ThreadID: request.ThreadID, SandboxPolicy: &fullAccess}); err != nil {
		t.Fatalf("UpdateSettings(full-access) error = %v", err)
	}
	response, err = handler.HandleMCPElicitation(context.Background(), request)
	content, contentOK := response.Content.(map[string]any)
	if err != nil || response.Action != mcp.MCPElicitationActionAccept || response.Meta != nil || !contentOK || len(content) != 0 || reviews.Load() != 1 {
		t.Fatalf("never/full-access response=%#v reviews=%d error=%v", response, reviews.Load(), err)
	}
}

func mcpRuntimeConfigForTest(name string, command string) *config.Config {
	return &config.Config{Values: map[string]any{
		"mcp_servers": map[string]any{
			name: map[string]any{"command": command, "enabled": true},
		},
	}}
}

func assertMCPConfiguredServerNames(t *testing.T, service *mcp.MCPService, want []string) {
	t.Helper()
	statuses := service.ConfiguredStatuses()
	got := make([]string, 0, len(statuses))
	for _, status := range statuses {
		got = append(got, status.Name)
	}
	if len(got) != len(want) {
		t.Fatalf("configured servers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("configured servers = %v, want %v", got, want)
		}
	}
}

func guardianMCPRequestForAuthorityTest() *mcp.MCPElicitationRequest {
	return &mcp.MCPElicitationRequest{
		ServerName: "browser-use",
		ThreadID:   "thread-authority",
		TurnID:     "turn-authority",
		Method:     "elicitation/create",
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Meta: map[string]any{
			"codex_request_type":  "approval_request",
			"codex_approval_kind": "mcp_tool_call",
			"tool_name":           "access_browser_origin",
		},
	}
}

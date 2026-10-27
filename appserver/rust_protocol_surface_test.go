package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestRustProtocolMethodSurfaceAgainstGoConstants(t *testing.T) {
	root := rustAppserverRustRoot(t)
	methods := rustProtocolMethodsFromRust(t, filepath.Join(root, "app-server-protocol", "src", "protocol", "common.rs"))

	goMethods := map[string]bool{
		string(MethodInitialize):                             true,
		string(MethodThreadStart):                            true,
		string(MethodThreadResume):                           true,
		string(MethodThreadFork):                             true,
		string(MethodThreadArchive):                          true,
		string(MethodThreadUnarchive):                        true,
		string(MethodThreadDelete):                           true,
		string(MethodThreadIncrementElicitation):             true,
		string(MethodThreadDecrementElicitation):             true,
		string(MethodThreadSetName):                          true,
		string(MethodThreadNameSet):                          true,
		string(MethodThreadGoalSet):                          true,
		string(MethodThreadGoalGet):                          true,
		string(MethodThreadGoalClear):                        true,
		string(MethodThreadUnsubscribe):                      true,
		string(MethodThreadMemoryModeSet):                    true,
		string(MethodThreadMemoryRead):                       true,
		string(MethodMemoryReset):                            true,
		string(MethodUserVerificationStatus):                 true,
		string(MethodUserVerificationEnroll):                 true,
		string(MethodUserVerificationDelete):                 true,
		string(MethodUserVerificationVerify):                 true,
		string(MethodUserVerificationCancel):                 true,
		string(MethodThreadCompactStart):                     true,
		string(MethodThreadApproveGuardianDeniedAction):      true,
		string(MethodThreadMetadataUpdate):                   true,
		string(MethodThreadAttachmentAdd):                    true,
		string(MethodThreadAttachmentList):                   true,
		string(MethodThreadAttachmentOwnerList):              true,
		string(MethodThreadAttachmentRemove):                 true,
		string(MethodThreadSettingsUpdate):                   true,
		string(MethodThreadShellCommand):                     true,
		string(MethodThreadBackgroundTerminalsClean):         true,
		string(MethodThreadBackgroundTerminalsList):          true,
		string(MethodThreadBackgroundTerminalsTerminate):     true,
		string(MethodThreadRevert):                           true,
		string(MethodThreadList):                             true,
		string(MethodThreadSearch):                           true,
		string(MethodThreadSearchOccurrences):                true,
		string(MethodThreadLoadedList):                       true,
		string(MethodThreadRead):                             true,
		string(MethodThreadItemsList):                        true,
		string(MethodThreadTurnsList):                        true,
		string(MethodThreadInjectItems):                      true,
		string(MethodThreadRealtimeStart):                    true,
		string(MethodThreadRealtimeAppendAudio):              true,
		string(MethodThreadRealtimeAppendText):               true,
		string(MethodThreadRealtimeAppendSpeech):             true,
		string(MethodThreadRealtimeStop):                     true,
		string(MethodThreadRealtimeListVoices):               true,
		string(MethodTurnStart):                              true,
		string(MethodTurnSteer):                              true,
		string(MethodTurnSettingsUpdate):                     true,
		string(MethodTurnInterrupt):                          true,
		string(MethodReviewStart):                            true,
		string(MethodExperimentalFeatureList):                true,
		string(MethodExperimentalFeatureSet):                 true,
		string(MethodAppList):                                true,
		string(MethodAppRead):                                true,
		string(MethodAppInstalled):                           true,
		string(MethodGetAuthStatus):                          true,
		string(MethodGetConversationSummary):                 true,
		string(MethodGitDiffToRemote):                        true,
		string(MethodFuzzyFileSearch):                        true,
		string(MethodFuzzyFileSearchStart):                   true,
		string(MethodFuzzyFileSearchUpdate):                  true,
		string(MethodFuzzyFileSearchStop):                    true,
		string(MethodHooksList):                              true,
		string(MethodSkillsList):                             true,
		string(MethodSkillsExtraRootsSet):                    true,
		string(MethodSkillsConfigWrite):                      true,
		string(MethodMarketplaceAdd):                         true,
		string(MethodMarketplaceRemove):                      true,
		string(MethodMarketplaceUpgrade):                     true,
		string(MethodPluginList):                             true,
		string(MethodPluginInstalled):                        true,
		string(MethodPluginReconcile):                        true,
		string(MethodPluginRead):                             true,
		string(MethodPluginSkillRead):                        true,
		string(MethodPluginShareSave):                        true,
		string(MethodPluginShareUpdateTargets):               true,
		string(MethodPluginShareList):                        true,
		string(MethodPluginShareCheckout):                    true,
		string(MethodPluginShareDelete):                      true,
		string(MethodPluginInstall):                          true,
		string(MethodPluginUninstall):                        true,
		string(MethodModelList):                              true,
		string(MethodModelProviderCapabilitiesRead):          true,
		string(MethodPermissionProfileList):                  true,
		string(MethodCollaborationModeList):                  true,
		string(MethodMockExperimentalMethod):                 true,
		string(MethodMCPServerOauthLogin):                    true,
		string(MethodMCPServerOauthCancel):                   true,
		string(MethodMCPServerRefresh):                       true,
		string(MethodConfigMCPServerReload):                  true,
		string(MethodMCPServerStatusList):                    true,
		string(MethodMCPServerResourceRead):                  true,
		string(MethodMCPServerToolCall):                      true,
		string(MethodFSReadFile):                             true,
		string(MethodFSWriteFile):                            true,
		string(MethodFSCreateDirectory):                      true,
		string(MethodFSGetMetadata):                          true,
		string(MethodFSReadDirectory):                        true,
		string(MethodFSRemove):                               true,
		string(MethodFSCopy):                                 true,
		string(MethodFSWatch):                                true,
		string(MethodFSUnwatch):                              true,
		string(MethodRemoteControlEnable):                    true,
		string(MethodRemoteControlDisable):                   true,
		string(MethodRemoteControlStatusRead):                true,
		string(MethodRemoteControlPairingStart):              true,
		string(MethodRemoteControlPairingStatus):             true,
		string(MethodRemoteControlClientsList):               true,
		string(MethodRemoteControlClientsRevoke):             true,
		string(MethodEnvironmentAdd):                         true,
		string(MethodEnvironmentInfo):                        true,
		string(MethodEnvironmentStatus):                      true,
		string(MethodWindowsSandboxSetupStart):               true,
		string(MethodWindowsSandboxReadiness):                true,
		string(MethodFeedbackUpload):                         true,
		string(MethodFeedbackSave):                           true,
		string(MethodConfigRead):                             true,
		string(MethodConfigValueWrite):                       true,
		string(MethodConfigBatchWrite):                       true,
		string(MethodConfigRequirementsRead):                 true,
		string(MethodExternalAgentConfigDetect):              true,
		string(MethodExternalAgentConfigImport):              true,
		string(MethodExternalAgentConfigImportHistoryRecord): true,
		string(MethodExternalAgentConfigImportHistoriesRead): true,
		string(MethodLoginAccount):                           true,
		string(MethodBedrockDiscover):                        true,
		string(MethodBedrockSetup):                           true,
		string(MethodGcpSetup):                               true,
		string(MethodBedrockCheckGovCloudRequirements):       true,
		string(MethodCancelLoginAccount):                     true,
		string(MethodAccountSessionsAdd):                     true,
		string(MethodAccountSessionsList):                    true,
		string(MethodAccountSessionsLogout):                  true,
		string(MethodAccountSessionsSwitch):                  true,
		string(MethodLogoutAccount):                          true,
		string(MethodGetAccount):                             true,
		string(MethodGetAccountRateLimits):                   true,
		string(MethodConsumeAccountRateLimitResetCredit):     true,
		string(MethodGetAccountTokenUsage):                   true,
		string(MethodGetWorkspaceMessages):                   true,
		string(MethodSendAddCreditsNudgeEmail):               true,
		string(MethodProcessSpawn):                           true,
		string(MethodProcessWriteStdin):                      true,
		string(MethodProcessKill):                            true,
		string(MethodProcessResizePty):                       true,
		string(MethodCommandExec):                            true,
		string(MethodCommandExecWrite):                       true,
		string(MethodCommandExecTerminate):                   true,
		string(MethodCommandExecResize):                      true,
		string(MethodGatewayOAuthRead):                       true,
		string(MethodGatewayOAuthLogin):                      true,
		string(MethodGatewayOAuthCancel):                     true,
		string(MethodMCPServerEventStreamStart):              true,
		string(MethodMCPServerEventStreamStop):               true,
		string(MethodMemoryStatus):                           true,
		string(MethodRolloutCompress):                        true,
		string(MethodServerDiagnostics):                      true,
		string(MethodThreadPredictionRequest):                true,
		string(MethodThreadSample):                           true,
		string(MethodThreadSampleCancel):                     true,
		string(MethodThreadQueueAdd):                         true,
		string(MethodThreadQueueList):                        true,
		string(MethodThreadQueueUpdate):                      true,
		string(MethodThreadQueueDelete):                      true,
		string(MethodThreadQueueReorder):                     true,
		string(MethodThreadQueueStart):                       true,
		string(MethodThreadSectionMove):                      true,
		string(MethodThreadSectionList):                      true,
		string(MethodThreadSectionCreate):                    true,
		string(MethodThreadSectionUpdate):                    true,
		string(MethodThreadSectionDelete):                    true,
		string(MethodProjectList):                            true,
		string(MethodProjectRead):                            true,
		string(MethodProjectCreate):                          true,
		string(MethodProjectImport):                          true,
		string(MethodProjectUpdate):                          true,
		string(MethodProjectMove):                            true,
		string(MethodProjectDelete):                          true,
		string(MethodThreadItemsRead):                        true,
		string(MethodThreadReadStateUpdate):                  true,
		string(MethodThreadTimelineList):                     true,
		string(MethodPluginSearch):                           true,
	}
	knownMissing := map[string]bool{}

	var missingMethods []string
	for _, method := range methods {
		if !goMethods[method] && !knownMissing[method] {
			missingMethods = append(missingMethods, method)
		}
	}
	if len(missingMethods) > 0 {
		t.Fatalf("Go appserver protocol gap: Rust methods without a Go constant: %q", missingMethods)
	}
	for method := range knownMissing {
		if !containsString(methods, method) {
			t.Fatalf("known missing Rust method %q no longer exists upstream", method)
		}
		if goMethods[method] {
			t.Fatalf("Rust method %q is implemented in Go; remove it from knownMissing", method)
		}
	}
}

func TestRustProtocolNotificationSurfaceAgainstGoConstants(t *testing.T) {
	root := rustAppserverRustRoot(t)
	notifications := rustProtocolNotificationsFromRust(t, filepath.Join(root, "app-server-protocol", "src", "protocol", "common.rs"))
	// The Go side is derived from the constants declared in protocol.go rather
	// than from a hand-maintained map: the previous map listed 36 of the 87
	// `NotificationMethod` constants, so it reported phantom gaps and hid real
	// ones. `Name NotificationMethod = "value"` occurs only in that constant
	// block, so the parse is exact.
	goNotifications := goNotificationConstantsFromSource(t, ".")
	var missingNotifications []string
	for _, notification := range notifications {
		if !goNotifications[notification] {
			missingNotifications = append(missingNotifications, notification)
		}
	}
	if len(missingNotifications) > 0 {
		t.Fatalf("Go appserver protocol gap: Rust notifications without a Go constant: %q", missingNotifications)
	}
}

func rustProtocolMethodsFromRust(t *testing.T, path string) []string {
	t.Helper()
	data := readRustProtocolSource(t, path)
	start := strings.Index(data, "client_request_definitions! {")
	end := strings.Index(data, "server_request_definitions! {")
	if start < 0 || end <= start {
		t.Fatalf("could not locate Rust client request definitions in %s", path)
	}
	data = data[start:end]
	re := regexp.MustCompile(`(?:Method[A-Za-z0-9_]+\s+Method\s+=\s+|[A-Za-z0-9_]+\s*=>\s*)"([^"]+)"`)
	methods := dedupeSortedStrings(extractMatches(data, re))
	if len(methods) == 0 {
		t.Fatalf("parsed 0 client request methods from %s; the Rust macro shape changed and this guard would silently pass", path)
	}
	return methods
}

// goNotificationConstantsFromSource extracts the method literal of every
// `NotificationX NotificationMethod = "value"` constant declared in the Go
// app-server protocol package. The constants are spread over several files
// (protocol.go, notifications.go, projects.go, remote_control_protocol.go),
// so the package source is scanned. Deriving the set keeps the Rust
// notification guard fail-closed without a hand-maintained map that drifts
// behind the constants, and a parse that finds nothing fails loudly instead of
// passing vacuously.
func goNotificationConstantsFromSource(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}
	re := regexp.MustCompile(`[A-Za-z0-9_]+\s+NotificationMethod\s*=\s*"([^"]+)"`)
	out := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		for _, match := range re.FindAllStringSubmatch(string(data), -1) {
			out[match[1]] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("parsed 0 NotificationMethod constants from %s; this guard would silently pass", dir)
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func rustProtocolNotificationsFromRust(t *testing.T, path string) []string {
	t.Helper()
	data := readRustProtocolSource(t, path)
	start := strings.Index(data, "server_notification_definitions! {")
	end := strings.Index(data, "client_notification_definitions! {")
	if start < 0 || end <= start {
		t.Fatalf("could not locate Rust server notification definitions in %s", path)
	}
	data = data[start:end]
	// The macro body is `Variant => "method/name" (v2::Type),`. The previous
	// `Notification... NotificationMethod = "..."` shape never occurs in
	// common.rs, so this guard parsed 0 notifications and stayed vacuous.
	re := regexp.MustCompile(`[A-Za-z0-9_]+\s*=>\s*"([^"]+)"`)
	notifications := dedupeSortedStrings(extractMatches(data, re))
	if len(notifications) == 0 {
		t.Fatalf("parsed 0 server notifications from %s; the Rust macro shape changed and this guard would silently pass", path)
	}
	return notifications
}

func readRustProtocolSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func rustAppserverRustRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs"),
	}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append([]string{env}, candidates...)
	}
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "Cargo.toml")); err == nil {
			return abs
		}
	}
	t.Skip("Rust snapshot not found; set CODEX_RUST_ROOT")
	return ""
}

func extractMatches(source string, re *regexp.Regexp) []string {
	matches := re.FindAllStringSubmatch(source, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			values = append(values, match[1])
		}
	}
	sort.Strings(values)
	return values
}

func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for i, value := range values {
		if i == 0 || value != values[i-1] {
			out = append(out, value)
		}
	}
	return out
}

// TestRustAppServerProtocolFieldsAgainstGoLikeRust pins the app-server protocol
// fields from the upstream delta between 5f3180c793 and 18e28fe1b9 that the Go
// port models directly. It reads the Rust definitions and requires the matching
// Go field/tag to exist, so a Rust rename or removal fails instead of silently
// drifting.
//
// Rust #51157 (environment.rs): EnvironmentAddParams.skills -> EnvironmentSkillsParams.required
// Rust #49598 (thread.rs): ThreadGoalMutationOrigin + ThreadGoalSetParams/ClearParams.origin
// Rust #51217 (thread_data.rs): MisalignmentErrorDetails.review_target
func TestRustAppServerProtocolFieldsAgainstGoLikeRust(t *testing.T) {
	root := rustAppserverRustRoot(t)
	v2 := filepath.Join(root, "app-server-protocol", "src", "protocol", "v2")

	environment := readRustProtocolSource(t, filepath.Join(v2, "environment.rs"))
	for _, want := range []string{
		"pub skills: Option<EnvironmentSkillsParams>,",
		"pub struct EnvironmentSkillsParams {",
		"pub required: Option<Vec<String>>,",
	} {
		if !strings.Contains(environment, want) {
			t.Fatalf("Rust environment.rs no longer declares %q", want)
		}
	}

	thread := readRustProtocolSource(t, filepath.Join(v2, "thread.rs"))
	for _, want := range []string{
		"pub enum ThreadGoalMutationOrigin {",
		"pub origin: Option<ThreadGoalMutationOrigin>,",
	} {
		if !strings.Contains(thread, want) {
			t.Fatalf("Rust thread.rs no longer declares %q", want)
		}
	}
	if got := strings.Count(thread, "pub origin: Option<ThreadGoalMutationOrigin>,"); got != 2 {
		t.Fatalf("Rust thread.rs origin field count = %d, want 2 (set + clear)", got)
	}

	threadData := readRustProtocolSource(t, filepath.Join(v2, "thread_data.rs"))
	if !strings.Contains(threadData, "pub review_target: Option<String>,") {
		t.Fatalf("Rust thread_data.rs no longer declares the review_target field")
	}

	// Go side: decode Rust-shaped params so the fields are proven reachable.
	var envParams EnvironmentAddParams
	if err := json.Unmarshal([]byte(`{"environmentId":"remote-a","execServerUrl":"ws://127.0.0.1:8765","connectTimeoutMs":300000,"skills":{"required":["parity-skill"]}}`), &envParams); err != nil {
		t.Fatalf("decode EnvironmentAddParams: %v", err)
	}
	if envParams.Skills == nil || len(envParams.Skills.Required) != 1 || envParams.Skills.Required[0] != "parity-skill" {
		t.Fatalf("EnvironmentAddParams.skills = %#v, want required=[parity-skill]", envParams.Skills)
	}

	var setParams GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"goal-thread","origin":"user","objective":"ship it"}`), &setParams); err != nil {
		t.Fatalf("decode GoalSetParams: %v", err)
	}
	if setParams.Origin == nil || *setParams.Origin != ThreadGoalMutationOriginUser {
		t.Fatalf("GoalSetParams.origin = %#v, want user", setParams.Origin)
	}

	var clearParams GoalClearParams
	if err := json.Unmarshal([]byte(`{"threadId":"thr_123","origin":"automatic"}`), &clearParams); err != nil {
		t.Fatalf("decode GoalClearParams: %v", err)
	}
	if clearParams.Origin == nil || *clearParams.Origin != ThreadGoalMutationOriginAutomatic {
		t.Fatalf("GoalClearParams.origin = %#v, want automatic", clearParams.Origin)
	}

	var errNotif ErrorNotification
	if err := json.Unmarshal([]byte(`{"error":{"message":"blocked","codexErrorInfo":"misalignmentPolicyViolation","misalignment":{"errorType":"unauthorized_data_transfer","reviewTarget":"blk_1"}}}`), &errNotif); err != nil {
		t.Fatalf("decode ErrorNotification: %v", err)
	}
	if errNotif.Error.Misalignment == nil || errNotif.Error.Misalignment.ReviewTarget == nil || *errNotif.Error.Misalignment.ReviewTarget != "blk_1" {
		t.Fatalf("TurnError.misalignment.reviewTarget = %#v, want blk_1", errNotif.Error.Misalignment)
	}
}

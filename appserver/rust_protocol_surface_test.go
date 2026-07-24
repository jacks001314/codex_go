package appserver

import (
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
		string(MethodMemoryReset):                            true,
		string(MethodThreadCompactStart):                     true,
		string(MethodThreadApproveGuardianDeniedAction):      true,
		string(MethodThreadMetadataUpdate):                   true,
		string(MethodThreadSettingsUpdate):                   true,
		string(MethodThreadShellCommand):                     true,
		string(MethodThreadBackgroundTerminalsClean):         true,
		string(MethodThreadBackgroundTerminalsList):          true,
		string(MethodThreadBackgroundTerminalsTerminate):     true,
		string(MethodThreadRollback):                         true,
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
		string(MethodConfigRead):                             true,
		string(MethodConfigValueWrite):                       true,
		string(MethodConfigBatchWrite):                       true,
		string(MethodConfigRequirementsRead):                 true,
		string(MethodExternalAgentConfigDetect):              true,
		string(MethodExternalAgentConfigImport):              true,
		string(MethodExternalAgentConfigImportHistoryRecord): true,
		string(MethodExternalAgentConfigImportHistoriesRead): true,
		string(MethodLoginAccount):                           true,
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
	}
	knownMissing := map[string]bool{}

	for _, method := range methods {
		if !goMethods[method] && !knownMissing[method] {
			t.Logf("Go appserver protocol gap: missing Rust method %q", method)
		}
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
	goNotifications := map[string]bool{
		string(NotificationThreadStarted):                   true,
		string(NotificationThreadStatusChanged):             true,
		string(NotificationThreadArchived):                  true,
		string(NotificationThreadDeleted):                   true,
		string(NotificationThreadUnarchived):                true,
		string(NotificationThreadClosed):                    true,
		string(NotificationSkillsChanged):                   true,
		string(NotificationThreadNameUpdated):               true,
		string(NotificationThreadGoalUpdated):               true,
		string(NotificationThreadGoalCleared):               true,
		string(NotificationThreadSettingsUpdated):           true,
		string(NotificationThreadTokenUsageUpdated):         true,
		string(NotificationRawResponseCompleted):            true,
		string(NotificationTurnStarted):                     true,
		string(NotificationTurnCompleted):                   true,
		string(NotificationThreadRealtimeStarted):           true,
		string(NotificationThreadRealtimeItemAdded):         true,
		string(NotificationThreadRealtimeTranscriptDelta):   true,
		string(NotificationThreadRealtimeTranscriptDone):    true,
		string(NotificationThreadRealtimeOutputAudioDelta):  true,
		string(NotificationThreadRealtimeSDP):               true,
		string(NotificationThreadRealtimeError):             true,
		string(NotificationThreadRealtimeClosed):            true,
		string(NotificationError):                           true,
		string(NotificationAccountLoginCompleted):           true,
		string(NotificationAccountUpdated):                  true,
		string(NotificationAccountRateLimitsUpdated):        true,
		string(NotificationAppListUpdated):                  true,
		string(NotificationModelRerouted):                   true,
		string(NotificationModelVerification):               true,
		string(NotificationWarning):                         true,
		string(NotificationDeprecationNotice):               true,
		string(NotificationConfigWarning):                   true,
		string(NotificationFuzzyFileSearchSessionUpdated):   true,
		string(NotificationFuzzyFileSearchSessionCompleted): true,
	}
	for _, notification := range notifications {
		if !goNotifications[notification] {
			t.Logf("Go appserver protocol gap: missing Rust notification %q", notification)
		}
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
	return dedupeSortedStrings(extractMatches(data, re))
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
	re := regexp.MustCompile(`Notification[A-Za-z0-9_]+\s+NotificationMethod\s+=\s+"([^"]+)"`)
	return dedupeSortedStrings(extractMatches(data, re))
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

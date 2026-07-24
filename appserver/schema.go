package appserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	ControlDirName        = "app-server-control"
	ControlSocketFileName = "app-server-control.sock"
)

type SchemaGenerateOptions struct {
	OutDir       string
	Prettier     string
	Experimental bool
	Internal     bool
}

type ProtocolSchema struct {
	Schema         string              `json:"$schema"`
	Title          string              `json:"title"`
	Version        string              `json:"version"`
	Experimental   bool                `json:"experimental"`
	Methods        []ProtocolMethod    `json:"methods"`
	ClientRequests []ProtocolMethod    `json:"clientRequests"`
	ServerRequests []ProtocolMethod    `json:"serverRequests"`
	Notifications  []ProtocolMethod    `json:"notifications"`
	Types          []ProtocolTypeEntry `json:"types"`
}

type ProtocolMethod struct {
	Method       string `json:"method"`
	Params       string `json:"params,omitempty"`
	Result       string `json:"result,omitempty"`
	Experimental bool   `json:"experimental,omitempty"`
}

type ProtocolTypeEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func AppServerControlSocketPath(codexHome string) string {
	if codexHome == "" {
		codexHome = ".codex"
	}
	return filepath.Join(codexHome, ControlDirName, ControlSocketFileName)
}

func GenerateTypeScript(options *SchemaGenerateOptions) error {
	if options == nil {
		return fmt.Errorf("schema generation options are required")
	}
	if options.OutDir == "" {
		return fmt.Errorf("output directory is required")
	}
	schema := BuildTypeScriptProtocolSchema(options.Experimental, options.Internal)
	body := renderTypeScriptSchema(schema)
	return writeGeneratedFile(filepath.Join(options.OutDir, "protocol.ts"), []byte(body))
}

func GenerateJSONSchema(options *SchemaGenerateOptions) error {
	if options == nil {
		return fmt.Errorf("schema generation options are required")
	}
	if options.OutDir == "" {
		return fmt.Errorf("output directory is required")
	}
	schema := BuildProtocolSchema(options.Experimental, options.Internal)
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return err
	}
	name := "protocol.schema.json"
	if options.Internal {
		name = "internal.schema.json"
	}
	return writeGeneratedFile(filepath.Join(options.OutDir, name), append(data, '\n'))
}

func BuildProtocolSchema(experimental bool, internal bool) *ProtocolSchema {
	clientRequests := baseClientRequestMethods()
	if experimental || internal {
		clientRequests = append(clientRequests, experimentalClientRequestMethods()...)
	}
	if internal {
		clientRequests = append(clientRequests, internalClientRequestMethods()...)
	}
	clientRequests = normalizeProtocolMethods(clientRequests)
	serverRequests := serverRequestMethods(experimental || internal)
	notifications := notificationMethods(experimental || internal)
	types := []ProtocolTypeEntry{
		{Name: "Request", Kind: "object"},
		{Name: "Response", Kind: "object"},
		{Name: "InitializeParams", Kind: "object"},
		{Name: "InitializeResponse", Kind: "object"},
		{Name: "Thread", Kind: "object"},
		{Name: "Turn", Kind: "object"},
		{Name: "Item", Kind: "object"},
	}
	if internal {
		types = append(types, ProtocolTypeEntry{Name: "InternalDiagnostics", Kind: "object"})
	}
	return &ProtocolSchema{
		Schema:         "https://json-schema.org/draft/2020-12/schema",
		Title:          "Codex app-server protocol",
		Version:        "go-port",
		Experimental:   experimental || internal,
		Methods:        append([]ProtocolMethod(nil), clientRequests...),
		ClientRequests: append([]ProtocolMethod(nil), clientRequests...),
		ServerRequests: serverRequests,
		Notifications:  notifications,
		Types:          types,
	}
}

func BuildTypeScriptProtocolSchema(experimental bool, internal bool) *ProtocolSchema {
	schema := BuildProtocolSchema(experimental, internal)
	schema.ClientRequests = normalizeProtocolMethods(append(schema.ClientRequests, typeScriptLegacyClientRequestMethods()...))
	schema.Notifications = normalizeProtocolMethods(append(schema.Notifications, typeScriptOnlyNotificationMethods()...))
	schema.Methods = append([]ProtocolMethod(nil), schema.ClientRequests...)
	return schema
}

func typeScriptLegacyClientRequestMethods() []ProtocolMethod {
	return []ProtocolMethod{
		{Method: string(MethodGetAuthStatus)},
		{Method: string(MethodGetConversationSummary)},
		{Method: string(MethodGitDiffToRemote)},
	}
}

func typeScriptOnlyNotificationMethods() []ProtocolMethod {
	return []ProtocolMethod{
		{Method: string(NotificationRawResponseItemCompleted)},
		{Method: string(NotificationRawResponseCompleted)},
	}
}

func baseClientRequestMethods() []ProtocolMethod {
	return []ProtocolMethod{
		{Method: string(MethodAppList)},
		{Method: string(MethodAppRead)},
		{Method: string(MethodAppInstalled)},
		{Method: string(MethodCancelLoginAccount)},
		{Method: string(MethodCommandExec)},
		{Method: string(MethodCommandExecResize)},
		{Method: string(MethodCommandExecTerminate)},
		{Method: string(MethodCommandExecWrite)},
		{Method: string(MethodConfigBatchWrite)},
		{Method: string(MethodConfigMCPServerReload)},
		{Method: string(MethodConfigRead)},
		{Method: string(MethodConfigRequirementsRead)},
		{Method: string(MethodConfigValueWrite)},
		{Method: string(MethodConsumeAccountRateLimitResetCredit)},
		{Method: string(MethodExternalAgentConfigDetect)},
		{Method: string(MethodExternalAgentConfigImport)},
		{Method: string(MethodExternalAgentConfigImportHistoryRecord)},
		{Method: string(MethodExternalAgentConfigImportHistoriesRead)},
		{Method: string(MethodFeedbackUpload)},
		{Method: string(MethodExperimentalFeatureList)},
		{Method: string(MethodExperimentalFeatureSet)},
		{Method: string(MethodFSCopy)},
		{Method: string(MethodFSCreateDirectory)},
		{Method: string(MethodFSGetMetadata)},
		{Method: string(MethodFSReadDirectory)},
		{Method: string(MethodFSReadFile)},
		{Method: string(MethodFSRemove)},
		{Method: string(MethodFSUnwatch)},
		{Method: string(MethodFSWatch)},
		{Method: string(MethodFSWriteFile)},
		{Method: string(MethodFuzzyFileSearch)},
		{Method: string(MethodGetAccount)},
		{Method: string(MethodGetAccountRateLimits)},
		{Method: string(MethodGetAccountTokenUsage)},
		{Method: string(MethodGetWorkspaceMessages)},
		{Method: string(MethodHooksList)},
		{Method: string(MethodInitialize)},
		{Method: string(MethodLoginAccount)},
		{Method: string(MethodLogoutAccount)},
		{Method: string(MethodMarketplaceAdd)},
		{Method: string(MethodMarketplaceRemove)},
		{Method: string(MethodMarketplaceUpgrade)},
		{Method: string(MethodMCPServerOauthLogin)},
		{Method: string(MethodMCPServerResourceRead)},
		{Method: string(MethodMCPServerStatusList)},
		{Method: string(MethodMCPServerToolCall)},
		{Method: string(MethodModelList)},
		{Method: string(MethodModelProviderCapabilitiesRead)},
		{Method: string(MethodPermissionProfileList)},
		{Method: string(MethodPluginInstall)},
		{Method: string(MethodPluginInstalled)},
		{Method: string(MethodPluginList)},
		{Method: string(MethodPluginRead)},
		{Method: string(MethodPluginShareCheckout)},
		{Method: string(MethodPluginShareDelete)},
		{Method: string(MethodPluginShareList)},
		{Method: string(MethodPluginShareSave)},
		{Method: string(MethodPluginShareUpdateTargets)},
		{Method: string(MethodPluginSkillRead)},
		{Method: string(MethodPluginUninstall)},
		{Method: string(MethodReviewStart)},
		{Method: string(MethodSendAddCreditsNudgeEmail)},
		{Method: string(MethodSkillsConfigWrite)},
		{Method: string(MethodSkillsExtraRootsSet)},
		{Method: string(MethodSkillsList)},
		{Method: string(MethodThreadApproveGuardianDeniedAction)},
		{Method: string(MethodThreadArchive)},
		{Method: string(MethodThreadCompactStart)},
		{Method: string(MethodThreadDelete)},
		{Method: string(MethodThreadFork)},
		{Method: string(MethodThreadGoalClear)},
		{Method: string(MethodThreadGoalGet)},
		{Method: string(MethodThreadGoalSet)},
		{Method: string(MethodThreadInjectItems)},
		{Method: string(MethodThreadList)},
		{Method: string(MethodThreadLoadedList)},
		{Method: string(MethodThreadMetadataUpdate)},
		{Method: string(MethodThreadNameSet)},
		{Method: string(MethodThreadRead)},
		{Method: string(MethodThreadResume)},
		{Method: string(MethodThreadRollback)},
		{Method: string(MethodThreadShellCommand)},
		{Method: string(MethodThreadStart)},
		{Method: string(MethodThreadUnarchive)},
		{Method: string(MethodThreadUnsubscribe)},
		{Method: string(MethodTurnInterrupt)},
		{Method: string(MethodTurnStart)},
		{Method: string(MethodTurnSteer)},
		{Method: string(MethodWindowsSandboxReadiness)},
		{Method: string(MethodWindowsSandboxSetupStart)},
	}
}

func experimentalClientRequestMethods() []ProtocolMethod {
	return []ProtocolMethod{
		{Method: string(MethodCollaborationModeList), Experimental: true},
		{Method: string(MethodEnvironmentAdd), Experimental: true},
		{Method: string(MethodEnvironmentInfo), Experimental: true},
		{Method: string(MethodEnvironmentStatus), Experimental: true},
		{Method: string(MethodFuzzyFileSearchStart), Experimental: true},
		{Method: string(MethodFuzzyFileSearchStop), Experimental: true},
		{Method: string(MethodFuzzyFileSearchUpdate), Experimental: true},
		{Method: string(MethodMemoryReset), Experimental: true},
		{Method: string(MethodMockExperimentalMethod), Experimental: true},
		{Method: string(MethodProcessKill), Experimental: true},
		{Method: string(MethodProcessResizePty), Experimental: true},
		{Method: string(MethodProcessSpawn), Experimental: true},
		{Method: string(MethodProcessWriteStdin), Experimental: true},
		{Method: string(MethodRemoteControlClientsList), Experimental: true},
		{Method: string(MethodRemoteControlClientsRevoke), Experimental: true},
		{Method: string(MethodRemoteControlDisable), Experimental: true},
		{Method: string(MethodRemoteControlEnable), Experimental: true},
		{Method: string(MethodRemoteControlPairingStart), Experimental: true},
		{Method: string(MethodRemoteControlPairingStatus), Experimental: true},
		{Method: string(MethodRemoteControlStatusRead), Experimental: true},
		{Method: string(MethodThreadBackgroundTerminalsClean), Experimental: true},
		{Method: string(MethodThreadBackgroundTerminalsList), Experimental: true},
		{Method: string(MethodThreadBackgroundTerminalsTerminate), Experimental: true},
		{Method: string(MethodThreadDecrementElicitation), Experimental: true},
		{Method: string(MethodThreadIncrementElicitation), Experimental: true},
		{Method: string(MethodThreadItemsList), Experimental: true},
		{Method: string(MethodThreadMemoryModeSet), Experimental: true},
		{Method: string(MethodThreadRealtimeAppendAudio), Experimental: true},
		{Method: string(MethodThreadRealtimeAppendSpeech), Experimental: true},
		{Method: string(MethodThreadRealtimeAppendText), Experimental: true},
		{Method: string(MethodThreadRealtimeListVoices), Experimental: true},
		{Method: string(MethodThreadRealtimeStart), Experimental: true},
		{Method: string(MethodThreadRealtimeStop), Experimental: true},
		{Method: string(MethodThreadSearch), Experimental: true},
		{Method: string(MethodThreadSearchOccurrences), Experimental: true},
		{Method: string(MethodThreadSettingsUpdate), Experimental: true},
		{Method: string(MethodThreadTurnsList), Experimental: true},
	}
}

func internalClientRequestMethods() []ProtocolMethod {
	return []ProtocolMethod{
		{Method: string(MethodAccountSessionsAdd)},
		{Method: string(MethodAccountSessionsList)},
		{Method: string(MethodAccountSessionsLogout)},
		{Method: string(MethodAccountSessionsSwitch)},
		{Method: string(MethodThreadDecrementElicitationLegacy), Experimental: true},
		{Method: string(MethodThreadIncrementElicitationLegacy), Experimental: true},
		{Method: string(MethodThreadSetName)},
	}
}

func serverRequestMethods(includeExperimental bool) []ProtocolMethod {
	methods := []ProtocolMethod{
		{Method: string(ServerRequestApplyPatchApproval)},
		{Method: string(ServerRequestAttestationGenerate)},
		{Method: string(ServerRequestChatGPTAuthTokensRefresh)},
		{Method: string(ServerRequestCommandExecutionApproval)},
		{Method: string(ServerRequestDynamicToolCall)},
		{Method: string(ServerRequestExecCommandApproval)},
		{Method: string(ServerRequestFileChangeApproval)},
		{Method: string(ServerRequestMCPElicitation)},
		{Method: string(ServerRequestPermissionsApproval)},
		{Method: string(ServerRequestToolUserInput)},
	}
	if includeExperimental {
		methods = append(methods, ProtocolMethod{Method: string(ServerRequestCurrentTimeRead), Experimental: true})
	}
	return normalizeProtocolMethods(methods)
}

func notificationMethods(includeExperimental bool) []ProtocolMethod {
	methods := []ProtocolMethod{
		{Method: string(NotificationAccountLoginCompleted)},
		{Method: string(NotificationAccountRateLimitsUpdated)},
		{Method: string(NotificationAccountUpdated)},
		{Method: string(NotificationAgentMessageDelta)},
		{Method: string(NotificationAppListUpdated)},
		{Method: string(NotificationCommandExecutionOutputDelta)},
		{Method: string(NotificationCommandExecOutputDelta)},
		{Method: string(NotificationConfigWarning)},
		{Method: string(NotificationContextCompacted)},
		{Method: string(NotificationDeprecationNotice)},
		{Method: string(NotificationError)},
		{Method: "externalAgentConfig/import/completed"},
		{Method: "externalAgentConfig/import/progress"},
		{Method: string(NotificationFileChangeOutputDelta)},
		{Method: string(NotificationFileChangePatchUpdated)},
		{Method: string(NotificationFSChanged)},
		{Method: string(NotificationFuzzyFileSearchSessionCompleted)},
		{Method: string(NotificationFuzzyFileSearchSessionUpdated)},
		{Method: string(NotificationHookCompleted)},
		{Method: string(NotificationHookStarted)},
		{Method: string(NotificationGuardianWarning)},
		{Method: string(NotificationItemGuardianApprovalReviewCompleted)},
		{Method: string(NotificationItemGuardianApprovalReviewStarted)},
		{Method: string(NotificationItemCompleted)},
		{Method: string(NotificationItemStarted)},
		{Method: string(NotificationMCPServerOauthLoginCompleted)},
		{Method: string(NotificationMCPServerStatusUpdated)},
		{Method: string(NotificationMCPToolCallProgress)},
		{Method: string(NotificationModelRerouted)},
		{Method: string(NotificationModelSafetyBufferingUpdated)},
		{Method: string(NotificationModelVerification)},
		{Method: string(NotificationPlanDelta)},
		{Method: string(NotificationProcessExited)},
		{Method: string(NotificationProcessOutputDelta)},
		{Method: string(NotificationReasoningSummaryPartAdded)},
		{Method: string(NotificationReasoningSummaryTextDelta)},
		{Method: string(NotificationReasoningTextDelta)},
		{Method: string(NotificationRemoteControlStatusChanged)},
		{Method: string(NotificationServerRequestResolved)},
		{Method: string(NotificationSkillsChanged)},
		{Method: string(NotificationTerminalInteraction)},
		{Method: string(NotificationThreadArchived)},
		{Method: string(NotificationThreadClosed)},
		{Method: string(NotificationThreadDeleted)},
		{Method: string(NotificationThreadGoalCleared)},
		{Method: string(NotificationThreadGoalUpdated)},
		{Method: string(NotificationThreadNameUpdated)},
		{Method: string(NotificationThreadRealtimeClosed)},
		{Method: string(NotificationThreadRealtimeError)},
		{Method: string(NotificationThreadRealtimeItemAdded)},
		{Method: string(NotificationThreadRealtimeOutputAudioDelta)},
		{Method: string(NotificationThreadRealtimeSDP)},
		{Method: string(NotificationThreadRealtimeStarted)},
		{Method: string(NotificationThreadRealtimeTranscriptDelta)},
		{Method: string(NotificationThreadRealtimeTranscriptDone)},
		{Method: string(NotificationThreadSettingsUpdated)},
		{Method: string(NotificationThreadStarted)},
		{Method: string(NotificationThreadStatusChanged)},
		{Method: string(NotificationThreadTokenUsageUpdated)},
		{Method: string(NotificationThreadUnarchived)},
		{Method: string(NotificationTurnCompleted)},
		{Method: string(NotificationTurnDiffUpdated)},
		{Method: string(NotificationTurnModerationMetadata)},
		{Method: string(NotificationTurnPlanUpdated)},
		{Method: string(NotificationTurnStarted)},
		{Method: string(NotificationWarning)},
		{Method: string(NotificationWindowsSandboxSetupCompleted)},
		{Method: string(NotificationWindowsWorldWritableWarning)},
	}
	return normalizeProtocolMethods(methods)
}

func normalizeProtocolMethods(methods []ProtocolMethod) []ProtocolMethod {
	byMethod := make(map[string]ProtocolMethod, len(methods))
	for _, method := range methods {
		if method.Method == "" {
			continue
		}
		method = enrichProtocolMethod(method)
		existing := byMethod[method.Method]
		method.Experimental = method.Experimental || existing.Experimental
		if method.Params == "" {
			method.Params = existing.Params
		}
		if method.Result == "" {
			method.Result = existing.Result
		}
		byMethod[method.Method] = method
	}
	normalized := make([]ProtocolMethod, 0, len(byMethod))
	for _, method := range byMethod {
		normalized = append(normalized, method)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].Method < normalized[j].Method
	})
	return normalized
}

func enrichProtocolMethod(method ProtocolMethod) ProtocolMethod {
	if method.Params != "" || method.Result != "" {
		return method
	}
	signature, ok := protocolMethodSignatures()[method.Method]
	if !ok {
		return method
	}
	method.Params = signature.Params
	method.Result = signature.Result
	return method
}

type protocolMethodSignature struct {
	Params string
	Result string
}

func protocolMethodSignatures() map[string]protocolMethodSignature {
	return map[string]protocolMethodSignature{
		string(MethodAccountSessionsAdd):                     {Params: "AccountSessionsAddParams", Result: "AccountSessionsResponse"},
		string(MethodAccountSessionsList):                    {Params: "AccountSessionsListParams", Result: "AccountSessionsResponse"},
		string(MethodAccountSessionsLogout):                  {Params: "AccountSessionsLogoutParams", Result: "AccountSessionsResponse"},
		string(MethodAccountSessionsSwitch):                  {Params: "AccountSessionsSwitchParams", Result: "AccountSessionsResponse"},
		string(MethodAppList):                                {Params: "AppsListParams", Result: "AppsListResponse"},
		string(MethodAppRead):                                {Params: "AppsReadParams", Result: "AppsReadResponse"},
		string(MethodAppInstalled):                           {Params: "AppsInstalledParams", Result: "AppsInstalledResponse"},
		string(MethodCancelLoginAccount):                     {Params: "CancelLoginAccountParams", Result: "CancelLoginAccountResponse"},
		string(MethodCollaborationModeList):                  {Params: "CollaborationModeListParams", Result: "CollaborationModeListResponse"},
		string(MethodCommandExec):                            {Params: "CommandExecParams", Result: "CommandExecResponse"},
		string(MethodCommandExecResize):                      {Params: "CommandExecResizeParams", Result: "CommandExecResizeResponse"},
		string(MethodCommandExecTerminate):                   {Params: "CommandExecTerminateParams", Result: "CommandExecTerminateResponse"},
		string(MethodCommandExecWrite):                       {Params: "CommandExecWriteParams", Result: "CommandExecWriteResponse"},
		string(MethodConfigBatchWrite):                       {Params: "ConfigBatchWriteParams", Result: "ConfigWriteResponse"},
		string(MethodConfigMCPServerReload):                  {Result: "McpServerRefreshResponse"},
		string(MethodConfigRead):                             {Params: "ConfigReadParams", Result: "ConfigReadResponse"},
		string(MethodConfigRequirementsRead):                 {Result: "ConfigRequirementsReadResponse"},
		string(MethodConfigValueWrite):                       {Params: "ConfigValueWriteParams", Result: "ConfigWriteResponse"},
		string(MethodConsumeAccountRateLimitResetCredit):     {Params: "ConsumeAccountRateLimitResetCreditParams", Result: "ConsumeAccountRateLimitResetCreditResponse"},
		string(MethodEnvironmentAdd):                         {Params: "EnvironmentAddParams", Result: "EnvironmentAddResponse"},
		string(MethodEnvironmentInfo):                        {Params: "EnvironmentInfoParams", Result: "EnvironmentInfoResponse"},
		string(MethodEnvironmentStatus):                      {Params: "EnvironmentStatusParams", Result: "EnvironmentStatusResponse"},
		string(MethodExperimentalFeatureList):                {Params: "ExperimentalFeatureListParams", Result: "ExperimentalFeatureListResponse"},
		string(MethodExperimentalFeatureSet):                 {Params: "ExperimentalFeatureEnablementSetParams", Result: "ExperimentalFeatureEnablementSetResponse"},
		string(MethodExternalAgentConfigDetect):              {Params: "ExternalAgentConfigDetectParams", Result: "ExternalAgentConfigDetectResponse"},
		string(MethodExternalAgentConfigImport):              {Params: "ExternalAgentConfigImportParams", Result: "ExternalAgentConfigImportResponse"},
		string(MethodExternalAgentConfigImportHistoryRecord): {Params: "ExternalAgentConfigImportHistoryRecordParams", Result: "ExternalAgentConfigImportHistoryRecordResponse"},
		string(MethodExternalAgentConfigImportHistoriesRead): {Result: "ExternalAgentConfigImportHistoriesReadResponse"},
		string(MethodFeedbackUpload):                         {Params: "FeedbackUploadParams", Result: "FeedbackUploadResponse"},
		string(MethodFSCopy):                                 {Params: "FsCopyParams", Result: "FsCopyResponse"},
		string(MethodFSCreateDirectory):                      {Params: "FsCreateDirectoryParams", Result: "FsCreateDirectoryResponse"},
		string(MethodFSGetMetadata):                          {Params: "FsGetMetadataParams", Result: "FsGetMetadataResponse"},
		string(MethodFSReadDirectory):                        {Params: "FsReadDirectoryParams", Result: "FsReadDirectoryResponse"},
		string(MethodFSReadFile):                             {Params: "FsReadFileParams", Result: "FsReadFileResponse"},
		string(MethodFSRemove):                               {Params: "FsRemoveParams", Result: "FsRemoveResponse"},
		string(MethodFSUnwatch):                              {Params: "FsUnwatchParams", Result: "FsUnwatchResponse"},
		string(MethodFSWatch):                                {Params: "FsWatchParams", Result: "FsWatchResponse"},
		string(MethodFSWriteFile):                            {Params: "FsWriteFileParams", Result: "FsWriteFileResponse"},
		string(MethodFuzzyFileSearch):                        {Params: "FuzzyFileSearchParams", Result: "FuzzyFileSearchResponse"},
		string(MethodFuzzyFileSearchStart):                   {Params: "FuzzyFileSearchSessionStartParams", Result: "FuzzyFileSearchSessionStartResponse"},
		string(MethodFuzzyFileSearchStop):                    {Params: "FuzzyFileSearchSessionStopParams", Result: "FuzzyFileSearchSessionStopResponse"},
		string(MethodFuzzyFileSearchUpdate):                  {Params: "FuzzyFileSearchSessionUpdateParams", Result: "FuzzyFileSearchSessionUpdateResponse"},
		string(MethodGetAccount):                             {Params: "GetAccountParams", Result: "GetAccountResponse"},
		string(MethodGetAccountRateLimits):                   {Result: "GetAccountRateLimitsResponse"},
		string(MethodGetAccountTokenUsage):                   {Result: "GetAccountTokenUsageResponse"},
		string(MethodGetWorkspaceMessages):                   {Result: "GetWorkspaceMessagesResponse"},
		string(MethodGetAuthStatus):                          {Params: "GetAuthStatusParams", Result: "GetAuthStatusResponse"},
		string(MethodGetConversationSummary):                 {Params: "GetConversationSummaryParams", Result: "GetConversationSummaryResponse"},
		string(MethodGitDiffToRemote):                        {Params: "GitDiffToRemoteParams", Result: "GitDiffToRemoteResponse"},
		string(MethodHooksList):                              {Params: "HooksListParams", Result: "HooksListResponse"},
		string(MethodInitialize):                             {Params: "InitializeParams", Result: "InitializeResponse"},
		string(MethodLoginAccount):                           {Params: "LoginAccountParams", Result: "LoginAccountResponse"},
		string(MethodLogoutAccount):                          {Result: "LogoutAccountResponse"},
		string(MethodMarketplaceAdd):                         {Params: "MarketplaceAddParams", Result: "MarketplaceAddResponse"},
		string(MethodMarketplaceRemove):                      {Params: "MarketplaceRemoveParams", Result: "MarketplaceRemoveResponse"},
		string(MethodMarketplaceUpgrade):                     {Params: "MarketplaceUpgradeParams", Result: "MarketplaceUpgradeResponse"},
		string(MethodMCPServerOauthCancel):                   {Params: "McpServerOauthCancelParams", Result: "McpServerOauthCancelResponse"},
		string(MethodMCPServerOauthLogin):                    {Params: "McpServerOauthLoginParams", Result: "McpServerOauthLoginResponse"},
		string(MethodMCPServerRefresh):                       {Result: "McpServerRefreshResponse"},
		string(MethodMCPServerResourceRead):                  {Params: "McpResourceReadParams", Result: "McpResourceReadResponse"},
		string(MethodMCPServerStatusList):                    {Params: "ListMcpServerStatusParams", Result: "ListMcpServerStatusResponse"},
		string(MethodMCPServerToolCall):                      {Params: "McpServerToolCallParams", Result: "McpServerToolCallResponse"},
		string(MethodMemoryReset):                            {Result: "MemoryResetResponse"},
		string(MethodMockExperimentalMethod):                 {Params: "MockExperimentalMethodParams", Result: "MockExperimentalMethodResponse"},
		string(MethodModelList):                              {Params: "ModelListParams", Result: "ModelListResponse"},
		string(MethodModelProviderCapabilitiesRead):          {Params: "ModelProviderCapabilitiesReadParams", Result: "ModelProviderCapabilitiesReadResponse"},
		string(MethodPermissionProfileList):                  {Params: "PermissionProfileListParams", Result: "PermissionProfileListResponse"},
		string(MethodPluginInstall):                          {Params: "PluginInstallParams", Result: "PluginInstallResponse"},
		string(MethodPluginInstalled):                        {Params: "PluginInstalledParams", Result: "PluginInstalledResponse"},
		string(MethodPluginList):                             {Params: "PluginListParams", Result: "PluginListResponse"},
		string(MethodPluginRead):                             {Params: "PluginReadParams", Result: "PluginReadResponse"},
		string(MethodPluginShareCheckout):                    {Params: "PluginShareCheckoutParams", Result: "PluginShareCheckoutResponse"},
		string(MethodPluginShareDelete):                      {Params: "PluginShareDeleteParams", Result: "PluginShareDeleteResponse"},
		string(MethodPluginShareList):                        {Params: "PluginShareListParams", Result: "PluginShareListResponse"},
		string(MethodPluginShareSave):                        {Params: "PluginShareSaveParams", Result: "PluginShareSaveResponse"},
		string(MethodPluginShareUpdateTargets):               {Params: "PluginShareUpdateTargetsParams", Result: "PluginShareUpdateTargetsResponse"},
		string(MethodPluginSkillRead):                        {Params: "PluginSkillReadParams", Result: "PluginSkillReadResponse"},
		string(MethodPluginUninstall):                        {Params: "PluginUninstallParams", Result: "PluginUninstallResponse"},
		string(MethodProcessKill):                            {Params: "ProcessKillParams", Result: "ProcessKillResponse"},
		string(MethodProcessResizePty):                       {Params: "ProcessResizePtyParams", Result: "ProcessResizePtyResponse"},
		string(MethodProcessSpawn):                           {Params: "ProcessSpawnParams", Result: "ProcessSpawnResponse"},
		string(MethodProcessWriteStdin):                      {Params: "ProcessWriteStdinParams", Result: "ProcessWriteStdinResponse"},
		string(MethodRemoteControlClientsList):               {Params: "RemoteControlClientsListParams", Result: "RemoteControlClientsListResponse"},
		string(MethodRemoteControlClientsRevoke):             {Params: "RemoteControlClientsRevokeParams", Result: "RemoteControlClientsRevokeResponse"},
		string(MethodRemoteControlDisable):                   {Params: "RemoteControlDisableParams", Result: "RemoteControlDisableResponse"},
		string(MethodRemoteControlEnable):                    {Params: "RemoteControlEnableParams", Result: "RemoteControlEnableResponse"},
		string(MethodRemoteControlPairingStart):              {Params: "RemoteControlPairingStartParams", Result: "RemoteControlPairingStartResponse"},
		string(MethodRemoteControlPairingStatus):             {Params: "RemoteControlPairingStatusParams", Result: "RemoteControlPairingStatusResponse"},
		string(MethodRemoteControlStatusRead):                {Result: "RemoteControlStatusReadResponse"},
		string(MethodReviewStart):                            {Params: "ReviewStartParams", Result: "ReviewStartResponse"},
		string(MethodSendAddCreditsNudgeEmail):               {Params: "SendAddCreditsNudgeEmailParams", Result: "SendAddCreditsNudgeEmailResponse"},
		string(MethodSkillsConfigWrite):                      {Params: "SkillsConfigWriteParams", Result: "SkillsConfigWriteResponse"},
		string(MethodSkillsExtraRootsSet):                    {Params: "SkillsExtraRootsSetParams", Result: "SkillsExtraRootsSetResponse"},
		string(MethodSkillsList):                             {Params: "SkillsListParams", Result: "SkillsListResponse"},
		string(MethodThreadApproveGuardianDeniedAction):      {Params: "ThreadApproveGuardianDeniedActionParams", Result: "ThreadApproveGuardianDeniedActionResponse"},
		string(MethodThreadArchive):                          {Params: "ThreadArchiveParams", Result: "ThreadArchiveResponse"},
		string(MethodThreadBackgroundTerminalsClean):         {Params: "ThreadBackgroundTerminalsCleanParams", Result: "ThreadBackgroundTerminalsCleanResponse"},
		string(MethodThreadBackgroundTerminalsList):          {Params: "ThreadBackgroundTerminalsListParams", Result: "ThreadBackgroundTerminalsListResponse"},
		string(MethodThreadBackgroundTerminalsTerminate):     {Params: "ThreadBackgroundTerminalsTerminateParams", Result: "ThreadBackgroundTerminalsTerminateResponse"},
		string(MethodThreadCompactStart):                     {Params: "ThreadCompactStartParams", Result: "ThreadCompactStartResponse"},
		string(MethodThreadDecrementElicitation):             {Params: "ThreadDecrementElicitationParams", Result: "ThreadDecrementElicitationResponse"},
		string(MethodThreadDecrementElicitationLegacy):       {Params: "ThreadDecrementElicitationParams", Result: "ThreadDecrementElicitationResponse"},
		string(MethodThreadDelete):                           {Params: "ThreadDeleteParams", Result: "ThreadDeleteResponse"},
		string(MethodThreadFork):                             {Params: "ThreadForkParams", Result: "ThreadForkResponse"},
		string(MethodThreadGoalClear):                        {Params: "ThreadGoalClearParams", Result: "ThreadGoalClearResponse"},
		string(MethodThreadGoalGet):                          {Params: "ThreadGoalGetParams", Result: "ThreadGoalGetResponse"},
		string(MethodThreadGoalSet):                          {Params: "ThreadGoalSetParams", Result: "ThreadGoalSetResponse"},
		string(MethodThreadIncrementElicitation):             {Params: "ThreadIncrementElicitationParams", Result: "ThreadIncrementElicitationResponse"},
		string(MethodThreadIncrementElicitationLegacy):       {Params: "ThreadIncrementElicitationParams", Result: "ThreadIncrementElicitationResponse"},
		string(MethodThreadInjectItems):                      {Params: "ThreadInjectItemsParams", Result: "ThreadInjectItemsResponse"},
		string(MethodThreadItemsList):                        {Params: "ThreadItemsListParams", Result: "ThreadItemsListResponse"},
		string(MethodThreadList):                             {Params: "ThreadListParams", Result: "ThreadListResponse"},
		string(MethodThreadLoadedList):                       {Params: "ThreadLoadedListParams", Result: "ThreadLoadedListResponse"},
		string(MethodThreadMemoryModeSet):                    {Params: "ThreadMemoryModeSetParams", Result: "ThreadMemoryModeSetResponse"},
		string(MethodThreadMetadataUpdate):                   {Params: "ThreadMetadataUpdateParams", Result: "ThreadMetadataUpdateResponse"},
		string(MethodThreadNameSet):                          {Params: "ThreadSetNameParams", Result: "ThreadSetNameResponse"},
		string(MethodThreadRead):                             {Params: "ThreadReadParams", Result: "ThreadReadResponse"},
		string(MethodThreadRealtimeAppendAudio):              {Params: "ThreadRealtimeAppendAudioParams", Result: "ThreadRealtimeAppendAudioResponse"},
		string(MethodThreadRealtimeAppendSpeech):             {Params: "ThreadRealtimeAppendSpeechParams", Result: "ThreadRealtimeAppendSpeechResponse"},
		string(MethodThreadRealtimeAppendText):               {Params: "ThreadRealtimeAppendTextParams", Result: "ThreadRealtimeAppendTextResponse"},
		string(MethodThreadRealtimeListVoices):               {Params: "ThreadRealtimeListVoicesParams", Result: "ThreadRealtimeListVoicesResponse"},
		string(MethodThreadRealtimeStart):                    {Params: "ThreadRealtimeStartParams", Result: "ThreadRealtimeStartResponse"},
		string(MethodThreadRealtimeStop):                     {Params: "ThreadRealtimeStopParams", Result: "ThreadRealtimeStopResponse"},
		string(MethodThreadResume):                           {Params: "ThreadResumeParams", Result: "ThreadResumeResponse"},
		string(MethodThreadRollback):                         {Params: "ThreadRollbackParams", Result: "ThreadRollbackResponse"},
		string(MethodThreadSearch):                           {Params: "ThreadSearchParams", Result: "ThreadSearchResponse"},
		string(MethodThreadSearchOccurrences):                {Params: "ThreadSearchOccurrencesParams", Result: "ThreadSearchOccurrencesResponse"},
		string(MethodThreadSetName):                          {Params: "ThreadSetNameParams", Result: "ThreadSetNameResponse"},
		string(MethodThreadSettingsUpdate):                   {Params: "ThreadSettingsUpdateParams", Result: "ThreadSettingsUpdateResponse"},
		string(MethodThreadShellCommand):                     {Params: "ThreadShellCommandParams", Result: "ThreadShellCommandResponse"},
		string(MethodThreadStart):                            {Params: "ThreadStartParams", Result: "ThreadStartResponse"},
		string(MethodThreadTurnsList):                        {Params: "ThreadTurnsListParams", Result: "ThreadTurnsListResponse"},
		string(MethodThreadUnarchive):                        {Params: "ThreadUnarchiveParams", Result: "ThreadUnarchiveResponse"},
		string(MethodThreadUnsubscribe):                      {Params: "ThreadUnsubscribeParams", Result: "ThreadUnsubscribeResponse"},
		string(MethodTurnInterrupt):                          {Params: "TurnInterruptParams", Result: "TurnInterruptResponse"},
		string(MethodTurnStart):                              {Params: "TurnStartParams", Result: "TurnStartResponse"},
		string(MethodTurnSteer):                              {Params: "TurnSteerParams", Result: "TurnSteerResponse"},
		string(MethodWindowsSandboxReadiness):                {Result: "WindowsSandboxReadinessResponse"},
		string(MethodWindowsSandboxSetupStart):               {Params: "WindowsSandboxSetupStartParams", Result: "WindowsSandboxSetupStartResponse"},

		string(ServerRequestApplyPatchApproval):       {Params: "ApplyPatchApprovalParams", Result: "ApplyPatchApprovalResponse"},
		string(ServerRequestAttestationGenerate):      {Params: "AttestationGenerateParams", Result: "AttestationGenerateResponse"},
		string(ServerRequestChatGPTAuthTokensRefresh): {Params: "ChatgptAuthTokensRefreshParams", Result: "ChatgptAuthTokensRefreshResponse"},
		string(ServerRequestCommandExecutionApproval): {Params: "CommandExecutionRequestApprovalParams", Result: "CommandExecutionRequestApprovalResponse"},
		string(ServerRequestCurrentTimeRead):          {Params: "CurrentTimeReadParams", Result: "CurrentTimeReadResponse"},
		string(ServerRequestDynamicToolCall):          {Params: "DynamicToolCallParams", Result: "DynamicToolCallResponse"},
		string(ServerRequestExecCommandApproval):      {Params: "ExecCommandApprovalParams", Result: "ExecCommandApprovalResponse"},
		string(ServerRequestFileChangeApproval):       {Params: "FileChangeRequestApprovalParams", Result: "FileChangeRequestApprovalResponse"},
		string(ServerRequestMCPElicitation):           {Params: "McpServerElicitationRequestParams", Result: "McpServerElicitationRequestResponse"},
		string(ServerRequestPermissionsApproval):      {Params: "PermissionsRequestApprovalParams", Result: "PermissionsRequestApprovalResponse"},
		string(ServerRequestToolUserInput):            {Params: "ToolRequestUserInputParams", Result: "ToolRequestUserInputResponse"},

		string(NotificationAccountLoginCompleted):               {Params: "AccountLoginCompletedNotification"},
		string(NotificationAccountRateLimitsUpdated):            {Params: "AccountRateLimitsUpdatedNotification"},
		string(NotificationAccountUpdated):                      {Params: "AccountUpdatedNotification"},
		string(NotificationAgentMessageDelta):                   {Params: "AgentMessageDeltaNotification"},
		string(NotificationAppListUpdated):                      {Params: "AppListUpdatedNotification"},
		string(NotificationCommandExecOutputDelta):              {Params: "CommandExecOutputDeltaNotification"},
		string(NotificationCommandExecutionOutputDelta):         {Params: "CommandExecutionOutputDeltaNotification"},
		string(NotificationConfigWarning):                       {Params: "ConfigWarningNotification"},
		string(NotificationContextCompacted):                    {Params: "ContextCompactedNotification"},
		string(NotificationDeprecationNotice):                   {Params: "DeprecationNoticeNotification"},
		string(NotificationError):                               {Params: "ErrorNotification"},
		string(NotificationFileChangeOutputDelta):               {Params: "FileChangeOutputDeltaNotification"},
		string(NotificationFileChangePatchUpdated):              {Params: "FileChangePatchUpdatedNotification"},
		string(NotificationFSChanged):                           {Params: "FsChangedNotification"},
		string(NotificationFuzzyFileSearchSessionCompleted):     {Params: "FuzzyFileSearchSessionCompletedNotification"},
		string(NotificationFuzzyFileSearchSessionUpdated):       {Params: "FuzzyFileSearchSessionUpdatedNotification"},
		string(NotificationHookCompleted):                       {Params: "HookCompletedNotification"},
		string(NotificationHookStarted):                         {Params: "HookStartedNotification"},
		string(NotificationGuardianWarning):                     {Params: "GuardianWarningNotification"},
		string(NotificationItemGuardianApprovalReviewCompleted): {Params: "ItemGuardianApprovalReviewCompletedNotification"},
		string(NotificationItemGuardianApprovalReviewStarted):   {Params: "ItemGuardianApprovalReviewStartedNotification"},
		string(NotificationItemCompleted):                       {Params: "ItemCompletedNotification"},
		string(NotificationItemStarted):                         {Params: "ItemStartedNotification"},
		string(NotificationMCPServerOauthLoginCompleted):        {Params: "McpServerOauthLoginCompletedNotification"},
		string(NotificationMCPServerStatusUpdated):              {Params: "McpServerStatusUpdatedNotification"},
		string(NotificationMCPToolCallProgress):                 {Params: "McpToolCallProgressNotification"},
		string(NotificationModelRerouted):                       {Params: "ModelReroutedNotification"},
		string(NotificationModelSafetyBufferingUpdated):         {Params: "ModelSafetyBufferingUpdatedNotification"},
		string(NotificationModelVerification):                   {Params: "ModelVerificationNotification"},
		string(NotificationPlanDelta):                           {Params: "PlanDeltaNotification"},
		string(NotificationProcessExited):                       {Params: "ProcessExitedNotification"},
		string(NotificationProcessOutputDelta):                  {Params: "ProcessOutputDeltaNotification"},
		string(NotificationRawResponseItemCompleted):            {Params: "RawResponseItemCompletedNotification"},
		string(NotificationRawResponseCompleted):                {Params: "RawResponseCompletedNotification"},
		string(NotificationReasoningSummaryPartAdded):           {Params: "ReasoningSummaryPartAddedNotification"},
		string(NotificationReasoningSummaryTextDelta):           {Params: "ReasoningSummaryTextDeltaNotification"},
		string(NotificationReasoningTextDelta):                  {Params: "ReasoningTextDeltaNotification"},
		string(NotificationRemoteControlStatusChanged):          {Params: "RemoteControlStatusChangedNotification"},
		string(NotificationServerRequestResolved):               {Params: "ServerRequestResolvedNotification"},
		string(NotificationSkillsChanged):                       {Params: "SkillsChangedNotification"},
		string(NotificationTerminalInteraction):                 {Params: "TerminalInteractionNotification"},
		string(NotificationThreadArchived):                      {Params: "ThreadArchivedNotification"},
		string(NotificationThreadClosed):                        {Params: "ThreadClosedNotification"},
		string(NotificationThreadDeleted):                       {Params: "ThreadDeletedNotification"},
		string(NotificationThreadGoalCleared):                   {Params: "ThreadGoalClearedNotification"},
		string(NotificationThreadGoalUpdated):                   {Params: "ThreadGoalUpdatedNotification"},
		string(NotificationThreadNameUpdated):                   {Params: "ThreadNameUpdatedNotification"},
		string(NotificationThreadRealtimeClosed):                {Params: "ThreadRealtimeClosedNotification"},
		string(NotificationThreadRealtimeError):                 {Params: "ThreadRealtimeErrorNotification"},
		string(NotificationThreadRealtimeItemAdded):             {Params: "ThreadRealtimeItemAddedNotification"},
		string(NotificationThreadRealtimeOutputAudioDelta):      {Params: "ThreadRealtimeOutputAudioDeltaNotification"},
		string(NotificationThreadRealtimeSDP):                   {Params: "ThreadRealtimeSdpNotification"},
		string(NotificationThreadRealtimeStarted):               {Params: "ThreadRealtimeStartedNotification"},
		string(NotificationThreadRealtimeTranscriptDelta):       {Params: "ThreadRealtimeTranscriptDeltaNotification"},
		string(NotificationThreadRealtimeTranscriptDone):        {Params: "ThreadRealtimeTranscriptDoneNotification"},
		string(NotificationThreadSettingsUpdated):               {Params: "ThreadSettingsUpdatedNotification"},
		string(NotificationThreadStarted):                       {Params: "ThreadStartedNotification"},
		string(NotificationThreadStatusChanged):                 {Params: "ThreadStatusChangedNotification"},
		string(NotificationThreadTokenUsageUpdated):             {Params: "ThreadTokenUsageUpdatedNotification"},
		string(NotificationThreadUnarchived):                    {Params: "ThreadUnarchivedNotification"},
		string(NotificationTurnCompleted):                       {Params: "TurnCompletedNotification"},
		string(NotificationTurnDiffUpdated):                     {Params: "TurnDiffUpdatedNotification"},
		string(NotificationTurnModerationMetadata):              {Params: "TurnModerationMetadataNotification"},
		string(NotificationTurnPlanUpdated):                     {Params: "TurnPlanUpdatedNotification"},
		string(NotificationTurnStarted):                         {Params: "TurnStartedNotification"},
		string(NotificationWarning):                             {Params: "WarningNotification"},
		string(NotificationWindowsSandboxSetupCompleted):        {Params: "WindowsSandboxSetupCompletedNotification"},
		string(NotificationWindowsWorldWritableWarning):         {Params: "WindowsWorldWritableWarningNotification"},
		"externalAgentConfig/import/completed":                  {Params: "ExternalAgentConfigImportCompletedNotification"},
		"externalAgentConfig/import/progress":                   {Params: "ExternalAgentConfigImportProgressNotification"},
	}
}

func renderTypeScriptSchema(schema *ProtocolSchema) string {
	data, _ := json.MarshalIndent(schema, "", "  ")
	return fmt.Sprintf(`// Generated by codex_go app-server schema tooling.

export interface ProtocolMethod {
  method: string;
  params?: string;
  result?: string;
  experimental?: boolean;
}

export interface ProtocolTypeEntry {
  name: string;
  kind: string;
}

export interface ProtocolSchema {
  $schema: string;
  title: string;
  version: string;
  experimental: boolean;
  methods: ProtocolMethod[];
  clientRequests: ProtocolMethod[];
  serverRequests: ProtocolMethod[];
  notifications: ProtocolMethod[];
  types: ProtocolTypeEntry[];
}

export const protocolSchema = %s as const;
`, string(data))
}

func writeGeneratedFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

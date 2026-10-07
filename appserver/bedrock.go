package appserver

// Amazon Bedrock account RPCs.
//
// Upstream Rust registers three experimental client requests in
// codex-rs/app-server-protocol/src/protocol/common.rs (BedrockDiscover,
// BedrockSetup, BedrockCheckGovCloudRequirements; #39277 / #49813 / #49817) and
// implements them in
// codex-rs/app-server/src/request_processors/account_processor/bedrock_setup.rs
// and bedrock_gov_cloud.rs, guarded by bedrock_auth.rs. The wire types live in
// codex-rs/app-server-protocol/src/protocol/v2/bedrock.rs.
//
// The Go app-server had the account/login* surface but none of these methods
// (grep -rn "account/bedrock" --include='*.go' = empty before this file), so the
// experimental Bedrock setup APIs were only reachable through a Rust app-server.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// AwsCredentialType mirrors v2::AwsCredentialType (serde rename_all = camelCase).
type AwsCredentialType string

const (
	AwsCredentialTypeAccessKeys    AwsCredentialType = "accessKeys"
	AwsCredentialTypeBedrockAPIKey AwsCredentialType = "bedrockApiKey"
)

// BedrockAwsProfile mirrors v2::BedrockAwsProfile. `region` is a nullable
// string on the wire (Rust Option<String> without skip_serializing_if), so the
// field is always emitted.
type BedrockAwsProfile struct {
	Name   string  `json:"name"`
	Region *string `json:"region"`
}

// BedrockEnvironmentCredential mirrors v2::BedrockEnvironmentCredential. The
// Rust field is `credential_type` renamed to `type` on the wire.
type BedrockEnvironmentCredential struct {
	CredentialType AwsCredentialType `json:"type"`
	Region         *string           `json:"region"`
}

// BedrockDiscoverParams mirrors v2::BedrockDiscoverParams (an empty object).
type BedrockDiscoverParams struct{}

// BedrockDiscoverResponse mirrors v2::BedrockDiscoverResponse.
type BedrockDiscoverResponse struct {
	Profiles               []BedrockAwsProfile            `json:"profiles"`
	EnvironmentCredentials []BedrockEnvironmentCredential `json:"environmentCredentials"`
}

// BedrockSetupType identifies the BedrockSetupParams variant. Rust models it as
// an internally tagged enum (`#[serde(tag = "type", rename_all = "camelCase")]`).
type BedrockSetupType string

const (
	BedrockSetupTypeProfile     BedrockSetupType = "profile"
	BedrockSetupTypeEnvironment BedrockSetupType = "environment"
)

// BedrockSetupParams mirrors v2::BedrockSetupParams: {type: "profile", profile,
// region} or {type: "environment", region}.
type BedrockSetupParams struct {
	Type    BedrockSetupType `json:"type"`
	Profile string           `json:"profile"`
	Region  string           `json:"region"`
}

// BedrockSetupResponse mirrors v2::BedrockSetupResponse (an empty object).
type BedrockSetupResponse struct{}

// BedrockCheckGovCloudRequirementsParams mirrors the empty params object.
type BedrockCheckGovCloudRequirementsParams struct{}

// BedrockCheckGovCloudRequirementsResponse mirrors
// v2::BedrockCheckGovCloudRequirementsResponse.
type BedrockCheckGovCloudRequirementsResponse struct {
	IsGovCloud bool `json:"isGovCloud"`
	ShouldWarn bool `json:"shouldWarn"`
}

const (
	awsRegionEnvVar            = "AWS_REGION"
	awsDefaultRegionEnvVar     = "AWS_DEFAULT_REGION"
	awsAccessKeyIDEnvVar       = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeyEnvVar   = "AWS_SECRET_ACCESS_KEY"
	awsProfileEnvVar           = "AWS_PROFILE"
	awsDefaultProfileEnvVar    = "AWS_DEFAULT_PROFILE"
	awsConfigFileEnvVar        = "AWS_CONFIG_FILE"
	awsSharedCredentialsEnvVar = "AWS_SHARED_CREDENTIALS_FILE"
	awsDefaultProfileName      = "default"
)

// bedrockGovCloudSupportedRegions mirrors Rust
// BEDROCK_GOV_CLOUD_SUPPORTED_REGIONS (codex-rs/model-provider/src/amazon_bedrock/mantle.rs:26).
var bedrockGovCloudSupportedRegions = map[string]struct{}{
	"us-gov-east-1": {},
	"us-gov-west-1": {},
}

// bedrockMantleSupportedRegions mirrors Rust BEDROCK_MANTLE_SUPPORTED_REGIONS.
// model/amazonBedrockMantleSupportedRegions holds the same set but is
// unexported and model/ is outside this work item's write scope, so the table is
// restated here with a test that keeps it aligned with the model package.
var bedrockMantleSupportedRegions = map[string]struct{}{
	"us-east-2":      {},
	"us-east-1":      {},
	"us-west-2":      {},
	"ap-southeast-3": {},
	"ap-south-1":     {},
	"ap-northeast-1": {},
	"eu-central-1":   {},
	"eu-west-1":      {},
	"eu-west-2":      {},
	"eu-south-1":     {},
	"eu-north-1":     {},
	"sa-east-1":      {},
}

// isSupportedAmazonBedrockRegion mirrors Rust is_supported_amazon_bedrock_region.
func isSupportedAmazonBedrockRegion(region string) bool {
	region = strings.TrimSpace(region)
	if _, ok := bedrockMantleSupportedRegions[region]; ok {
		return true
	}
	return isAmazonBedrockGovCloudRegion(region)
}

// isAmazonBedrockGovCloudRegion mirrors Rust is_amazon_bedrock_gov_cloud_region.
func isAmazonBedrockGovCloudRegion(region string) bool {
	_, ok := bedrockGovCloudSupportedRegions[strings.TrimSpace(region)]
	return ok
}

// isAmazonBedrockRuntimeProvider mirrors Rust ModelProviderInfo::is_amazon_bedrock_runtime.
func isAmazonBedrockRuntimeProvider(info *model.ProviderInfo) bool {
	if info == nil {
		return false
	}
	name := strings.TrimSpace(info.Name)
	return strings.EqualFold(name, model.AmazonBedrockRuntimeProviderName) ||
		strings.EqualFold(name, model.AmazonBedrockRuntimeProviderID)
}

// nonEmptyEnvVar mirrors Rust non_empty_env_var: an unset/blank variable yields "".
func nonEmptyEnvVar(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// ensureBedrockLoginAllowed mirrors AccountRequestProcessor::ensure_bedrock_login_allowed.
func (r *RuntimeRouter) ensureBedrockLoginAllowed() error {
	if auth.IsWorkloadIdentitySelected() {
		return jsonRPCInvalidRequest("Configured external authentication is owned by the app-server host and cannot be changed through account RPCs.")
	}
	if r.externalChatGPTAuthActive() {
		return jsonRPCInvalidRequest(externalChatGPTAuthActiveMessage)
	}
	cfg, err := r.effectiveAuthConfig()
	if err != nil {
		return err
	}
	if !cfg.IsLoginMethodAllowed(config.ForcedLoginMethodAPI) {
		return jsonRPCInvalidRequest("Amazon Bedrock login is disabled. Use ChatGPT login instead.")
	}
	return nil
}

// effectiveAuthConfig builds the config view used for auth policy decisions:
// the merged values plus the managed requirements the service resolved.
func (r *RuntimeRouter) effectiveAuthConfig() (*config.Config, error) {
	service := r.requireConfig()
	read, err := service.Read(&config.ConfigReadParams{})
	if err != nil {
		return nil, err
	}
	if read == nil {
		return nil, fmt.Errorf("failed to load configuration")
	}
	cfg := &config.Config{Values: read.Config}
	if requirements := service.Requirements(); requirements != nil {
		cfg.Requirements = requirements.Requirements
	}
	return cfg, nil
}

// handleBedrockDiscover mirrors AccountRequestProcessor::bedrock_discover.
func (r *RuntimeRouter) handleBedrockDiscover(request *Request) (*BedrockDiscoverResponse, error) {
	var params BedrockDiscoverParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureBedrockLoginAllowed(); err != nil {
		return nil, err
	}
	profiles, err := discoverAWSProfiles()
	if err != nil {
		return nil, fmt.Errorf("failed to discover AWS profiles: %w", err)
	}
	region := nonEmptyEnvVar(awsRegionEnvVar)
	credentials := make([]BedrockEnvironmentCredential, 0, 2)
	if nonEmptyEnvVar(awsAccessKeyIDEnvVar) != "" && nonEmptyEnvVar(awsSecretAccessKeyEnvVar) != "" {
		credentials = append(credentials, BedrockEnvironmentCredential{
			CredentialType: AwsCredentialTypeAccessKeys,
			Region:         stringPtrIfNotEmpty(region),
		})
	}
	if nonEmptyEnvVar(model.AmazonBedrockBearerTokenEnv) != "" {
		credentials = append(credentials, BedrockEnvironmentCredential{
			CredentialType: AwsCredentialTypeBedrockAPIKey,
			Region:         stringPtrIfNotEmpty(region),
		})
	}
	return &BedrockDiscoverResponse{Profiles: profiles, EnvironmentCredentials: credentials}, nil
}

// handleBedrockSetup mirrors AccountRequestProcessor::bedrock_setup.
func (r *RuntimeRouter) handleBedrockSetup(request *Request) (*BedrockSetupResponse, error) {
	var params BedrockSetupParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureBedrockLoginAllowed(); err != nil {
		return nil, err
	}
	if err := r.ensureUserModelProviderCanBeBedrock(); err != nil {
		return nil, err
	}
	setupType := BedrockSetupType(strings.TrimSpace(string(params.Type)))
	switch setupType {
	case BedrockSetupTypeProfile, BedrockSetupTypeEnvironment:
	default:
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("unknown Amazon Bedrock setup type `%s`", params.Type))
	}
	if setupType == BedrockSetupTypeEnvironment {
		if snapshot := r.setupAuthSnapshot(); snapshot != nil && snapshot.Mode() == "bedrock-api-key" {
			return nil, jsonRPCInvalidRequest("Codex-managed Bedrock credentials are already configured and take priority over AWS environment credentials. Run `codex logout` and try again.")
		}
	}
	region := strings.TrimSpace(params.Region)
	if !isSupportedAmazonBedrockRegion(region) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("Amazon Bedrock does not support region `%s`", region))
	}
	var profile *string
	if setupType == BedrockSetupTypeProfile {
		trimmed := strings.TrimSpace(params.Profile)
		if trimmed == "" {
			return nil, jsonRPCInvalidRequest("AWS profile name must not be empty.")
		}
		if err := validateAWSProfileCredentials(trimmed); err != nil {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("failed to load credentials for AWS profile `%s`: %v", trimmed, err))
		}
		profile = &trimmed
	} else {
		hasAPIKey := nonEmptyEnvVar(model.AmazonBedrockBearerTokenEnv) != ""
		hasAccessKeys := nonEmptyEnvVar(awsAccessKeyIDEnvVar) != "" && nonEmptyEnvVar(awsSecretAccessKeyEnvVar) != ""
		if !hasAPIKey && !hasAccessKeys {
			return nil, jsonRPCInvalidRequest("No AWS credentials found. Please Configure AWS credentials or complete AWS sign-in, then try again.")
		}
	}
	r.cancelAllAccountLoginRuntimes()
	r.requireAccount().CancelActiveLogins()
	if err := r.configureBedrockProvider(&region, profile); err != nil {
		return nil, err
	}
	return &BedrockSetupResponse{}, nil
}

// setupAuthSnapshot returns the Codex-managed auth snapshot, mirroring
// AccountRequestProcessor's AuthManager::auth_cached lookup.
func (r *RuntimeRouter) setupAuthSnapshot() *auth.AuthDotJSON {
	if r == nil {
		return nil
	}
	return r.requireAccount().AuthSnapshot()
}

// configureBedrockProvider mirrors bedrock_auth::configure_bedrock_provider.
func (r *RuntimeRouter) configureBedrockProvider(region *string, profile *string) error {
	edits := []config.ConfigEdit{
		{KeyPath: "model_provider", Value: model.AmazonBedrockProviderID, MergeStrategy: config.MergeReplace},
		{KeyPath: "model_providers.amazon-bedrock.aws.profile", Value: configEditValue(profile), MergeStrategy: config.MergeReplace},
	}
	if region != nil {
		edits = append(edits, config.ConfigEdit{
			KeyPath:       "model_providers.amazon-bedrock.aws.region",
			Value:         *region,
			MergeStrategy: config.MergeReplace,
		})
	}
	response, err := r.requireConfig().BatchWrite(&config.ConfigBatchWriteParams{Edits: edits})
	if err != nil {
		return err
	}
	if response != nil && response.OverriddenMetadata != nil {
		return jsonRPCInvalidRequest(fmt.Sprintf("Amazon Bedrock configuration cannot take effect: %s", response.OverriddenMetadata.Message))
	}
	return nil
}

// configEditValue renders an optional string as the Rust `serde_json::json!`
// value does: `None` becomes a JSON null, which the config writer treats as a
// key removal (Rust MergeStrategy::Replace semantics).
func configEditValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// ensureUserModelProviderCanBeBedrock mirrors
// bedrock_auth::ensure_user_model_provider_can_be_bedrock.
func (r *RuntimeRouter) ensureUserModelProviderCanBeBedrock() error {
	service := r.requireConfig()
	read, err := service.Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil {
		return fmt.Errorf("failed to load configuration layers: %w", err)
	}
	if read == nil {
		return fmt.Errorf("failed to load configuration layers")
	}
	userPrecedence, hasUserLayer := bedrockUserLayerPrecedence(read.Layers)
	if !hasUserLayer {
		userFile := ""
		if home := service.CodexHome(); strings.TrimSpace(home) != "" {
			userFile = config.ConfigPath(home)
		}
		fallback := config.LayerSource{Type: config.LayerSourceUser, File: userFile}
		userPrecedence = fallback.Precedence()
	}
	for idx := len(read.Layers) - 1; idx >= 0; idx-- {
		layer := read.Layers[idx]
		if layer.Name.Precedence() <= userPrecedence {
			continue
		}
		layerConfig, _ := layer.Config.(map[string]any)
		raw, ok := layerConfig["model_provider"]
		if !ok {
			continue
		}
		effective := strings.TrimSpace(stringValueFromAny(raw))
		if effective == model.AmazonBedrockProviderID {
			break
		}
		source := bedrockFormatConfigLayerSource(layer.Name)
		return jsonRPCInvalidRequest(fmt.Sprintf(
			"Amazon Bedrock login cannot select `%s` because %s sets `model_provider` to %s",
			model.AmazonBedrockProviderID, source, effective,
		))
	}
	if credentialExport, ok := bedrockCredentialExportAtPath(read.Config); ok && credentialExport != nil {
		return jsonRPCInvalidRequest("Amazon Bedrock is configured to use `aws.credential_export`. Please clear this setting to use another sign-in method.")
	}
	return nil
}

// bedrockUserLayerPrecedence returns the precedence of the highest-precedence
// user layer, mirroring ConfigLayerStack::get_active_user_layer.
func bedrockUserLayerPrecedence(layers []config.Layer) (int16, bool) {
	precedence := int16(0)
	found := false
	for _, layer := range layers {
		if layer.Name.Type != config.LayerSourceUser {
			continue
		}
		if !found || layer.Name.Precedence() > precedence {
			precedence = layer.Name.Precedence()
			found = true
		}
	}
	return precedence, found
}

func bedrockCredentialExportAtPath(values map[string]any) (any, bool) {
	providers, ok := values["model_providers"].(map[string]any)
	if !ok {
		return nil, false
	}
	bedrock, ok := providers[model.AmazonBedrockProviderID].(map[string]any)
	if !ok {
		return nil, false
	}
	aws, ok := bedrock["aws"].(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := aws["credential_export"]
	return value, ok
}

// bedrockFormatConfigLayerSource mirrors Rust format_config_layer_source
// (codex-rs/config/src/config_layer_source.rs:78). tui.FormatConfigLayerSource
// carries the same mapping but importing tui from appserver would add a cycle.
func bedrockFormatConfigLayerSource(source config.LayerSource) string {
	switch source.Type {
	case config.LayerSourcePackagedDefaults:
		return fmt.Sprintf("packaged defaults (%s)", source.File)
	case config.LayerSourceMDM:
		return fmt.Sprintf("MDM (%s:%s)", source.Domain, source.Key)
	case config.LayerSourceSystem:
		return fmt.Sprintf("system (%s)", source.File)
	case config.LayerSourceEnterpriseManaged:
		return fmt.Sprintf("enterprise-managed (%s, %s)", source.Name, source.ID)
	case config.LayerSourceUser:
		return fmt.Sprintf("user (%s)", source.File)
	case config.LayerSourceProject:
		return fmt.Sprintf("project (%s/config.toml)", source.DotCodexFolder)
	case config.LayerSourceSessionFlags:
		return "session-flags"
	case config.LayerSourceLegacyManagedConfigFromFile:
		return fmt.Sprintf("legacy managed_config.toml (%s)", source.File)
	case config.LayerSourceLegacyManagedConfigFromMDM:
		return "legacy managed_config.toml (MDM)"
	default:
		if source.Type != "" {
			return string(source.Type)
		}
		return "<unknown>"
	}
}

func stringValueFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

// handleBedrockCheckGovCloudRequirements mirrors
// AccountRequestProcessor::bedrock_check_gov_cloud_requirements.
func (r *RuntimeRouter) handleBedrockCheckGovCloudRequirements(request *Request) (*BedrockCheckGovCloudRequirementsResponse, error) {
	var params BedrockCheckGovCloudRequirementsParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	service := r.requireConfig()
	// Rust loads the latest configuration (including managed requirements) on
	// every call (ConfigManager::load_latest_config), so a requirements.toml
	// written after startup takes effect without a restart. The Go config
	// service caches requirements at construction, so refresh them here first;
	// an explicitly installed override is preserved.
	if err := service.ReloadRequirementsFromHome(); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	read, err := service.Read(&config.ConfigReadParams{})
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	if read == nil {
		return nil, fmt.Errorf("failed to load configuration")
	}
	providerID := strings.TrimSpace(stringFromMap(read.Config, "model_provider"))
	providerInfo, err := model.ProviderForConfigID(read.Config, providerID, strings.TrimSpace(stringFromMap(read.Config, "openai_base_url")))
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	if providerInfo == nil || !providerInfo.IsAmazonBedrock() {
		return &BedrockCheckGovCloudRequirementsResponse{}, nil
	}

	var endpointDomain string
	if baseURL := strings.TrimSpace(providerInfo.BaseURL); baseURL != "" {
		parsed, parseErr := url.Parse(baseURL)
		if parseErr != nil || strings.TrimSpace(parsed.Host) == "" {
			return nil, fmt.Errorf("Amazon Bedrock endpoint has no valid hostname")
		}
		endpointDomain = strings.TrimSuffix(parsed.Host, ".")
	}
	endpointRegion := amazonBedrockEndpointRegion(endpointDomain)
	region := endpointRegion
	if region == "" {
		resolved, resolveErr := r.resolveAmazonBedrockRegion(*providerInfo)
		if resolveErr != nil {
			return nil, fmt.Errorf("failed to resolve Amazon Bedrock region: %w", resolveErr)
		}
		region = resolved
	}
	isGovCloud := isAmazonBedrockGovCloudRegion(region)
	shouldWarn := false
	if isGovCloud {
		domain := endpointDomain
		if domain == "" {
			if isAmazonBedrockRuntimeProvider(providerInfo) {
				domain = fmt.Sprintf("bedrock-runtime.%s.amazonaws.com", region)
			} else {
				domain = fmt.Sprintf("bedrock-mantle.%s.api.aws", region)
			}
		}
		shouldWarn = !bedrockRequirementsAllowGovCloud(service.Requirements(), domain)
	}
	return &BedrockCheckGovCloudRequirementsResponse{IsGovCloud: isGovCloud, ShouldWarn: shouldWarn}, nil
}

// amazonBedrockEndpointRegion extracts the region from an official Bedrock
// endpoint host, mirroring the endpoint_region closure in bedrock_gov_cloud.rs.
func amazonBedrockEndpointRegion(domain string) string {
	service, rest, ok := strings.Cut(domain, ".")
	if !ok {
		return ""
	}
	var region string
	switch service {
	case "bedrock-mantle":
		region = strings.TrimSuffix(rest, ".api.aws")
	case "bedrock-runtime", "bedrock-runtime-fips":
		region = strings.TrimSuffix(rest, ".amazonaws.com")
	default:
		return ""
	}
	// Require a single region label, not a substring in a proxy hostname.
	if region == "" || strings.Contains(region, ".") {
		return ""
	}
	return region
}

// bedrockRequirementsAllowGovCloud mirrors the api_only / network_allowed
// baseline check in bedrock_gov_cloud.rs.
func bedrockRequirementsAllowGovCloud(requirements *config.ConfigRequirementsReadResponse, domain string) bool {
	var resolved *config.ConfigRequirements
	if requirements != nil {
		resolved = requirements.Requirements
	}
	apiOnly := false
	if resolved != nil && resolved.AllowedLoginMethods != nil && len(resolved.AllowedLoginMethods) > 0 {
		apiOnly = true
		for _, method := range resolved.AllowedLoginMethods {
			if method != config.ForcedLoginMethodAPI {
				apiOnly = false
				break
			}
		}
	}
	networkAllowed := false
	if resolved != nil && resolved.Application != nil && resolved.Application.Network != nil {
		network := resolved.Application.Network
		if network.Enabled && network.Domains[domain] == config.NetworkAllow {
			networkAllowed = true
		}
	}
	return apiOnly && networkAllowed
}

// resolveAmazonBedrockRegion mirrors codex_model_provider::resolve_amazon_bedrock_region:
// Codex-managed Bedrock credentials win, then the standard AWS region chain.
func (r *RuntimeRouter) resolveAmazonBedrockRegion(providerInfo model.ProviderInfo) (string, error) {
	if region := r.managedBedrockRegion(); region != "" {
		return region, nil
	}
	awsConfig := &auth.AWSAuthConfig{Service: model.AmazonBedrockMantleServiceName}
	if providerInfo.AWS != nil {
		awsConfig.Profile = strings.TrimSpace(providerInfo.AWS.Profile)
		awsConfig.Region = strings.TrimSpace(providerInfo.AWS.Region)
	}
	return auth.ResolveAWSRegionWithOptions(awsConfig, nil)
}

// managedBedrockRegion returns the region baked into a Codex-managed Bedrock
// API key, matching Rust's bedrock-api-key auth source precedence.
func (r *RuntimeRouter) managedBedrockRegion() string {
	if r == nil {
		return ""
	}
	snapshot := r.setupAuthSnapshot()
	if snapshot == nil || snapshot.Mode() != "bedrock-api-key" {
		return ""
	}
	switch key := snapshot.BedrockAPIKey.(type) {
	case *auth.BedrockAPIKeyAuth:
		if key != nil {
			return strings.TrimSpace(key.Region)
		}
	case auth.BedrockAPIKeyAuth:
		return strings.TrimSpace(key.Region)
	case map[string]any:
		if region, ok := key["region"].(string); ok {
			return strings.TrimSpace(region)
		}
	}
	return ""
}

// discoverAWSProfiles mirrors codex_aws_auth::discover_aws_profiles: enumerate
// the AWS shared config and credentials files, sort the selected profile first,
// then alphabetically.
func discoverAWSProfiles() ([]BedrockAwsProfile, error) {
	configPath, credentialsPath, err := awsSharedConfigFiles()
	if err != nil {
		return nil, err
	}
	names := map[string]struct{}{}
	for _, file := range []struct {
		path     string
		isConfig bool
	}{{configPath, true}, {credentialsPath, false}} {
		sections, err := parseAWSSharedConfigFile(file.path, file.isConfig)
		if err != nil {
			return nil, err
		}
		for name := range sections {
			names[name] = struct{}{}
		}
	}
	selected := nonEmptyEnvVar(awsProfileEnvVar)
	if selected == "" {
		selected = nonEmptyEnvVar(awsDefaultProfileEnvVar)
	}
	if selected == "" {
		selected = awsDefaultProfileName
	}
	profiles := make([]BedrockAwsProfile, 0, len(names))
	for name := range names {
		profile := BedrockAwsProfile{Name: name}
		// The profile's region comes from the same SDK loader Rust's
		// ProfileSet uses; only the profile *names* need the local INI reader
		// because the Go AWS SDK exposes no profile-listing API.
		if loaded, err := loadAWSSharedConfigProfile(name); err == nil {
			profile.Region = stringPtrIfNotEmpty(strings.TrimSpace(loaded.Region))
		}
		profiles = append(profiles, profile)
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		leftSelected := profiles[i].Name == selected
		rightSelected := profiles[j].Name == selected
		if leftSelected != rightSelected {
			return leftSelected
		}
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, nil
}

// parseAWSSharedConfigFile reads a shared config/credentials INI file. Config
// file sections may be written `[profile NAME]` or `[NAME]`; credentials files
// use `[NAME]`. Missing files are ignored, matching the AWS SDK profile loader.
func parseAWSSharedConfigFile(path string, isConfig bool) (map[string]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sections := map[string]map[string]string{}
	current := ""
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			if isConfig {
				name = strings.TrimSpace(strings.TrimPrefix(name, "profile "))
			}
			if name == "" {
				current = ""
				continue
			}
			current = name
			if sections[current] == nil {
				sections[current] = map[string]string{}
			}
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		sections[current][strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return sections, nil
}

// awsSharedConfigFiles resolves the shared config/credentials file paths the
// way the AWS SDK's profile loader does (AWS_CONFIG_FILE /
// AWS_SHARED_CREDENTIALS_FILE, then ~/.aws/{config,credentials}).
func awsSharedConfigFiles() (string, string, error) {
	home, homeErr := os.UserHomeDir()
	configPath := nonEmptyEnvVar(awsConfigFileEnvVar)
	if configPath == "" {
		if homeErr != nil {
			return "", "", homeErr
		}
		configPath = filepath.Join(home, ".aws", "config")
	}
	credentialsPath := nonEmptyEnvVar(awsSharedCredentialsEnvVar)
	if credentialsPath == "" {
		if homeErr != nil {
			return "", "", homeErr
		}
		credentialsPath = filepath.Join(home, ".aws", "credentials")
	}
	return configPath, credentialsPath, nil
}

// validateAWSProfileCredentials mirrors codex_aws_auth::validate_aws_profile:
// resolve credentials from the named profile in the shared config and
// credentials files only, without falling back to the process environment.
func validateAWSProfileCredentials(profile string) error {
	loaded, err := loadAWSSharedConfigProfile(profile)
	if err != nil {
		return err
	}
	if !sharedConfigHasCredentials(loaded) {
		return fmt.Errorf("the credentials provider returned no credentials for profile `%s`", profile)
	}
	return nil
}

// sharedConfigHasCredentials reports whether a loaded profile supplies
// credentials, either directly, through a configured credential source, or via
// a source_profile chain.
func sharedConfigHasCredentials(loaded awsconfig.SharedConfig) bool {
	if strings.TrimSpace(loaded.Credentials.AccessKeyID) != "" ||
		strings.TrimSpace(loaded.CredentialSource) != "" {
		return true
	}
	if loaded.Source != nil {
		return sharedConfigHasCredentials(*loaded.Source)
	}
	return false
}

// loadAWSSharedConfigProfile loads one profile from the shared config and
// credentials files with the AWS SDK's profile loader, mirroring Rust's
// codex_aws_auth ProfileFileCredentialsProvider input.
func loadAWSSharedConfigProfile(profile string) (awsconfig.SharedConfig, error) {
	configPath, credentialsPath, err := awsSharedConfigFiles()
	if err != nil {
		return awsconfig.SharedConfig{}, err
	}
	return awsconfig.LoadSharedConfigProfile(context.Background(), profile, func(options *awsconfig.LoadSharedConfigOptions) {
		options.ConfigFiles = []string{configPath}
		options.CredentialsFiles = []string{credentialsPath}
	})
}

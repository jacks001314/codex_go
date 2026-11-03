package appserver

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"codex_go/config"
	"codex_go/model"
)

// amazonBedrockMantleProviderName is the display name Rust uses for the
// amazon-bedrock provider id (#58845); the provider name constant still reads
// "Amazon Bedrock" because it identifies the auth family.
const amazonBedrockMantleProviderName = "Amazon Bedrock Mantle"

// handleModelProviderList mirrors Rust #58845: it reports OpenAI plus every
// explicitly configured provider, the provider-specific settings a client may
// edit, and the effective default provider.
func (r *RuntimeRouter) handleModelProviderList(request *Request) (*model.ProviderListResponse, error) {
	var params model.ProviderListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if r.services.Config == nil {
		return nil, fmt.Errorf("failed to read providers: config service is unavailable")
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil {
		return nil, fmt.Errorf("failed to read providers: %w", err)
	}
	effective := read.Config
	if effective == nil {
		effective = map[string]any{}
	}
	layers := read.Layers
	var managed *config.ConfigRequirements
	if requirementsRead := r.services.Config.Requirements(); requirementsRead != nil {
		managed = requirementsRead.Requirements
	}
	configured := map[string]any{}
	if table, ok := effective["model_providers"].(map[string]any); ok {
		for id, definition := range table {
			configured[id] = definition
		}
	}
	if managed != nil {
		// Rust merges required provider definitions into the effective config,
		// where a required definition replaces the local entry wholesale.
		for id, definition := range managed.ModelProviders {
			configured[id] = definition
		}
	}

	defaultID := model.OpenAIProviderID
	if managed != nil && managed.ModelProvider != nil && strings.TrimSpace(*managed.ModelProvider) != "" {
		defaultID = strings.TrimSpace(*managed.ModelProvider)
	} else if selected := strings.TrimSpace(stringFromMap(effective, "model_provider")); selected != "" {
		defaultID = selected
	}

	userLayer := activeUserLayer(layers)
	canChangeDefault := (managed == nil || managed.ModelProvider == nil) && userLayer != nil
	if canChangeDefault {
		if selection := highestModelProviderSelectionLayer(layers); selection != nil && selection.Name.Precedence() > userLayer.Name.Precedence() {
			canChangeDefault = false
		}
	}

	ids := make([]string, 0, len(configured)+1)
	for id := range configured {
		// Local OSS built-ins cannot have configured provider tables.
		if id == model.OllamaOSSProviderID || id == model.LMStudioOSSProviderID {
			continue
		}
		ids = append(ids, id)
	}
	ids = append(ids, model.OpenAIProviderID)
	sort.Strings(ids)
	deduped := ids[:0]
	for index, id := range ids {
		if index > 0 && ids[index-1] == id {
			continue
		}
		deduped = append(deduped, id)
	}

	data := make([]model.ProviderSummary, 0, len(deduped))
	for _, id := range deduped {
		providerConfig, _ := configured[id].(map[string]any)
		data = append(data, summarizeModelProvider(id, providerConfig, effective, layers, userLayer, managed))
	}
	return &model.ProviderListResponse{
		Data:              data,
		DefaultProviderID: defaultID,
		CanChangeDefault:  canChangeDefault,
	}, nil
}

func summarizeModelProvider(id string, providerConfig map[string]any, effective map[string]any, layers []config.Layer, userLayer *config.Layer, managed *config.ConfigRequirements) model.ProviderSummary {
	var displayName string
	switch id {
	case model.AmazonBedrockProviderID:
		displayName = amazonBedrockMantleProviderName
	case model.AmazonBedrockRuntimeProviderID:
		displayName = model.AmazonBedrockRuntimeProviderName
	case model.GCPProviderID:
		displayName = model.GCPProviderName
	case model.OpenAIProviderID:
		displayName = model.OpenAIProviderName
	default:
		displayName = id
		if name := providerStringField(providerConfig, "name"); name != nil {
			displayName = *name
		}
	}
	builtinAuth := displayName == model.OpenAIProviderName ||
		displayName == model.AmazonBedrockProviderName ||
		displayName == model.AmazonBedrockRuntimeProviderName ||
		displayName == model.GCPProviderName

	var rawBaseURL string
	var hasBaseURL bool
	if id == model.OpenAIProviderID {
		rawBaseURL, hasBaseURL = effective["openai_base_url"].(string)
	} else if value := providerStringField(providerConfig, "base_url"); value != nil {
		rawBaseURL = *value
		hasBaseURL = true
	}
	baseURL, urlHasAuth := displayableProviderBaseURL(rawBaseURL, hasBaseURL)

	settings := model.ProviderSettings{Type: "customGateway", Auth: &model.GatewayAuth{Mode: "none"}}
	switch id {
	case model.OpenAIProviderID:
		settings = model.ProviderSettings{Type: "openai"}
	case model.AmazonBedrockProviderID, model.AmazonBedrockRuntimeProviderID:
		aws := providerMapField(providerConfig, "aws")
		settings = model.ProviderSettings{
			Type:    "bedrock",
			Profile: providerStringField(aws, "profile"),
			Region:  providerStringField(aws, "region"),
		}
	case model.GCPProviderID:
		settings = model.ProviderSettings{
			Type:           "gcp",
			ProjectID:      providerStringField(providerConfig, "project_id"),
			Location:       providerStringField(providerConfig, "location"),
			CredentialFile: providerStringField(providerConfig, "credential_file"),
		}
	default:
		envKey := providerStringField(providerConfig, "env_key")
		auth := model.GatewayAuth{Mode: "none"}
		switch {
		case builtinAuth || urlHasAuth || hasExternalProviderAuth(providerConfig):
			auth = model.GatewayAuth{Mode: "externallyConfigured", EnvKey: envKey}
		case envKey != nil:
			auth = model.GatewayAuth{Mode: "environmentVariable", EnvKey: envKey}
		}
		settings = model.ProviderSettings{Type: "customGateway", Auth: &auth}
	}

	canEdit := canEditModelProvider(id, providerConfig, settings, strings.TrimSpace(displayName) == "" || builtinAuth, layers, userLayer, managed)
	return model.ProviderSummary{
		ID:       id,
		Name:     displayName,
		CanEdit:  canEdit,
		BaseURL:  baseURL,
		Settings: settings,
	}
}

// displayableProviderBaseURL keeps the configured URL only when a client can
// safely display it: Rust drops endpoints with credentials, query strings,
// fragments, non-http(s) schemes, or no host. urlHasAuth reports credentials
// separately because it also selects externally configured gateway auth.
func displayableProviderBaseURL(raw string, hasValue bool) (*string, bool) {
	if !hasValue {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	urlHasAuth := false
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword || parsed.User.Username() != "" {
			urlHasAuth = true
		}
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		urlHasAuth = true
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Host == "" || urlHasAuth || parsed.Fragment != "" {
		return nil, urlHasAuth
	}
	value := raw
	return &value, urlHasAuth
}

// canEditModelProvider mirrors Rust's can_edit_provider: a provider is editable
// when the active user layer can still change at least one supported field
// without a higher-precedence layer pinning it.
func canEditModelProvider(id string, providerConfig map[string]any, settings model.ProviderSettings, requiresRename bool, layers []config.Layer, userLayer *config.Layer, managed *config.ConfigRequirements) bool {
	if userLayer == nil || id == "" {
		return false
	}
	if managed != nil {
		if _, ok := managed.ModelProviders[id]; ok {
			return false
		}
	}
	var fields []string
	switch settings.Type {
	case "bedrock":
		if _, ok := providerConfig["auth"]; ok {
			fields = nil
		} else if aws := providerMapField(providerConfig, "aws"); aws != nil {
			if _, ok := aws["credential_export"]; ok {
				fields = []string{"aws.region"}
			} else {
				fields = []string{"aws.profile", "aws.region"}
			}
		} else {
			fields = []string{"aws.profile", "aws.region"}
		}
	case "gcp":
		fields = []string{"project_id", "location", "credential_file"}
	case "openai":
		fields = nil
	case "customGateway":
		switch {
		case requiresRename:
			fields = []string{"name"}
		case settings.Auth != nil && settings.Auth.Mode == "externallyConfigured":
			fields = []string{"name", "base_url"}
		default:
			fields = []string{"name", "base_url", "env_key"}
		}
	}
	for _, field := range fields {
		overridden := false
		for index := len(layers) - 1; index >= 0; index-- {
			if layers[index].Name.Precedence() <= userLayer.Name.Precedence() {
				continue
			}
			values, _ := layers[index].Config.(map[string]any)
			providers, _ := values["model_providers"].(map[string]any)
			entry, _ := providers[id].(map[string]any)
			if entry == nil {
				continue
			}
			if providerFieldPresent(entry, field) {
				overridden = true
				break
			}
		}
		if !overridden {
			return true
		}
	}
	return false
}

// hasExternalProviderAuth mirrors Rust's has_external_auth for custom
// gateways whose credentials live outside the gateway form.
func hasExternalProviderAuth(providerConfig map[string]any) bool {
	for _, key := range []string{"auth", "gateway_oauth", "aws", "experimental_bearer_token"} {
		if _, ok := providerConfig[key]; ok {
			return true
		}
	}
	if required, ok := providerConfig["requires_openai_auth"].(bool); ok && required {
		return true
	}
	for _, key := range []string{"http_headers", "env_http_headers", "query_params"} {
		if table, ok := providerConfig[key].(map[string]any); ok && len(table) > 0 {
			return true
		}
	}
	return false
}

// activeUserLayer returns the highest-precedence enabled user layer, which is
// Rust's writable user layer for provider discovery. The Go config service
// reads the user layer unless a loader override suppresses it, so the only
// remaining check is the disabled marker.
func activeUserLayer(layers []config.Layer) *config.Layer {
	for index := len(layers) - 1; index >= 0; index-- {
		if layers[index].Name.Type != config.LayerSourceUser {
			continue
		}
		if layers[index].DisabledReason != nil {
			return nil
		}
		return &layers[index]
	}
	return nil
}

// highestModelProviderSelectionLayer returns the highest-precedence layer that
// pins model_provider, ignoring disabled layers like Rust's
// layers_high_to_low lookup.
func highestModelProviderSelectionLayer(layers []config.Layer) *config.Layer {
	for index := len(layers) - 1; index >= 0; index-- {
		values, _ := layers[index].Config.(map[string]any)
		if values == nil {
			continue
		}
		if _, ok := values["model_provider"]; ok {
			return &layers[index]
		}
	}
	return nil
}

func providerStringField(values map[string]any, key string) *string {
	if values == nil {
		return nil
	}
	value, ok := values[key]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return nil
	}
	return &text
}

func providerMapField(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	table, _ := values[key].(map[string]any)
	return table
}

func providerFieldPresent(entry map[string]any, path string) bool {
	var current any = entry
	for _, key := range strings.Split(path, ".") {
		table, ok := current.(map[string]any)
		if !ok {
			return false
		}
		value, ok := table[key]
		if !ok {
			return false
		}
		current = value
	}
	return true
}

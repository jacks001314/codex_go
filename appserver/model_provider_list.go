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

// modelProviderCatalog is the configuration view shared by provider discovery
// and provider writes (#58845/#58876).
type modelProviderCatalog struct {
	effective  map[string]any
	layers     []config.Layer
	userLayer  *config.Layer
	managed    *config.ConfigRequirements
	builtins   map[string]model.ProviderInfo
	configured map[string]any
}

func loadModelProviderCatalog(r *RuntimeRouter) (*modelProviderCatalog, error) {
	if r == nil || r.services.Config == nil {
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
	var managed *config.ConfigRequirements
	if requirementsRead := r.services.Config.Requirements(); requirementsRead != nil {
		managed = requirementsRead.Requirements
	}
	catalog := &modelProviderCatalog{
		effective: effective,
		layers:    read.Layers,
		userLayer: activeUserLayer(read.Layers),
		managed:   managed,
		builtins:  model.BuiltInProviders(""),
	}
	catalog.configured = map[string]any{}
	if table, ok := effective["model_providers"].(map[string]any); ok {
		for id, definition := range table {
			catalog.configured[id] = definition
		}
	}
	if managed != nil {
		// Rust merges required provider definitions into the effective config,
		// where a required definition replaces the local entry wholesale.
		for id, definition := range managed.ModelProviders {
			catalog.configured[id] = definition
		}
	}
	return catalog, nil
}

// defaultProviderID mirrors Rust's required_model_provider() fallback chain.
func (c *modelProviderCatalog) defaultProviderID() string {
	if c == nil {
		return model.OpenAIProviderID
	}
	if c.managed != nil && c.managed.ModelProvider != nil && strings.TrimSpace(*c.managed.ModelProvider) != "" {
		return strings.TrimSpace(*c.managed.ModelProvider)
	}
	if selected := strings.TrimSpace(stringFromMap(c.effective, "model_provider")); selected != "" {
		return selected
	}
	return model.OpenAIProviderID
}

func (c *modelProviderCatalog) tableFor(id string) map[string]any {
	table, _ := c.configured[id].(map[string]any)
	return table
}

// summarize mirrors Rust's summarize_provider: safe display data only.
func (c *modelProviderCatalog) summarize(id string) model.ProviderSummary {
	table := c.tableFor(id)
	builtin, isBuiltin := c.builtins[id]
	displayName := id
	switch {
	case isBuiltin && id == model.AmazonBedrockProviderID:
		displayName = amazonBedrockMantleProviderName
	case isBuiltin:
		displayName = builtin.Name
	default:
		if name := providerStringField(table, "name"); name != nil {
			displayName = *name
		}
	}
	builtinAuth := usesBuiltinProviderAuth(displayName)
	requiresRename := !isBuiltin && (strings.TrimSpace(displayName) == "" || builtinAuth)
	externalAuth := builtinAuth || hasExternalProviderAuth(table)

	var rawBaseURL string
	var hasBaseURL bool
	if id == model.OpenAIProviderID {
		rawBaseURL, hasBaseURL = c.effective["openai_base_url"].(string)
	} else if value := providerStringField(table, "base_url"); value != nil {
		rawBaseURL, hasBaseURL = *value, true
	}
	baseURL, _ := displayableProviderBaseURL(rawBaseURL, hasBaseURL)

	settings := model.ProviderSettings{Type: "customGateway", Auth: &model.GatewayAuth{Mode: "none"}}
	switch {
	case id == model.OpenAIProviderID:
		settings = model.ProviderSettings{Type: "openai"}
	case id == model.AmazonBedrockProviderID || id == model.AmazonBedrockRuntimeProviderID:
		aws := providerMapField(table, "aws")
		settings = model.ProviderSettings{
			Type:    "bedrock",
			Profile: providerStringField(aws, "profile"),
			Region:  providerStringField(aws, "region"),
		}
	case id == model.GCPProviderID:
		settings = model.ProviderSettings{
			Type:           "gcp",
			ProjectID:      providerStringField(table, "project_id"),
			Location:       providerStringField(table, "location"),
			CredentialFile: providerStringField(table, "credential_file"),
		}
	default:
		envKey := providerStringField(table, "env_key")
		auth := model.GatewayAuth{Mode: "none"}
		switch {
		case externalAuth:
			auth = model.GatewayAuth{Mode: "externallyConfigured", EnvKey: envKey}
		case envKey != nil:
			auth = model.GatewayAuth{Mode: "environmentVariable", EnvKey: envKey}
		}
		settings = model.ProviderSettings{Type: "customGateway", Auth: &auth}
	}

	canEdit := false
	for _, field := range c.supportedFields(id) {
		if requiresRename && field != "name" {
			continue
		}
		if field == "env_key" && externalAuth {
			continue
		}
		if c.fieldWritable(id, field) {
			canEdit = true
			break
		}
	}
	return model.ProviderSummary{
		ID:       id,
		Name:     displayName,
		CanEdit:  canEdit,
		BaseURL:  baseURL,
		Settings: settings,
	}
}

// supportedFields mirrors Rust's supported_fields: the provider fields a client
// may patch. An empty result means the provider exposes no editable settings.
func (c *modelProviderCatalog) supportedFields(id string) []string {
	table := c.tableFor(id)
	_, isBuiltin := c.builtins[id]
	switch {
	case isBuiltin && (id == model.AmazonBedrockProviderID || id == model.AmazonBedrockRuntimeProviderID):
		if _, ok := table["auth"]; ok {
			return nil
		}
		if aws := providerMapField(table, "aws"); aws != nil {
			if _, ok := aws["credential_export"]; ok {
				return []string{"aws.region"}
			}
		}
		return []string{"aws.profile", "aws.region"}
	case isBuiltin && id == model.GCPProviderID:
		return []string{"project_id", "location", "credential_file"}
	case isBuiltin:
		return nil
	default:
		return []string{"name", "base_url", "env_key"}
	}
}

// fieldWritable mirrors Rust's field_is_writable: the active user layer must be
// able to change the field, no managed requirement may pin the provider, and no
// higher-precedence layer may already set it.
func (c *modelProviderCatalog) fieldWritable(id string, field string) bool {
	user := c.userLayer
	if user == nil || id == "" {
		return false
	}
	if c.managed != nil {
		if _, ok := c.managed.ModelProviders[id]; ok {
			return false
		}
	}
	for index := len(c.layers) - 1; index >= 0; index-- {
		if c.layers[index].Name.Precedence() <= user.Name.Precedence() {
			continue
		}
		values, _ := c.layers[index].Config.(map[string]any)
		providers, _ := values["model_providers"].(map[string]any)
		entry, _ := providers[id].(map[string]any)
		if entry == nil {
			continue
		}
		if providerFieldPresent(entry, field) {
			return false
		}
	}
	return true
}

// handleModelProviderList mirrors Rust #58845.
func (r *RuntimeRouter) handleModelProviderList(request *Request) (*model.ProviderListResponse, error) {
	var params model.ProviderListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.listModelProviders()
}

// ModelProviderList exposes the modelProvider/list result to in-process TUI
// clients (Rust #59541 app::provider).
func (r *RuntimeRouter) ModelProviderList() (*model.ProviderListResponse, error) {
	return r.listModelProviders()
}

func (r *RuntimeRouter) listModelProviders() (*model.ProviderListResponse, error) {
	catalog, err := loadModelProviderCatalog(r)
	if err != nil {
		return nil, err
	}
	userLayer := catalog.userLayer
	canChangeDefault := (catalog.managed == nil || catalog.managed.ModelProvider == nil) && userLayer != nil
	if canChangeDefault {
		if selection := highestModelProviderSelectionLayer(catalog.layers); selection != nil && selection.Name.Precedence() > userLayer.Name.Precedence() {
			canChangeDefault = false
		}
	}

	ids := make([]string, 0, len(catalog.configured)+1)
	for id := range catalog.configured {
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
		data = append(data, catalog.summarize(id))
	}
	return &model.ProviderListResponse{
		Data:              data,
		DefaultProviderID: catalog.defaultProviderID(),
		CanChangeDefault:  canChangeDefault,
	}, nil
}

func usesBuiltinProviderAuth(name string) bool {
	return name == model.OpenAIProviderName ||
		name == model.AmazonBedrockProviderName ||
		name == model.AmazonBedrockRuntimeProviderName ||
		name == model.GCPProviderName
}

// hasExternalProviderAuth mirrors Rust's has_external_auth: credentials carried
// by the endpoint or by fields the gateway form does not expose.
func hasExternalProviderAuth(providerConfig map[string]any) bool {
	if providerConfig == nil {
		return false
	}
	if baseURL := providerStringField(providerConfig, "base_url"); baseURL != nil {
		if _, hasAuth := parseProviderEndpoint(*baseURL); hasAuth {
			return true
		}
	}
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

// parseProviderEndpoint reports whether a URL may be displayed to a client and
// whether it carries credentials (userinfo or a query string).
func parseProviderEndpoint(raw string) (bool, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false, false
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
	safe := (scheme == "http" || scheme == "https") && parsed.Host != "" && !urlHasAuth && parsed.Fragment == ""
	return safe, urlHasAuth
}

// displayableProviderBaseURL keeps the configured URL only when a client can
// safely display it: Rust drops endpoints with credentials, query strings,
// fragments, non-http(s) schemes, or no host.
func displayableProviderBaseURL(raw string, hasValue bool) (*string, bool) {
	if !hasValue {
		return nil, false
	}
	safe, urlHasAuth := parseProviderEndpoint(raw)
	if !safe {
		return nil, urlHasAuth
	}
	value := raw
	return &value, urlHasAuth
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

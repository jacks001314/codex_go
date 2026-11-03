package appserver

import (
	"fmt"
	"path/filepath"
	"strings"

	"codex_go/config"
	"codex_go/model"
)

// handleModelProviderConfigWrite mirrors Rust #58876: apply a provider field
// patch through the shared configuration queue, then return the saved provider
// using the same safe summary as modelProvider/list.
func (r *RuntimeRouter) handleModelProviderConfigWrite(request *Request) (*model.ProviderConfigWriteResponse, error) {
	var params model.ProviderConfigWriteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	catalog, err := loadModelProviderCatalog(r)
	if err != nil {
		return nil, err
	}
	providerID, edits, err := modelProviderWriteEdits(&params, catalog)
	if err != nil {
		return nil, err
	}
	service := r.requireConfig()
	writeParams := &config.ConfigBatchWriteParams{Edits: edits}
	response, err := service.BatchWrite(writeParams)
	if err != nil {
		return nil, err
	}
	if response != nil && response.Status == config.WriteOKOverridden {
		message := "Configuration was overridden by a higher-precedence layer"
		if response.OverriddenMetadata != nil && strings.TrimSpace(response.OverriddenMetadata.Message) != "" {
			message = response.OverriddenMetadata.Message
		}
		return nil, providerConfigWriteError(config.ConfigWriteLayerReadonly, message)
	}
	r.applyConfigBatchWriteMutation(writeParams)
	saved, err := loadModelProviderCatalog(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read saved provider: %w", err)
	}
	// Saving the provider model/list is using invalidates its catalog for the
	// next access; edits to other providers must not.
	if saved.defaultProviderID() == providerID {
		r.invalidateModelCatalog()
	}
	return &model.ProviderConfigWriteResponse{Provider: saved.summarize(providerID)}, nil
}

type modelProviderWriteField struct {
	path  string
	value *string
}

// modelProviderWriteEdits mirrors Rust's provider_edits: resolve the target
// provider, validate the patch, and produce user-layer config edits.
func modelProviderWriteEdits(params *model.ProviderConfigWriteParams, catalog *modelProviderCatalog) (string, []config.ConfigEdit, error) {
	if params == nil {
		return "", nil, providerConfigInvalid("Provider settings are required.")
	}
	patch := params.Settings
	var providerID string
	var fields []modelProviderWriteField
	switch patch.Type {
	case "customGateway":
		custom := patch.CustomGateway
		if custom == nil {
			return "", nil, providerConfigInvalid("Provider settings are required.")
		}
		target := ""
		if custom.ProviderID != nil {
			target = strings.TrimSpace(*custom.ProviderID)
		}
		if target != "" {
			if _, ok := catalog.builtins[target]; ok || target == model.LegacyOllamaChatProviderID {
				return "", nil, providerConfigInvalid("A custom gateway cannot use a built-in provider ID.")
			}
		}
		var existing map[string]any
		if target != "" {
			existing = catalog.tableFor(target)
			if existing == nil {
				return "", nil, providerConfigWriteError(config.ConfigWritePathNotFound, "Unknown provider ID.")
			}
		}
		name, err := validateCustomGatewayPatch(custom, existing)
		if err != nil {
			return "", nil, err
		}
		providerID = target
		if providerID == "" {
			if !custom.BaseURL.Set || custom.BaseURL.Value == nil {
				return "", nil, providerConfigInvalid("A base URL is required to create a custom provider.")
			}
			providerID = catalog.allocateCustomProviderID(name)
		}
		if custom.BaseURL.Set {
			fields = append(fields, modelProviderWriteField{"base_url", custom.BaseURL.Value})
		}
		if custom.EnvKey.Set {
			fields = append(fields, modelProviderWriteField{"env_key", custom.EnvKey.Value})
		}
		if custom.Name != nil {
			fields = append(fields, modelProviderWriteField{"name", &name})
		}
	case "bedrock":
		bedrock := patch.Bedrock
		if bedrock == nil {
			return "", nil, providerConfigInvalid("Provider settings are required.")
		}
		providerID = bedrock.ProviderID
		if bedrock.Profile.Set {
			fields = append(fields, modelProviderWriteField{"aws.profile", bedrock.Profile.Value})
		}
		if bedrock.Region.Set {
			fields = append(fields, modelProviderWriteField{"aws.region", bedrock.Region.Value})
		}
	case "gcp":
		gcp := patch.Gcp
		if gcp == nil {
			return "", nil, providerConfigInvalid("Provider settings are required.")
		}
		providerID = gcp.ProviderID
		if gcp.ProjectID.Set {
			fields = append(fields, modelProviderWriteField{"project_id", gcp.ProjectID.Value})
		}
		if gcp.Location.Set {
			fields = append(fields, modelProviderWriteField{"location", gcp.Location.Value})
		}
		if gcp.CredentialFile.Set {
			fields = append(fields, modelProviderWriteField{"credential_file", gcp.CredentialFile.Value})
			if gcp.CredentialFile.Value != nil && !filepath.IsAbs(strings.TrimSpace(*gcp.CredentialFile.Value)) {
				return "", nil, providerConfigInvalid("model_providers.gcp: provider credential_file must be an absolute path")
			}
		}
	default:
		return "", nil, providerConfigInvalid("Provider settings are required.")
	}
	for _, field := range fields {
		if field.value != nil && strings.TrimSpace(*field.value) == "" {
			return "", nil, providerConfigInvalid("Configuration values must not be empty; use null to clear a field.")
		}
	}

	existing := catalog.tableFor(providerID)
	_, isBuiltin := catalog.builtins[providerID]
	supported := catalog.supportedFields(providerID)
	for _, field := range fields {
		if !modelProviderFieldSupported(supported, field.path) {
			return "", nil, providerConfigInvalid("A supplied field is not editable for this provider.")
		}
		if !catalog.fieldWritable(providerID, field.path) {
			return "", nil, providerConfigWriteError(config.ConfigWriteLayerReadonly, "This configuration field is read-only.")
		}
	}
	// Custom override tables require a name, but clearing an absent leaf needs
	// no table.
	userProvider := catalog.userProviderTable(providerID)
	if !isBuiltin && len(fields) > 0 && !modelProviderFieldsContain(fields, "name") {
		needsName := userProvider != nil
		for _, field := range fields {
			if field.value != nil {
				needsName = true
				break
			}
		}
		if needsName && providerStringField(userProvider, "name") == nil {
			if name := providerStringField(existing, "name"); name != nil && strings.TrimSpace(*name) != "" {
				value := strings.TrimSpace(*name)
				fields = append(fields, modelProviderWriteField{"name", &value})
			}
		}
	}
	if !isBuiltin && existing == nil {
		wireAPI := string(model.WireAPIResponses)
		fields = append(fields, modelProviderWriteField{"wire_api", &wireAPI})
	}

	escapedID := strings.ReplaceAll(strings.ReplaceAll(providerID, "\\", "\\\\"), "\"", "\\\"")
	edits := make([]config.ConfigEdit, 0, len(fields))
	for _, field := range fields {
		var value any
		if field.value != nil {
			value = *field.value
		}
		edits = append(edits, config.ConfigEdit{
			KeyPath:       "model_providers.\"" + escapedID + "\"." + field.path,
			Value:         value,
			MergeStrategy: config.MergeReplace,
		})
	}
	return providerID, edits, nil
}

// validateCustomGatewayPatch mirrors Rust's validate_custom_patch.
func validateCustomGatewayPatch(patch *model.CustomGatewaySettingsParams, existing map[string]any) (string, error) {
	if patch.BaseURL.Set && patch.BaseURL.Value != nil {
		if safe, _ := parseProviderEndpoint(strings.TrimSpace(*patch.BaseURL.Value)); !safe {
			return "", providerConfigInvalid("Enter an HTTP(S) base URL without credentials, a query, or a fragment.")
		}
	}
	if patch.EnvKey.Set && patch.EnvKey.Value != nil {
		if !validProviderEnvKey(strings.TrimSpace(*patch.EnvKey.Value)) {
			return "", providerConfigInvalid("Invalid API key environment variable name.")
		}
	}
	existingName := providerStringField(existing, "name")
	if patch.EnvKey.Set && existing != nil {
		usesBuiltinAuth := false
		switch {
		case existingName != nil:
			usesBuiltinAuth = usesBuiltinProviderAuth(*existingName)
		case patch.ProviderID != nil:
			usesBuiltinAuth = usesBuiltinProviderAuth(strings.TrimSpace(*patch.ProviderID))
		}
		if hasExternalProviderAuth(existing) || usesBuiltinAuth {
			return "", providerConfigInvalid("Authentication is configured externally. Omit envKey to preserve it.")
		}
	}
	name := ""
	if patch.Name != nil {
		name = strings.TrimSpace(*patch.Name)
	} else if existingName != nil {
		name = strings.TrimSpace(*existingName)
	}
	if name == "" {
		return "", providerConfigInvalid("Enter a provider name.")
	}
	// Legacy names select built-in authentication even on a custom provider.
	if usesBuiltinProviderAuth(name) {
		return "", providerConfigInvalid("This name is reserved for a built-in provider. Enter a different custom provider name.")
	}
	return name, nil
}

func validProviderEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for index, char := range key {
		switch {
		case char == '_':
		case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// allocateCustomProviderID mirrors Rust's allocate_custom_id: slug the name and
// append a numeric suffix until the id is free.
func (c *modelProviderCatalog) allocateCustomProviderID(name string) string {
	var parts []string
	var current strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			current.WriteRune(char)
			continue
		}
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	stem := strings.ToLower(strings.Join(parts, "-"))
	if stem == "" {
		stem = "custom"
	}
	defaultProviderID := c.defaultProviderID()
	isTaken := func(id string) bool {
		if id == defaultProviderID {
			return true
		}
		if _, ok := c.builtins[id]; ok {
			return true
		}
		_, ok := c.configured[id]
		return ok
	}
	id := stem
	for suffix := 2; id == model.LegacyOllamaChatProviderID || isTaken(id); suffix++ {
		id = fmt.Sprintf("%s-%d", stem, suffix)
	}
	return id
}

func (c *modelProviderCatalog) userProviderTable(id string) map[string]any {
	if c == nil || c.userLayer == nil {
		return nil
	}
	values, _ := c.userLayer.Config.(map[string]any)
	providers, _ := values["model_providers"].(map[string]any)
	table, _ := providers[id].(map[string]any)
	return table
}

func modelProviderFieldSupported(supported []string, field string) bool {
	for _, candidate := range supported {
		if candidate == field {
			return true
		}
	}
	return false
}

func modelProviderFieldsContain(fields []modelProviderWriteField, path string) bool {
	for _, field := range fields {
		if field.path == path {
			return true
		}
	}
	return false
}

func providerConfigInvalid(message string) error {
	return providerConfigWriteError(config.ConfigWriteValidation, message)
}

func providerConfigWriteError(code config.ConfigWriteErrorCode, message string) error {
	return &config.ConfigWriteError{
		Code: code,
		Err:  fmt.Errorf("%w: %s", config.ErrInvalidConfigRequest, message),
	}
}

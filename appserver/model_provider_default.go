package appserver

import (
	"fmt"

	"codex_go/config"
	"codex_go/model"
)

// handleModelProviderDefaultSet mirrors Rust #61397: persist the default model
// provider for new threads and unscoped model lists.
func (r *RuntimeRouter) handleModelProviderDefaultSet(request *Request) (*config.ConfigWriteResponse, error) {
	var params model.ProviderDefaultSetParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.ModelProviderDefaultSet(params)
}

// ModelProviderDefaultSet exposes the same persistence path to embedded TUI
// clients.
func (r *RuntimeRouter) ModelProviderDefaultSet(params model.ProviderDefaultSetParams) (*config.ConfigWriteResponse, error) {
	catalog, err := loadModelProviderCatalog(r)
	if err != nil {
		return nil, err
	}
	if catalog.managed != nil && catalog.managed.ModelProvider != nil {
		return nil, providerConfigWriteError(config.ConfigWriteRequirementReadonly, "`model_provider` is managed by requirements and cannot be changed")
	}
	providerID := params.ProviderID
	if _, ok := catalog.builtins[providerID]; !ok {
		if _, configured := catalog.configured[providerID]; !configured {
			return nil, providerConfigWriteError(config.ConfigWriteValidation, fmt.Sprintf("Model provider `%s` is not configured.", providerID))
		}
	}
	writeParams := &config.ConfigBatchWriteParams{Edits: []config.ConfigEdit{{
		KeyPath:       "model_provider",
		Value:         providerID,
		MergeStrategy: config.MergeReplace,
	}}}
	response, err := r.requireConfig().BatchWrite(writeParams)
	if err != nil {
		return nil, err
	}
	r.applyConfigBatchWriteMutation(writeParams)
	if response != nil && response.Status == config.WriteOK {
		// The active provider catalog changed, so model/list must reload on its
		// next access (Rust config_processor::model_provider_default_set).
		r.invalidateModelCatalog()
	}
	return response, nil
}

package app

import (
	"context"
	"io"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	modelpkg "codex_go/model"
	"codex_go/tui/onboarding"
)

// maybeRunGovCloudGuidance mirrors Rust #52176: already-configured GovCloud
// providers see the blocking guidance at startup, and acknowledgment is
// persisted to `notice.hide_gov_cloud_guidance`.
func maybeRunGovCloudGuidance(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) (bool, error) {
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil || loaded.HideGovCloudGuidance() {
		return false, nil
	}
	providerID := strings.ToLower(strings.TrimSpace(interactiveStringFromConfig(loaded.Values, "model_provider")))
	isBedrock := providerID == strings.ToLower(modelpkg.AmazonBedrockProviderID) ||
		strings.Contains(providerID, "amazon-bedrock")
	endpoint, endpointErr := resolveInteractiveRemoteEndpoint(root)
	if endpointErr != nil {
		return false, nil
	}
	if !isBedrock && endpoint == nil {
		return false, nil
	}
	govCloud := false
	if endpoint != nil {
		govCloud, _ = remoteGovCloudRequirements(ctx, endpoint)
	} else {
		_, _, _, _, checkGovCloud := interactiveBedrockCallbacks(codexHome)
		govCloud, _ = checkGovCloud(ctx)
	}
	if !govCloud {
		return false, nil
	}
	result, err := onboarding.RunGovCloudGuidance(ctx, stdin, stdout, interactiveNoAltScreenFromValues(root, loaded.Values))
	if err != nil {
		return false, err
	}
	if result.Cancelled {
		return true, nil
	}
	service := config.NewConfigService(codexHome)
	_, _ = service.BatchWrite(&config.ConfigBatchWriteParams{Edits: []config.ConfigEdit{{
		KeyPath:       "notice.hide_gov_cloud_guidance",
		Value:         true,
		MergeStrategy: config.MergeReplace,
	}}})
	return false, nil
}

func remoteGovCloudRequirements(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (bool, error) {
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return false, err
	}
	defer client.close()
	var response appserver.BedrockCheckGovCloudRequirementsResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodBedrockCheckGovCloudRequirements,
		appserver.BedrockCheckGovCloudRequirementsParams{}, &response); err != nil {
		return false, err
	}
	return response.IsGovCloud, nil
}

package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/sandbox"
	"codex_go/tool"
	"codex_go/turn"
	"codex_go/utils"
)

func (r *RuntimeRouter) executorSkillSandboxContextsForTurn(cfg *config.Config, cwd string, params *turn.TurnStartParams) (map[string]*execserverclient.FileSystemSandboxContext, error) {
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		return nil, err
	}
	if resolution == nil || resolution.Profile == nil || !executorPermissionProfileRequiresSandboxedReads(resolution.ProfileJSON, resolution.Profile) {
		return nil, nil
	}
	profileJSON := strings.TrimSpace(resolution.ProfileJSON)
	if profileJSON == "" {
		profileJSON, err = sandbox.RuntimePermissionProfileJSON(*resolution.Profile)
		if err != nil {
			return nil, err
		}
	}
	windowsLevel := sandbox.WindowsSandboxLevel(windowsSandboxLevelFromConfigValues(configValues(cfg)))
	privateDesktop := windowsSandboxPrivateDesktopFromConfigValues(configValues(cfg))
	useLegacyLandlock := cfg != nil && features.Enabled(cfg.FeatureSettings(), "use_legacy_landlock")

	contexts := map[string]*execserverclient.FileSystemSandboxContext{}
	add := func(environmentID string, environmentCWD string, workspaceRoots []string) error {
		environmentID = strings.TrimSpace(environmentID)
		environmentCWD = executorEnvironmentNativePath(environmentCWD)
		if environmentID == "" || environmentCWD == "" {
			return nil
		}
		if len(workspaceRoots) == 0 {
			workspaceRoots = []string{environmentCWD}
		}
		for i := range workspaceRoots {
			workspaceRoots[i] = executorEnvironmentNativePath(workspaceRoots[i])
		}
		context, contextErr := tool.NewFileSystemSandboxContext(tool.FileSystemSandboxContextOptions{
			PermissionProfile:            resolution.Profile,
			PermissionProfileJSON:        profileJSON,
			CWD:                          environmentCWD,
			WorkspaceRoots:               workspaceRoots,
			WindowsSandboxLevel:          windowsLevel,
			WindowsSandboxPrivateDesktop: privateDesktop,
			UseLegacyLandlock:            useLegacyLandlock,
		})
		if contextErr != nil {
			return contextErr
		}
		contexts[environmentID] = context
		return nil
	}

	localCWD := firstNonEmpty(primaryTurnEnvironmentCWD(params, cwd), cwd, r.services.DefaultCWD)
	localRoots := []string(nil)
	if params != nil {
		localRoots = append(localRoots, params.RuntimeWorkspaceRoots...)
	}
	if err := add("local", localCWD, localRoots); err != nil {
		return nil, err
	}
	if r != nil && r.services.Environment != nil {
		for _, record := range r.services.Environment.List() {
			recordCWD := ""
			if record.CWD != nil {
				recordCWD = *record.CWD
			}
			if err := add(record.EnvironmentID, recordCWD, nil); err != nil {
				return nil, err
			}
		}
	}
	if params != nil {
		for _, environment := range params.Environments {
			environmentID := firstNonEmpty(
				threadItemStringFromAnyMap(environment, "environmentId"),
				threadItemStringFromAnyMap(environment, "environment_id"),
			)
			environmentCWD := firstNonEmpty(
				threadItemStringFromAnyMap(environment, "cwd"),
				threadItemStringFromAnyMap(environment, "CWD"),
			)
			workspaceRoots := stringSliceFromAny(firstNonNil(environment["workspaceRoots"], environment["workspace_roots"]))
			if err := add(environmentID, environmentCWD, workspaceRoots); err != nil {
				return nil, err
			}
		}
	}
	return contexts, nil
}

func executorPermissionProfileRequiresSandboxedReads(raw string, profile *sandbox.PermissionProfile) bool {
	if profile == nil || profile.Disabled {
		return false
	}
	var wire struct {
		Type       string `json:"type"`
		FileSystem struct {
			Type    string `json:"type"`
			Entries []struct {
				Access string `json:"access"`
				Path   struct {
					Type  string `json:"type"`
					Value struct {
						Kind string `json:"kind"`
					} `json:"value"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if json.Unmarshal([]byte(raw), &wire) != nil {
		return profile.HasDenyReadEntries()
	}
	if wire.Type == "disabled" || wire.FileSystem.Type == "unrestricted" {
		return false
	}
	fullRootRead := false
	for _, entry := range wire.FileSystem.Entries {
		if strings.EqualFold(entry.Access, string(sandbox.FileSystemAccessDeny)) {
			return true
		}
		if entry.Path.Type == "special" && entry.Path.Value.Kind == "root" && (strings.EqualFold(entry.Access, string(sandbox.FileSystemAccessRead)) || strings.EqualFold(entry.Access, string(sandbox.FileSystemAccessWrite))) {
			fullRootRead = true
		}
	}
	return wire.FileSystem.Type == "restricted" && !fullRootRead
}

func executorEnvironmentNativePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if path, err := utils.Parse(value); err == nil {
		return path.NativePathString()
	}
	return value
}

func executorSkillSandboxContextKey(context *execserverclient.FileSystemSandboxContext) string {
	if context == nil {
		return ""
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		return "invalid"
	}
	return string(encoded)
}

func executorSkillSandboxContextForEnvironment(contexts map[string]*execserverclient.FileSystemSandboxContext, environmentID string) (*execserverclient.FileSystemSandboxContext, error) {
	if contexts == nil {
		return nil, nil
	}
	context := contexts[strings.TrimSpace(environmentID)]
	if context == nil {
		return nil, errors.New("failed to read skill resource")
	}
	return context, nil
}

func validateExecutorSkillSandboxAvailability(context *execserverclient.FileSystemSandboxContext, path string) error {
	if context == nil || !executorPathUsesWindowsConvention(path) {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(context.WindowsSandboxLevel), string(sandbox.WindowsSandboxDisabled)) {
		return errors.New("executor skill resource requires an unavailable filesystem sandbox")
	}
	return nil
}

func executorPathUsesWindowsConvention(value string) bool {
	if path, err := utils.Parse(strings.TrimSpace(value)); err == nil {
		convention, ok := path.InferConvention()
		return ok && convention == utils.ConventionWindows
	}
	legacy := utils.NewLegacyAppPathString(value)
	convention, ok := legacy.InferAbsolutePathConvention()
	return ok && convention == utils.ConventionWindows
}

func requireExecutorSkillSandboxContext(contexts map[string]*execserverclient.FileSystemSandboxContext, environmentID string, path string) (*execserverclient.FileSystemSandboxContext, error) {
	context, err := executorSkillSandboxContextForEnvironment(contexts, environmentID)
	if err != nil {
		return nil, err
	}
	if err := validateExecutorSkillSandboxAvailability(context, path); err != nil {
		return nil, fmt.Errorf("failed to read executor skill resource: %w", err)
	}
	return context, nil
}

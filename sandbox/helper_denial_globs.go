package sandbox

import (
	"encoding/json"
	"fmt"

	"codex_go/utils"
)

// BindWindowsHelperDenialGlobs anchors relative deny globs before a filesystem
// helper changes its working directory. Home-relative globs retain their meaning.
func BindWindowsHelperDenialGlobs(profile *PermissionProfile, policyCWD string) (*PermissionProfile, error) {
	raw, err := RuntimePermissionProfileJSON(*profile)
	if err != nil {
		return nil, err
	}
	var wire rustPermissionProfileWire
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, err
	}
	if wire.FileSys != nil {
		for i := range wire.FileSys.Entries {
			entry := &wire.FileSys.Entries[i]
			if entry.Access != string(FileSystemAccessDeny) || entry.Path.Type != "glob_pattern" {
				continue
			}
			pattern := entry.Path.Pattern
			if _, home := utils.ConventionWindows.HomeRelativeSuffix(pattern); home {
				continue
			}
			legacy := utils.NewLegacyAppPathString(pattern)
			if _, err := legacy.ToPathURI(utils.ConventionWindows); err == nil {
				continue
			}
			cwd, ok := utils.InferredPathURI(policyCWD)
			if !ok {
				return nil, fmt.Errorf("invalid helper policy cwd: %s", policyCWD)
			}
			if err := cwd.ValidateGlobDirectory(utils.ConventionWindows); err != nil {
				return nil, err
			}
			joined, err := cwd.Join(pattern)
			if err != nil {
				return nil, err
			}
			native, err := utils.LegacyAppPathStringFromURI(joined, utils.ConventionWindows)
			if err != nil {
				return nil, err
			}
			entry.Path.Pattern = native.Value
		}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	return ParseRuntimePermissionProfileJSON(string(encoded))
}

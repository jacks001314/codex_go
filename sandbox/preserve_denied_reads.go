package sandbox

import (
	"encoding/json"
	"reflect"
)

// PreserveDeniedReads retains the current filesystem denials when replacing
// a managed permission profile, including the glob expansion bound (#61134).
func PreserveDeniedReads(requested PermissionProfile, current PermissionProfile) (*PermissionProfile, error) {
	raw, err := RuntimePermissionProfileJSON(requested)
	if err != nil {
		return nil, err
	}
	previous, err := RuntimePermissionProfileJSON(current)
	if err != nil {
		return nil, err
	}
	var next, old rustPermissionProfileWire
	if err := json.Unmarshal([]byte(raw), &next); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(previous), &old); err != nil {
		return nil, err
	}
	hasDenials := old.FileSys != nil
	if hasDenials {
		hasDenials = false
		for _, entry := range old.FileSys.Entries {
			if entry.Access == string(FileSystemAccessDeny) {
				hasDenials = true
				break
			}
		}
	}
	if next.Type == "disabled" && hasDenials {
		// Rust converts a disabled profile's unrestricted filesystem policy to
		// a managed restricted policy when existing deny-read entries must be
		// retained. Full access remains enabled because a disabled profile has
		// no separate network restriction.
		next.Type = "managed"
		next.Network = string(NetworkEnabled)
		next.FileSys = &rustPermissionFilesystem{Type: "restricted"}
	}
	if next.FileSys == nil || old.FileSys == nil {
		return ParseRuntimePermissionProfileJSON(raw)
	}
	if next.FileSys.Type == "unrestricted" && hasDenials {
		next.FileSys = &rustPermissionFilesystem{Type: "restricted", Entries: []rustPermissionFilesystemEntry{{Path: rustPermissionFilesystemPath{Type: "special", Value: rustPermissionFilesystemSpecial{Kind: "root"}}, Access: string(FileSystemAccessWrite)}}}
	}
	if next.FileSys.Type == "restricted" {
		if next.FileSys.GlobScanMaxDepth == nil {
			next.FileSys.GlobScanMaxDepth = old.FileSys.GlobScanMaxDepth
		}
		for _, entry := range old.FileSys.Entries {
			if entry.Access != string(FileSystemAccessDeny) {
				continue
			}
			found := false
			for _, existing := range next.FileSys.Entries {
				if reflect.DeepEqual(existing, entry) {
					found = true
					break
				}
			}
			if !found {
				next.FileSys.Entries = append(next.FileSys.Entries, entry)
			}
		}
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	return ParseRuntimePermissionProfileJSON(string(encoded))
}

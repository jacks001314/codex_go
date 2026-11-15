package sandbox

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// RenderFileSystemContext ports Rust's FileSystemContext::render
// (core/src/context/environment_context.rs): the environment's workspace roots
// followed by the materialized permission profile. It is used by the
// per_environment_context world-state format to describe each selected
// environment's own filesystem rules.
func RenderFileSystemContext(profile *PermissionProfile, policyCWD string, workspaceRoots []string) (string, error) {
	if profile == nil {
		readOnly := ReadOnlyPermissionProfile()
		profile = &readOnly
	}
	// Rust materializes project roots with the environment's workspace roots
	// before rendering. Keep the original profile when materialization cannot
	// resolve a foreign path so the section still describes the rules.
	if materialized, err := MaterializePermissionProfileWorkspaceRoots(profile, policyCWD, workspaceRoots); err == nil && materialized != nil {
		profile = materialized
	}
	raw, err := RuntimePermissionProfileJSON(*profile)
	if err != nil {
		return "", err
	}
	wire, err := parseRustPermissionProfileWireJSON([]byte(raw))
	if err != nil {
		return "", err
	}

	var rendered strings.Builder
	rendered.WriteString("<filesystem>")
	roots := make([]string, 0, len(workspaceRoots))
	for _, root := range workspaceRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) > 0 {
		rendered.WriteString("<workspace_roots>")
		for _, root := range roots {
			pushXMLTextElement(&rendered, "root", root)
		}
		rendered.WriteString("</workspace_roots>")
	}
	switch strings.ToLower(strings.TrimSpace(wire.Type)) {
	case "disabled":
		rendered.WriteString(`<permission_profile type="disabled"><file_system type="unrestricted" /></permission_profile>`)
	case "external":
		rendered.WriteString(`<permission_profile type="external"><file_system type="external" /></permission_profile>`)
	case "managed":
		rendered.WriteString(`<permission_profile type="managed">`)
		if err := renderManagedFileSystem(&rendered, wire.FileSys); err != nil {
			return "", err
		}
		rendered.WriteString(`</permission_profile>`)
	default:
		return "", fmt.Errorf("unsupported permission profile type %q", wire.Type)
	}
	rendered.WriteString("</filesystem>")
	return rendered.String(), nil
}

func renderManagedFileSystem(rendered *strings.Builder, fileSystem *rustPermissionFilesystem) error {
	if fileSystem == nil || strings.EqualFold(strings.TrimSpace(fileSystem.Type), "unrestricted") {
		rendered.WriteString(`<file_system type="unrestricted" />`)
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(fileSystem.Type), "restricted") {
		return fmt.Errorf("unsupported file system type %q", fileSystem.Type)
	}
	entries := dedupeFileSystemEntries(fileSystem.Entries)
	if len(entries) == 0 && fileSystem.GlobScanMaxDepth == nil {
		rendered.WriteString(`<file_system type="restricted" />`)
		return nil
	}
	rendered.WriteString(`<file_system type="restricted"`)
	if fileSystem.GlobScanMaxDepth != nil {
		fmt.Fprintf(rendered, ` glob_scan_max_depth="%d"`, *fileSystem.GlobScanMaxDepth)
	}
	rendered.WriteString(`>`)
	for _, entry := range entries {
		renderFileSystemEntry(rendered, entry)
	}
	rendered.WriteString(`</file_system>`)
	return nil
}

func renderFileSystemEntry(rendered *strings.Builder, entry rustPermissionFilesystemEntry) {
	rendered.WriteString(`<entry access="`)
	rendered.WriteString(entry.Access)
	if strings.EqualFold(entry.Access, string(FileSystemAccessDeny)) {
		rendered.WriteString(`" escalatable="false`)
	}
	rendered.WriteString(`">`)
	switch entry.Path.Type {
	case "glob_pattern":
		pushXMLTextElement(rendered, "glob", entry.Path.Pattern)
	case "special":
		pushXMLTextElement(rendered, "special", renderSpecialFileSystemPath(entry.Path.Value))
	default:
		pushXMLTextElement(rendered, "path", entry.Path.Path)
	}
	rendered.WriteString(`</entry>`)
}

func renderSpecialFileSystemPath(value rustPermissionFilesystemSpecial) string {
	var base string
	switch strings.ToLower(strings.TrimSpace(value.Kind)) {
	case "root":
		base = ":root"
	case "minimal":
		base = ":minimal"
	case "project_roots", "current_working_directory":
		base = ":workspace_roots"
	case "tmpdir":
		base = ":tmpdir"
	case "slash_tmp":
		base = ":slash_tmp"
	default:
		base = value.Kind
	}
	if value.Subpath != nil && *value.Subpath != "" {
		return base + "/" + *value.Subpath
	}
	return base
}

func dedupeFileSystemEntries(entries []rustPermissionFilesystemEntry) []rustPermissionFilesystemEntry {
	seen := make(map[string]struct{}, len(entries))
	out := make([]rustPermissionFilesystemEntry, 0, len(entries))
	for _, entry := range entries {
		subpath := ""
		if entry.Path.Value.Subpath != nil {
			subpath = *entry.Path.Value.Subpath
		}
		key := strings.Join([]string{
			entry.Access,
			entry.Path.Type,
			entry.Path.Path,
			entry.Path.Pattern,
			entry.Path.Value.Kind,
			subpath,
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	return out
}

func pushXMLTextElement(rendered *strings.Builder, name string, value string) {
	rendered.WriteByte('<')
	rendered.WriteString(name)
	rendered.WriteByte('>')
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(value)); err == nil {
		rendered.WriteString(escaped.String())
	} else {
		rendered.WriteString(value)
	}
	rendered.WriteString("</")
	rendered.WriteString(name)
	rendered.WriteByte('>')
}

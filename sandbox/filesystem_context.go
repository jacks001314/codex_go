package sandbox

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
)

// FileSystemContextFormat selects the model-visible filesystem rendering.
type FileSystemContextFormat int

const (
	// FileSystemContextFull renders every filesystem entry.
	FileSystemContextFull FileSystemContextFormat = iota
	// FileSystemContextCompact summarizes repeated protected metadata reads.
	FileSystemContextCompact
)

// RenderFileSystemContext ports Rust's FileSystemContext::render
// (core/src/context/environment_context.rs): the environment's workspace roots
// followed by the materialized permission profile. It is used by the
// per_environment_context world-state format to describe each selected
// environment's own filesystem rules.
func RenderFileSystemContext(profile *PermissionProfile, policyCWD string, workspaceRoots []string) (string, error) {
	return RenderFileSystemContextWithFormat(profile, policyCWD, workspaceRoots, FileSystemContextFull)
}

// RenderFileSystemContextWithFormat mirrors FileSystemContext::render for the
// full and compact formats introduced by Rust #64164.
func RenderFileSystemContextWithFormat(profile *PermissionProfile, policyCWD string, workspaceRoots []string, format FileSystemContextFormat) (string, error) {
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

	roots := make([]string, 0, len(workspaceRoots))
	for _, root := range workspaceRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	if format == FileSystemContextCompact &&
		strings.EqualFold(strings.TrimSpace(wire.Type), "managed") &&
		wire.FileSys != nil &&
		strings.EqualFold(strings.TrimSpace(wire.FileSys.Type), "restricted") &&
		wire.FileSys.GlobScanMaxDepth == nil {
		if compact, ok := renderCompactFileSystemContext(roots, dedupeFileSystemEntries(wire.FileSys.Entries)); ok {
			return compact, nil
		}
	}

	var rendered strings.Builder
	rendered.WriteString("<filesystem>")
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

var compactFileSystemMetadataNames = []string{".git", ".agents", ".codex", ".gcode", ".aws"}

// renderCompactFileSystemContext mirrors Rust #64164. It only summarizes a
// profile when every literal write path has exactly the same generated
// protected-metadata reads, so any explicit rule keeps the full listing.
func renderCompactFileSystemContext(workspaceRoots []string, entries []rustPermissionFilesystemEntry) (string, bool) {
	if len(workspaceRoots) == 0 || len(entries) < 2*(1+len(compactFileSystemMetadataNames)) {
		return "", false
	}
	readRoot := false
	tempGrants := []string{}
	writes := []string{}
	reads := map[string]struct{}{}
	for _, entry := range entries {
		access := strings.ToLower(strings.TrimSpace(entry.Access))
		switch entry.Path.Type {
		case "", "path":
			switch access {
			case strings.ToLower(string(FileSystemAccessWrite)):
				writes = append(writes, entry.Path.Path)
			case strings.ToLower(string(FileSystemAccessRead)):
				if _, duplicate := reads[entry.Path.Path]; duplicate {
					return "", false
				}
				reads[entry.Path.Path] = struct{}{}
			default:
				return "", false
			}
		case "special":
			kind := strings.ToLower(strings.TrimSpace(entry.Path.Value.Kind))
			switch {
			case access == strings.ToLower(string(FileSystemAccessRead)) && kind == "root":
				readRoot = true
			case access == strings.ToLower(string(FileSystemAccessWrite)) && (kind == "slash_tmp" || kind == "tmpdir"):
				tempGrants = append(tempGrants, renderSpecialFileSystemPath(entry.Path.Value))
			default:
				return "", false
			}
		default:
			return "", false
		}
	}
	if !readRoot || len(writes) < 2 || len(reads) != len(writes)*len(compactFileSystemMetadataNames) {
		return "", false
	}
	for _, write := range writes {
		if _, writableRead := reads[write]; writableRead {
			return "", false
		}
	}

	remainingRoots := make(map[string]struct{}, len(workspaceRoots))
	for _, root := range workspaceRoots {
		remainingRoots[root] = struct{}{}
	}
	otherWrites := []string{}
	for _, write := range writes {
		for _, name := range compactFileSystemMetadataNames {
			child := filepath.Join(write, name)
			if _, ok := reads[child]; !ok {
				return "", false
			}
			delete(reads, child)
		}
		if _, workspaceRoot := remainingRoots[write]; workspaceRoot {
			delete(remainingRoots, write)
		} else {
			otherWrites = append(otherWrites, write)
		}
	}
	if len(remainingRoots) != 0 || len(reads) != 0 {
		return "", false
	}

	var rendered strings.Builder
	rendered.WriteString("<filesystem>\nManaged, restricted. Read access to :root.")
	if len(workspaceRoots) > 0 {
		fmt.Fprintf(&rendered, "\nWritable workspace roots: %s", jsonStringsForXML(workspaceRoots))
	}
	if len(otherWrites) > 0 {
		fmt.Fprintf(&rendered, "\nOther writable paths: %s", jsonStringsForXML(otherWrites))
	}
	last := len(compactFileSystemMetadataNames) - 1
	fmt.Fprintf(&rendered,
		"\nDirectly under each listed path, %s and %s have read-only entries.",
		strings.Join(compactFileSystemMetadataNames[:last], ", "),
		compactFileSystemMetadataNames[last],
	)
	if len(tempGrants) > 0 {
		fmt.Fprintf(&rendered, "\nSeparate write grants: %s", strings.Join(tempGrants, ", "))
	}
	rendered.WriteString("\n</filesystem>")
	return rendered.String(), true
}

func jsonStringsForXML(values []string) string {
	encoded, _ := json.Marshal(values)
	rendered := strings.ReplaceAll(string(encoded), `\u0026`, "&")
	rendered = strings.ReplaceAll(rendered, `\u003c`, "<")
	rendered = strings.ReplaceAll(rendered, `\u003e`, ">")
	rendered = strings.ReplaceAll(rendered, "&", "&amp;")
	rendered = strings.ReplaceAll(rendered, "<", "&lt;")
	rendered = strings.ReplaceAll(rendered, ">", "&gt;")
	return rendered
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

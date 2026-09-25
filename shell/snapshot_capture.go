package shell

// Shell-snapshot capture: the script a shell runs to dump its state, the decode
// of that stream, and the replay text a snapshot file holds.
//
// Rust parity: codex-rs/shell-command/src/shell_snapshot_capture.rs,
// shell_snapshot_exports.rs and shell_snapshot_render.rs. The records are
// NUL-delimited, and function/alias/option text stays native shell source; only
// the credential-broker stage decodes export values, so Go keeps declaration
// text verbatim.

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// SnapshotStartup selects whether a capture seeds the user's interactive
// configuration, mirroring Rust's SnapshotStartup.
type SnapshotStartup int

const (
	// SnapshotStartupInteractive replicates a login shell's startup files.
	SnapshotStartupInteractive SnapshotStartup = iota
	// SnapshotStartupNonInteractive captures without the user's startup files.
	SnapshotStartupNonInteractive
)

// SnapshotCaptureOptions selects what a capture run records. Shell state and
// aliases are always captured; declarations add the native export records, and
// the environment record is only needed by the credential-broker stage.
type SnapshotCaptureOptions struct {
	Startup      SnapshotStartup
	Declarations bool
	Environment  bool
}

// SnapshotCaptureScript returns the script whose standard output is the capture
// stream for shellType, or false when the shell has no snapshot support
// (Rust's snapshot_capture_script).
func SnapshotCaptureScript(shellType ShellType, options SnapshotCaptureOptions) (string, bool) {
	return snapshotCaptureScript(shellType, options, false)
}

// SnapshotSourceCaptureScript returns the incremental variant a replay sources:
// the options and aliases are emitted inside a single brace group, so an alias
// or quoting option restored from the snapshot cannot reinterpret the records
// that follow it (Rust's snapshot_source_capture_script, #48078). POSIX sh has
// no `source`, so it keeps the evaluating form.
func SnapshotSourceCaptureScript(shellType ShellType, options SnapshotCaptureOptions) (string, bool) {
	return snapshotCaptureScript(shellType, options, shellType != ShellSh)
}

// snapshotStartupEnvironment records the POSIX startup file the capture sourced
// and the shell environment from just before it did, so the credential stage can
// put both back (Rust's SNAPSHOT_STARTUP_ENVIRONMENT).
const snapshotStartupEnvironment = "printf '\\0CODEX_SNAPSHOT_POSIX_STARTUP\\0%s\\0' \"$__codex_env_file\"\n" +
	"    SNAPSHOT_ENVIRONMENT\n" +
	"    printf '\\0'"

// snapshotStartupMarker delimits that startup record in the capture stream.
const snapshotStartupMarker = "\x00CODEX_SNAPSHOT_POSIX_STARTUP\x00"

func snapshotCaptureScript(shellType ShellType, options SnapshotCaptureOptions, sourceReplay bool) (string, bool) {
	var script string
	switch shellType {
	case ShellZsh:
		script = snapshotStartupPrefix(shellType, options.Startup) + snapshotZshScript
	case ShellBash:
		script = snapshotStartupPrefix(shellType, options.Startup) + snapshotBashScript
	case ShellSh:
		script = snapshotStartupPrefix(shellType, options.Startup) + snapshotShScript
	default:
		return "", false
	}
	declarations := ""
	if options.Declarations {
		declarations = SnapshotExportsScript(shellType)
	}
	optionsBegin := ""
	aliasesEnd := ""
	if sourceReplay {
		optionsBegin = "printf '{\\n'"
		aliasesEnd = "printf \"case '' in '') ;; esac\\n}\\n\""
	}
	script = strings.ReplaceAll(script, "SNAPSHOT_OPTIONS_BEGIN", optionsBegin)
	script = strings.ReplaceAll(script, "SNAPSHOT_ALIASES_END", aliasesEnd)
	script = strings.ReplaceAll(script, "SNAPSHOT_EXPORTS", declarations)
	if options.Declarations {
		script = strings.ReplaceAll(script, "SNAPSHOT_DECLARATION_ENVIRONMENT", snapshotEnvironment)
	} else {
		script = strings.ReplaceAll(script, "SNAPSHOT_DECLARATION_ENVIRONMENT", "")
	}
	script = strings.ReplaceAll(script, "SNAPSHOT_COMMAND_HELPER", snapshotCommandHelper)
	if options.Declarations && options.Environment {
		script = strings.ReplaceAll(script, "SNAPSHOT_STARTUP_ENVIRONMENT", snapshotStartupEnvironment)
	} else {
		script = strings.ReplaceAll(script, "SNAPSHOT_STARTUP_ENVIRONMENT", "")
	}
	if options.Environment {
		script = strings.ReplaceAll(script, "SNAPSHOT_ENVIRONMENT", snapshotEnvironment)
	} else {
		script = strings.ReplaceAll(script, "SNAPSHOT_ENVIRONMENT", "")
	}
	return script, true
}

// snapshotStartupPrefix seeds the interactive configuration the capture must
// load before recording state, matching Rust's shell_startup_script use.
func snapshotStartupPrefix(shellType ShellType, startup SnapshotStartup) string {
	if startup != SnapshotStartupInteractive {
		return ""
	}
	switch shellType {
	case ShellZsh:
		return shellStartupZsh
	case ShellBash:
		return shellStartupBash
	case ShellSh:
		return posixEnvPathExpansionFunction + "\n" + snapshotShStartupScript
	default:
		return ""
	}
}

// SnapshotExportsScript returns the native export-declaration capture script for
// shellType (Rust's exports::script), or "" for a shell without one. Each
// declaration is written between two NUL records whose first record names the
// variable.
func SnapshotExportsScript(shellType ShellType) string {
	var script string
	switch shellType {
	case ShellBash:
		script = snapshotExportsBash
	case ShellZsh:
		script = snapshotExportsZsh
	case ShellSh:
		script = snapshotExportsSh
	default:
		return ""
	}
	script = strings.ReplaceAll(script, "RECORD_START", "printf '%s\\0' \"$__codex_snapshot_export_name\"")
	script = strings.ReplaceAll(script, "RECORD_END", "printf '\\0'")
	return script
}

// CapturedSnapshot is the decoded capture stream: native shell state and
// aliases, the export declarations, and the raw environment records.
type CapturedSnapshot struct {
	// StartupEnvironment is set for POSIX sh captures that sourced a startup
	// file; it carries that file and the environment from just before.
	StartupEnvironment *CapturedStartupEnvironment
	ShellType          ShellType
	// State is the native function/option text, from the snapshot banner on.
	State string
	// Aliases is the native alias text captured after the state.
	Aliases string
	// Exports are the native export declarations in capture order.
	Exports []CapturedExport
	// Environment is the NUL-delimited environment from the same shell.
	Environment []byte
}

// CapturedStartupEnvironment is a POSIX startup file and the environment from
// immediately before the capture sourced it.
type CapturedStartupEnvironment struct {
	Path        string
	Environment []byte
}

// CapturedExport is one native export declaration and the variable it binds.
//
// Rust additionally decodes zsh `-T` tied arrays here because its credential
// stage substitutes values inside them; Go's replay keeps the native
// declaration text, so the decode lands with that stage.
type CapturedExport struct {
	Source string
	Key    string
}

// ParseCapturedSnapshot decodes a capture stream, or returns nil when the
// stream is incomplete or the shell has no snapshot support (Rust's
// CapturedSnapshot::parse).
func ParseCapturedSnapshot(shellType ShellType, captured []byte) *CapturedSnapshot {
	if shellType != ShellBash && shellType != ShellZsh && shellType != ShellSh {
		return nil
	}
	reader := &snapshotRecordReader{data: captured}
	var startupEnvironment *CapturedStartupEnvironment
	if shellType == ShellSh {
		if start := bytes.Index(captured, []byte(snapshotStartupMarker)); start >= 0 {
			reader.offset = start + len(snapshotStartupMarker)
			path, ok := reader.take()
			if !ok {
				return nil
			}
			environmentStart := reader.offset
			for {
				record, ok := reader.take()
				if !ok {
					return nil
				}
				if len(record) == 0 {
					break
				}
			}
			// Rust keeps the environment region up to the record list's final
			// terminator, so each entry's own NUL survives and only the last
			// one is dropped; consumers split on NUL and drop empty pieces.
			environmentEnd := reader.offset - 1
			startupEnvironment = &CapturedStartupEnvironment{
				Path:        lossyUTF8(path),
				Environment: captured[environmentStart:environmentEnd],
			}
		}
	}
	// state NUL aliases NUL (name NUL declaration NUL)* NUL environment.
	state, ok := reader.take()
	if !ok {
		return nil
	}
	// Some shells print a banner before the snapshot; keep the snapshot itself.
	index := bytes.Index(state, []byte("# Snapshot file"))
	if index < 0 || !utf8.Valid(state[index:]) {
		return nil
	}
	aliases, ok := reader.take()
	if !ok || !utf8.Valid(aliases) {
		return nil
	}
	exports := []CapturedExport{}
	for {
		key, ok := reader.take()
		if !ok || !utf8.Valid(key) {
			return nil
		}
		if len(key) == 0 {
			if shellType == ShellSh {
				// POSIX sh captures declarations as environment records.
				declared, ok := parseShDeclarationRecords(reader)
				if !ok {
					return nil
				}
				exports = append(exports, declared...)
			}
			return &CapturedSnapshot{
				StartupEnvironment: startupEnvironment,
				ShellType:          shellType,
				State:              string(state[index:]),
				Aliases:            string(aliases),
				Exports:            exports,
				Environment:        reader.remaining(),
			}
		}
		source, ok := reader.take()
		if !ok {
			return nil
		}
		exports = append(exports, CapturedExport{Source: lossyUTF8(source), Key: string(key)})
	}
}

// parseShDeclarationRecords turns POSIX sh's environment records into export
// declarations, keeping only names a shell would accept and skipping the
// working-directory variables.
func parseShDeclarationRecords(reader *snapshotRecordReader) ([]CapturedExport, bool) {
	declared := []CapturedExport{}
	for {
		record, ok := reader.take()
		if !ok {
			return nil, false
		}
		if len(record) == 0 {
			return declared, true
		}
		equals := bytes.IndexByte(record, '=')
		if equals < 0 {
			return nil, false
		}
		key := string(record[:equals])
		if !isSnapshotExportName(key) {
			continue
		}
		quoted, ok := tryQuoteShellValue(lossyUTF8(record[equals+1:]))
		if !ok {
			return nil, false
		}
		declared = append(declared, CapturedExport{
			Source: "export " + key + "=" + quoted + "\n",
			Key:    key,
		})
	}
}

// RenderState renders the native shell state a replay restores, without the
// export declarations (Rust's CapturedSnapshot::render_state).
func (s *CapturedSnapshot) RenderState() string {
	if s == nil {
		return ""
	}
	return s.State + s.Aliases
}

// RenderScript renders the complete replay script: shell state, then the
// export declarations (Rust's CapturedSnapshot::render_script).
func (s *CapturedSnapshot) RenderScript() string {
	if s == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(s.RenderState())
	builder.WriteString("# exports (native declarations)\n")
	for _, export := range s.Exports {
		builder.WriteString(export.Source)
	}
	return builder.String()
}

// snapshotRecordReader walks the NUL-delimited records of a capture stream.
type snapshotRecordReader struct {
	data   []byte
	offset int
}

func (r *snapshotRecordReader) take() ([]byte, bool) {
	if r == nil || r.offset > len(r.data) {
		return nil, false
	}
	index := bytes.IndexByte(r.data[r.offset:], 0)
	if index < 0 {
		return nil, false
	}
	record := r.data[r.offset : r.offset+index]
	r.offset += index + 1
	return record, true
}

func (r *snapshotRecordReader) remaining() []byte {
	if r == nil || r.offset > len(r.data) {
		return nil
	}
	return r.data[r.offset:]
}

// isSnapshotExportName reports whether a captured environment record names a
// variable a shell can export again (Rust's identifier filter, PWD and OLDPWD
// excluded).
func isSnapshotExportName(name string) bool {
	if name == "" || name == "PWD" || name == "OLDPWD" {
		return false
	}
	for index, character := range []byte(name) {
		switch {
		case character == '_':
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case index > 0 && character >= '0' && character <= '9':
		default:
			return false
		}
	}
	return true
}

// tryQuoteShellValue quotes a captured value the way Rust's shlex::try_quote
// does: values made of safe characters are left alone, everything else is
// single-quoted with embedded quotes escaped.
func tryQuoteShellValue(value string) (string, bool) {
	if strings.ContainsRune(value, 0) {
		return "", false
	}
	if value == "" {
		return "''", true
	}
	if strings.IndexFunc(value, func(character rune) bool { return !isShellSafeCharacter(character) }) < 0 {
		return value, true
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'", true
}

// isShellSafeCharacter reports whether shlex leaves the character unquoted.
func isShellSafeCharacter(character rune) bool {
	switch {
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	case character >= '0' && character <= '9':
		return true
	}
	switch character {
	case '@', '%', '+', '=', ':', ',', '.', '/', '-', '_':
		return true
	default:
		return false
	}
}

// lossyUTF8 mirrors String::from_utf8_lossy: every invalid byte becomes the
// replacement character instead of dropping data.
func lossyUTF8(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	var builder strings.Builder
	builder.Grow(len(raw))
	for len(raw) > 0 {
		character, size := utf8.DecodeRune(raw)
		if character == utf8.RuneError && size <= 1 {
			builder.WriteRune(utf8.RuneError)
			raw = raw[1:]
			continue
		}
		builder.Write(raw[:size])
		raw = raw[size:]
	}
	return builder.String()
}

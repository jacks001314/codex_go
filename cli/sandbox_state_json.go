package cli

import (
	"net/url"
	"runtime"
	"strconv"
	"strings"
)

// `codex sandbox --sandbox-state-json` carries codex_mcp::SandboxState, which
// Rust decodes with serde_json before it parses `-c` overrides, loads config or
// looks at the command. The struct is
//
//	#[serde(rename_all = "camelCase")]
//	struct SandboxState {
//	    permission_profile: PermissionProfile,
//	    codex_linux_sandbox_exe: Option<PathBuf>,
//	    sandbox_cwd: PathUri,
//	    #[serde(default)] use_legacy_landlock: bool,
//	    #[serde(default)] use_mxc: bool,
//	}
//
// so `permissionProfile` and `sandboxCwd` are required, the launcher path is an
// optional string, the two booleans default to false, and unknown fields are
// ignored. `sandboxCwd` is a PathUri - a `file:` URI rather than a native path,
// because the launcher forwards it to a child that may run on another host.
//
// serde reports syntax and type errors with the byte it stopped at, so this
// decoder reuses the scanner behind --startup-client-info and reproduces the
// diagnostics the struct can produce: syntax, invalid type, duplicate field,
// missing field and the untagged PermissionProfile enum.

const sandboxStateOption = "--sandbox-state-json"

// SandboxStateJSONError is serde's `invalid --sandbox-state-json value: ...`
// diagnostic.
type SandboxStateJSONError struct {
	Detail string
}

func (e *SandboxStateJSONError) Error() string {
	return "invalid " + sandboxStateOption + " value: " + e.Detail
}

// SandboxStateCWDHostError is the anyhow context Rust prints when the state's
// PathUri describes a path this host cannot represent.
type SandboxStateCWDHostError struct {
	URI      string
	Platform string
}

func (e *SandboxStateCWDHostError) Error() string {
	return "sandbox state cwd is not native to this host\n\nCaused by:\n    '" + e.URI + "' is invalid on '" + e.Platform + "'"
}

// SandboxStateValue is a decoded --sandbox-state-json payload. The permission
// profile keeps its original JSON text so the sandbox runner sees exactly what
// the launcher sent.
type SandboxStateValue struct {
	PermissionProfileJSON string
	CodexLinuxSandboxExe  *string
	UseLegacyLandlock     *bool
	UseMXC                *bool

	// The PathUri split into the pieces PathUri::to_abs_path needs: the
	// canonical `file:` spelling (used in diagnostics), the authority and the
	// URI path.
	cwdCanonical string
	cwdHost      string
	cwdPath      string

	// head and tail bracket the sandboxCwd string in the rebuilt payload: an
	// object is spliced in place, while a positional sequence is rewritten as
	// the equivalent object because the runner only reads objects.
	head string
	tail string
}

// NativeCWD converts the state's PathUri into a path this host understands,
// mirroring PathUri::to_abs_path.
func (v *SandboxStateValue) NativeCWD() (string, error) {
	return sandboxStateNativePath(v.cwdCanonical, v.cwdHost, v.cwdPath)
}

// NormalizedJSON returns the payload with sandboxCwd rewritten as the native
// path the sandbox runner expects; every other byte is preserved.
func (v *SandboxStateValue) NormalizedJSON(nativeCWD string) string {
	if v == nil {
		return ""
	}
	encoded := make([]byte, 0, len(v.head)+len(nativeCWD)+len(v.tail))
	encoded = append(encoded, v.head...)
	encoded = append(encoded, strconv.Quote(nativeCWD)...)
	encoded = append(encoded, v.tail...)
	return string(encoded)
}

// ParseSandboxStateJSONValue decodes a --sandbox-state-json payload the way
// serde_json decodes codex_mcp::SandboxState.
func ParseSandboxStateJSONValue(raw string) (*SandboxStateValue, error) {
	scanner := &clapJSONScanner{src: []byte(raw)}
	node, detail := scanner.parseValue()
	if detail != "" {
		return nil, &SandboxStateJSONError{Detail: detail}
	}
	scanner.skipSpace()
	if scanner.i < len(scanner.src) {
		return nil, &SandboxStateJSONError{Detail: scanner.detail("trailing characters", scanner.i+1)}
	}
	value := &SandboxStateValue{}
	if detail := value.decode(scanner, node); detail != "" {
		return nil, &SandboxStateJSONError{Detail: detail}
	}
	return value, nil
}

// sandboxStateFields lists the struct fields in declaration order; serde checks
// the required ones in that order once the object is exhausted.
var sandboxStateFields = []string{"permissionProfile", "codexLinuxSandboxExe", "sandboxCwd", "useLegacyLandlock", "useMxc"}

func (v *SandboxStateValue) decode(scanner *clapJSONScanner, node *clapJSONNode) string {
	switch node.kind {
	case clapJSONObject:
		return v.decodeObject(scanner, node)
	case clapJSONArray:
		return v.decodeArray(scanner, node)
	default:
		return clapJSONScalarDetail(node, "struct SandboxState")
	}
}

func (v *SandboxStateValue) decodeObject(scanner *clapJSONScanner, node *clapJSONNode) string {
	seen := map[string]bool{}
	for _, field := range node.fields {
		switch field.key {
		case "permissionProfile", "codexLinuxSandboxExe", "sandboxCwd", "useLegacyLandlock", "useMxc":
		default:
			continue
		}
		if seen[field.key] {
			return scanner.detail("duplicate field `"+field.key+"`", field.keyEnd)
		}
		seen[field.key] = true
		if detail := v.decodeField(scanner, field.key, field.value); detail != "" {
			return detail
		}
	}
	for _, required := range []string{"permissionProfile", "sandboxCwd"} {
		if !seen[required] {
			return scanner.detail("missing field `"+required+"`", node.end)
		}
	}
	return ""
}

// decodeArray follows serde's sequence representation, which fills the struct
// fields positionally before reporting a length mismatch.
func (v *SandboxStateValue) decodeArray(scanner *clapJSONScanner, node *clapJSONNode) string {
	// serde only reports a short sequence: the two trailing booleans carry
	// #[serde(default)], so three elements are enough to build the struct.
	const requiredElements = 3
	for i, elem := range node.elems {
		if i >= len(sandboxStateFields) {
			return scanner.detail("trailing characters", elem.start+1)
		}
		if detail := v.decodeField(scanner, sandboxStateFields[i], elem); detail != "" {
			return detail
		}
	}
	if len(node.elems) < requiredElements {
		return scanner.detail("invalid length "+strconv.Itoa(len(node.elems))+", expected struct SandboxState with "+strconv.Itoa(len(sandboxStateFields))+" elements", node.end)
	}
	exe := "null"
	if len(node.elems) > 1 && node.elems[1].kind != clapJSONNull {
		exe = strconv.Quote(node.elems[1].str)
	}
	legacy := "false"
	if len(node.elems) > 3 && node.elems[3].kind == clapJSONBool {
		legacy = node.elems[3].str
	}
	mxc := "false"
	if len(node.elems) > 4 && node.elems[4].kind == clapJSONBool {
		mxc = node.elems[4].str
	}
	v.head = `{"permissionProfile":` + v.PermissionProfileJSON + `,"codexLinuxSandboxExe":` + exe + `,"sandboxCwd":`
	v.tail = `,"useLegacyLandlock":` + legacy + `,"useMxc":` + mxc + `}`
	return ""
}

func (v *SandboxStateValue) decodeField(scanner *clapJSONScanner, key string, node *clapJSONNode) string {
	switch key {
	case "permissionProfile":
		if detail := sandboxStatePermissionProfileDetail(scanner, node); detail != "" {
			return detail
		}
		v.PermissionProfileJSON = string(scanner.src[node.start:node.end])
		return ""
	case "codexLinuxSandboxExe":
		switch node.kind {
		case clapJSONNull:
			return ""
		case clapJSONString:
			exe := node.str
			v.CodexLinuxSandboxExe = &exe
			return ""
		default:
			return clapJSONScalarDetail(node, "path string")
		}
	case "sandboxCwd":
		if node.kind != clapJSONString {
			return clapJSONScalarDetail(node, "a string")
		}
		uri, detail := parseSandboxStatePathURI(node.str)
		if detail != "" {
			// PathUri errors surface after the whole string value is consumed.
			return scanner.detail(detail, node.end+1)
		}
		v.cwdCanonical, v.cwdHost, v.cwdPath = uri.canonical, uri.host, uri.path
		if v.head == "" && v.tail == "" {
			v.head = string(scanner.src[:node.start])
			v.tail = string(scanner.src[node.end:])
		}
		return ""
	case "useLegacyLandlock", "useMxc":
		if node.kind != clapJSONBool {
			return clapJSONScalarDetail(node, "a boolean")
		}
		flag := node.str == "true"
		if key == "useLegacyLandlock" {
			v.UseLegacyLandlock = &flag
		} else {
			v.UseMXC = &flag
		}
		return ""
	}
	return ""
}

// sandboxStatePermissionProfileDetail reports serde's untagged-enum diagnostic.
// PermissionProfile accepts either a tagged profile (`type` plus that variant's
// fields) or the legacy untagged shape; when neither matches serde reports one
// message naming the enum.
func sandboxStatePermissionProfileDetail(scanner *clapJSONScanner, node *clapJSONNode) string {
	untagged := func() string {
		return scanner.detail("data did not match any variant of untagged enum PermissionProfileDe", sandboxStateUntaggedOffset(scanner, node))
	}
	if node.kind != clapJSONObject {
		return untagged()
	}
	profileType := ""
	keys := map[string]bool{}
	for _, field := range node.fields {
		keys[field.key] = true
		if field.key != "type" {
			continue
		}
		if field.value.kind != clapJSONString {
			return untagged()
		}
		switch field.value.str {
		case "managed", "disabled", "external":
			profileType = field.value.str
		default:
			return untagged()
		}
	}
	switch profileType {
	case "managed":
		if !keys["file_system"] || !keys["network"] {
			return untagged()
		}
	case "external":
		if !keys["network"] {
			return untagged()
		}
	}
	return ""
}

// sandboxStateUntaggedOffset is where serde's untagged-enum failure surfaces:
// the end of the buffered value, or the closing brace that follows it.
func sandboxStateUntaggedOffset(scanner *clapJSONScanner, node *clapJSONNode) int {
	rest := scanner.src[node.end:]
	skip := 0
	for skip < len(rest) && (rest[skip] == ' ' || rest[skip] == '\t' || rest[skip] == '\n' || rest[skip] == '\r') {
		skip++
	}
	if skip < len(rest) && (rest[skip] == '}' || rest[skip] == ']') {
		return node.end + skip + 1
	}
	return node.end
}

// sandboxStatePathURI is a `file:` URI split into the parts a native path needs.
type sandboxStatePathURI struct {
	canonical string
	host      string
	path      string
}

// parseSandboxStatePathURI reproduces PathUri::parse: the url crate validates
// the scheme and normalises a `file:` URI to `file://<authority><path>`.
func parseSandboxStatePathURI(raw string) (sandboxStatePathURI, string) {
	scheme, rest, ok := splitPathURIScheme(raw)
	if !ok {
		return sandboxStatePathURI{}, "invalid URI: relative URL without a base"
	}
	if !strings.EqualFold(scheme, "file") {
		return sandboxStatePathURI{}, "unsupported path URI scheme `" + strings.ToLower(scheme) + "`"
	}
	uri := sandboxStatePathURI{}
	path := rest
	if strings.HasPrefix(rest, "//") {
		authority := rest[2:]
		if cut := strings.IndexByte(authority, '/'); cut >= 0 {
			uri.host = authority[:cut]
			path = authority[cut:]
		} else {
			uri.host = authority
			path = "/"
		}
	} else if !strings.HasPrefix(path, "/") {
		// `file:tmp` is a special URL whose path becomes `/tmp`.
		path = "/" + path
	}
	uri.path = path
	uri.canonical = "file://" + uri.host + path
	return uri, ""
}

// splitPathURIScheme splits `<scheme>:` off a URI the way the url crate does.
func splitPathURIScheme(raw string) (string, string, bool) {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == ':':
			if i == 0 {
				return "", "", false
			}
			return raw[:i], raw[i+1:], true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return "", "", false
		}
	}
	return "", "", false
}

// sandboxStateNativePath turns a validated PathUri into a native path or the
// error PathUri::to_abs_path reports for a foreign convention.
func sandboxStateNativePath(canonical string, host string, path string) (string, error) {
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", &SandboxStateCWDHostError{URI: canonical, Platform: runtime.GOOS}
	}
	if runtime.GOOS != "windows" {
		if host != "" && host != "localhost" {
			return "", &SandboxStateCWDHostError{URI: canonical, Platform: runtime.GOOS}
		}
		return decoded, nil
	}
	if host != "" && host != "localhost" {
		return `\\` + host + strings.ReplaceAll(decoded, "/", `\`), nil
	}
	if len(decoded) >= 3 && decoded[0] == '/' && decoded[2] == ':' && isASCIILetter(decoded[1]) {
		drive := strings.ToUpper(decoded[1:2])
		return drive + strings.ReplaceAll(decoded[2:], "/", `\`), nil
	}
	return "", &SandboxStateCWDHostError{URI: canonical, Platform: runtime.GOOS}
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

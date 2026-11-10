package tcptunnel

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Bounds mirror Rust's `codex_tcp_tunnel::control` constants.
const (
	MaxTokenBytes              = 64 * 1024
	MaxConnectMetadataBytes    = 16 * 1024
	MaxConnectHeaders          = 16
	MaxConnectHeaderValueBytes = 4 * 1024
)

// ReadConnectHeaders mirrors Rust's `read_connect_headers`: one newline
// terminated JSON list of `["x-name","value"]` pairs, bounded in count, name
// shape, and value length. Only non-forwarding `x-` extension headers are
// accepted.
func ReadConnectHeaders(reader *bufio.Reader) (http.Header, error) {
	line, err := readBoundedLine(reader, MaxConnectMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("reading CONNECT metadata: %w", err)
	}
	if len(line) == 0 || len(line) > MaxConnectMetadataBytes || line[len(line)-1] != '\n' {
		return nil, fmt.Errorf("invalid or oversized CONNECT metadata")
	}
	line = line[:len(line)-1]
	var pairs [][]string
	if err := json.Unmarshal(line, &pairs); err != nil {
		return nil, fmt.Errorf("invalid CONNECT metadata")
	}
	if len(pairs) > MaxConnectHeaders {
		return nil, fmt.Errorf("too many CONNECT metadata headers")
	}
	headers := http.Header{}
	for _, pair := range pairs {
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid CONNECT metadata")
		}
		name, value := pair[0], pair[1]
		canonical := http.CanonicalHeaderKey(name)
		if !validHeaderName(name) {
			return nil, fmt.Errorf("invalid CONNECT metadata header name")
		}
		if _, exists := headers[canonical]; exists {
			return nil, fmt.Errorf("duplicate CONNECT metadata header")
		}
		extension := strings.ToLower(name)
		if !strings.HasPrefix(extension, "x-") ||
			strings.HasPrefix(extension, "x-forwarded-") ||
			extension == "x-real-ip" {
			return nil, fmt.Errorf("CONNECT metadata must use non-forwarding extension headers")
		}
		if len(value) > MaxConnectHeaderValueBytes {
			return nil, fmt.Errorf("CONNECT metadata header value is too long")
		}
		if !validHeaderValue(value) {
			return nil, fmt.Errorf("invalid CONNECT metadata header value")
		}
		headers[canonical] = []string{value}
	}
	return headers, nil
}

// TunnelCredential selects the bearer shape a proxy expects: a plain OAuth or
// opaque bearer, or the ERS-issued tunnel JWT that Portals requires for
// ERS-authenticated listeners (Rust #60568).
type TunnelCredential int

const (
	// CredentialBearer accepts any non-empty token but rejects ERS tunnel JWTs
	// that predate the v2 wire version.
	CredentialBearer TunnelCredential = iota
	// CredentialErsTunnelJWT additionally requires every token to be an
	// ERS-issued v2 tunnel JWT.
	CredentialErsTunnelJWT
)

// Read returns one trimmed bearer line, with a nil result meaning the
// controlling pipe closed.
func (c TunnelCredential) Read(reader *bufio.Reader) (*string, error) {
	line, err := readBoundedLine(reader, MaxTokenBytes)
	if err != nil {
		return nil, fmt.Errorf("reading MASQUE token: %w", err)
	}
	if len(line) == 0 {
		return nil, nil
	}
	if len(line) > MaxTokenBytes {
		return nil, fmt.Errorf("MASQUE token is too long")
	}
	if !utf8.Valid(line) {
		return nil, fmt.Errorf("invalid MASQUE token encoding")
	}
	auth, err := c.Header(strings.TrimSpace(string(line)))
	if err != nil {
		return nil, err
	}
	return &auth, nil
}

// Header validates one token and renders the Authorization value. Tokens that
// announce themselves as ERS tunnel JWTs are checked for the v2 wire version;
// the gateway still verifies the signature.
func (c TunnelCredential) Header(token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("empty MASQUE token")
	}
	parts := strings.Split(token, ".")
	ers := false
	if header, ok := jwtSegment(parts[0]); ok {
		if typ, _ := header["typ"].(string); typ == "cca-tunnel+jwt" {
			ers = true
		}
	}
	if ers || c == CredentialErsTunnelJWT {
		if !ers || len(parts) != 3 || parts[2] == "" || !jwtVersionTwo(parts[1]) {
			return "", fmt.Errorf("Portals requires an ERS-issued v2 tunnel JWT")
		}
	}
	auth := "Bearer " + token
	if !validHeaderValue(auth) {
		return "", fmt.Errorf("invalid MASQUE token header")
	}
	return auth, nil
}

// ReadAuthToken mirrors Rust's former read_auth_token: one trimmed bearer line
// in plain bearer mode.
func ReadAuthToken(reader *bufio.Reader) (*string, error) {
	return CredentialBearer.Read(reader)
}

// jwtSegment decodes one unpadded base64url JWT segment as a JSON object.
func jwtSegment(segment string) (map[string]any, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if json.Unmarshal(decoded, &object) != nil {
		return nil, false
	}
	return object, true
}

// jwtVersionTwo reports whether the claims segment carries ver equal to 2.
func jwtVersionTwo(segment string) bool {
	claims, ok := jwtSegment(segment)
	if !ok {
		return false
	}
	version, ok := claims["ver"].(float64)
	return ok && version == 2
}

// readBoundedLine mirrors Rust's `Read::take(limit + 1).read_until(b'\n', ..)`:
// it returns at most limit+1 bytes including the terminating newline when one
// is present, and an empty slice at EOF with no data. A longer line keeps
// reading to its newline so an oversized input is reported rather than
// silently truncated.
func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	line := []byte{}
	for {
		chunk, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				return line, nil
			}
			return nil, err
		}
		if len(line) < limit+1 {
			line = append(line, chunk)
		}
		if chunk == '\n' {
			break
		}
	}
	return line, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' || c == '\'' ||
			c == '*' || c == '+' || c == '-' || c == '.' || c == '^' || c == '_' ||
			c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// controlMetadata is the parsed CONNECT metadata, or the error that stopped it.
type controlMetadata struct {
	headers http.Header
	err     error
}

// controlToken is one bearer line, or the error that stopped the stream.
// A nil token with a nil error means the controlling pipe closed.
type controlToken struct {
	auth string
	err  error
}

// controlInput mirrors Rust's `control_input`: a background reader that first
// produces the CONNECT metadata (when requested) and then the bearer stream.
// Invalid metadata stops the credential stream without exposing secrets.
type controlInput struct {
	metadata chan controlMetadata
	tokens   chan controlToken
}

func newControlInput(reader *bufio.Reader, connectHeadersStdin bool, proxyOrigin string) *controlInput {
	input := &controlInput{
		metadata: make(chan controlMetadata, 1),
		tokens:   make(chan controlToken, 1),
	}
	go func() {
		headers := http.Header{}
		if connectHeadersStdin {
			parsed, err := ReadConnectHeaders(reader)
			if err != nil {
				input.metadata <- controlMetadata{err: err}
				close(input.tokens)
				return
			}
			headers = parsed
		}
		input.metadata <- controlMetadata{headers: headers}
		// Portals listeners need ERS-issued v2 tunnel JWTs unless the caller
		// selected OAuth mode explicitly (Rust #60568).
		credential := CredentialBearer
		if containsString(GatewayOrigins, proxyOrigin) && headers.Get("x-portals-auth-mode") != "oauth" {
			credential = CredentialErsTunnelJWT
		}
		for {
			token, err := credential.Read(reader)
			if err != nil {
				input.tokens <- controlToken{err: err}
				close(input.tokens)
				return
			}
			if token == nil {
				close(input.tokens)
				return
			}
			input.tokens <- controlToken{auth: *token}
		}
	}()
	return input
}

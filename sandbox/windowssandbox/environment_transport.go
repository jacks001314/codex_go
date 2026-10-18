package windowssandbox

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	SandboxLaunchPayloadArg = "--launch-payload-env"

	sandboxLaunchEnvPrefix     = "CODEX_SANDBOX_LAUNCH_"
	sandboxLaunchEnvCount      = "CODEX_SANDBOX_LAUNCH_COUNT"
	sandboxLaunchEnvLength     = "CODEX_SANDBOX_LAUNCH_BYTES"
	sandboxLaunchEnvChunkBytes = 16 * 1024
	sandboxLaunchEnvMaxBytes   = 16 * 1024 * 1024
	sandboxLaunchEnvMaxChunks  = sandboxLaunchEnvMaxBytes/(sandboxLaunchEnvChunkBytes-3) + 1
)

// NeedsSandboxLaunchEnvironment reports whether the payload is too large for a
// Windows command line (Rust #47919).
func NeedsSandboxLaunchEnvironment(payload string) bool {
	return len(utf16.Encode([]rune(payload))) > 24_000
}

// IsSandboxLaunchEnvKey reports whether key belongs to the launcher-only
// environment namespace.
func IsSandboxLaunchEnvKey(key string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), strings.ToUpper(sandboxLaunchEnvPrefix))
}

// EncodeSandboxLaunchPayload transports a UTF-8 payload through bounded
// environment chunks. Existing transport variables are removed only after the
// complete payload has passed validation.
func EncodeSandboxLaunchPayload(payload string, env map[string]string) error {
	if len(payload) == 0 || len(payload) > sandboxLaunchEnvMaxBytes {
		return fmt.Errorf("sandbox launch payload must contain 1..%d bytes", sandboxLaunchEnvMaxBytes)
	}
	if strings.ContainsRune(payload, '\x00') {
		return errors.New("sandbox launch payload contains a NUL")
	}
	if !utf8.ValidString(payload) {
		return errors.New("sandbox launch payload is not valid UTF-8")
	}
	if env == nil {
		return errors.New("sandbox launch environment is required")
	}
	chunks := make([]string, 0, len(payload)/sandboxLaunchEnvChunkBytes+1)
	for start := 0; start < len(payload); {
		end := start + sandboxLaunchEnvChunkBytes
		if end > len(payload) {
			end = len(payload)
		}
		for end > start && end < len(payload) && !utf8.RuneStart(payload[end]) {
			end--
		}
		for end < len(payload) && !utf8.ValidString(payload[start:end]) {
			end--
		}
		if end <= start {
			return errors.New("sandbox launch payload cannot be chunked safely")
		}
		chunks = append(chunks, payload[start:end])
		start = end
	}
	if len(chunks) > sandboxLaunchEnvMaxChunks {
		return errors.New("sandbox launch payload has too many chunks")
	}
	for key := range env {
		if IsSandboxLaunchEnvKey(key) {
			delete(env, key)
		}
	}
	env[sandboxLaunchEnvCount] = strconv.Itoa(len(chunks))
	env[sandboxLaunchEnvLength] = strconv.Itoa(len(payload))
	for index, chunk := range chunks {
		env[fmt.Sprintf("%s%d", sandboxLaunchEnvPrefix, index)] = chunk
	}
	return nil
}

// DecodeSandboxLaunchPayload reconstructs a payload from environment chunks
// without inspecting unrelated variables.
func DecodeSandboxLaunchPayload(env map[string]string) (string, error) {
	encoded := map[string]string{}
	for key, value := range env {
		if !IsSandboxLaunchEnvKey(key) {
			continue
		}
		canonical := canonicalSandboxLaunchEnvKey(key)
		if len(encoded) >= sandboxLaunchEnvMaxChunks+2 ||
			len(canonical) > len(sandboxLaunchEnvPrefix)+32 ||
			len(value) > sandboxLaunchEnvChunkBytes {
			return "", errors.New("oversized sandbox launch environment")
		}
		if _, duplicate := encoded[canonical]; duplicate {
			return "", errors.New("duplicate sandbox launch environment variables")
		}
		encoded[canonical] = value
	}
	count, err := strconv.Atoi(encoded[sandboxLaunchEnvCount])
	if err != nil {
		return "", errors.New("missing sandbox launch chunk count")
	}
	length, err := strconv.Atoi(encoded[sandboxLaunchEnvLength])
	if err != nil {
		return "", errors.New("missing sandbox launch payload length")
	}
	if count < 1 || count > sandboxLaunchEnvMaxChunks || length > sandboxLaunchEnvMaxBytes {
		return "", errors.New("invalid sandbox launch payload size")
	}
	if len(encoded) != count+2 {
		return "", errors.New("unexpected sandbox launch environment variables")
	}
	var builder strings.Builder
	builder.Grow(length)
	for index := 0; index < count; index++ {
		chunk, ok := encoded[fmt.Sprintf("%s%d", sandboxLaunchEnvPrefix, index)]
		if !ok {
			return "", errors.New("missing sandbox launch chunk")
		}
		if chunk == "" || builder.Len()+len(chunk) > length {
			return "", errors.New("sandbox launch payload length mismatch")
		}
		builder.WriteString(chunk)
	}
	payload := builder.String()
	if len(payload) != length || !utf8.ValidString(payload) {
		return "", errors.New("sandbox launch payload length mismatch")
	}
	return payload, nil
}

// DecodeAndScrubSandboxLaunchPayload removes transport variables even when
// decoding fails.
func DecodeAndScrubSandboxLaunchPayload(env map[string]string) (string, error) {
	payload, err := DecodeSandboxLaunchPayload(env)
	for key := range env {
		if IsSandboxLaunchEnvKey(key) {
			delete(env, key)
		}
	}
	return payload, err
}

func canonicalSandboxLaunchEnvKey(key string) string {
	upper := strings.ToUpper(strings.TrimSpace(key))
	switch upper {
	case sandboxLaunchEnvCount:
		return sandboxLaunchEnvCount
	case sandboxLaunchEnvLength:
		return sandboxLaunchEnvLength
	default:
		return sandboxLaunchEnvPrefix + upper[len(sandboxLaunchEnvPrefix):]
	}
}

// SandboxLaunchEnvironmentFromEnviron scans process environment variables into
// a map, ignoring malformed entries.
func SandboxLaunchEnvironmentFromEnviron(values []string) map[string]string {
	env := make(map[string]string, len(values))
	for _, item := range values {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	return env
}

// SandboxLaunchEnvironFromMap renders a map back into KEY=VALUE entries.
func SandboxLaunchEnvironFromMap(env map[string]string) []string {
	values := make([]string, 0, len(env))
	for key, value := range env {
		values = append(values, key+"="+value)
	}
	return values
}

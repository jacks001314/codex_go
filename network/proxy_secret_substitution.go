package network

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxBodySubstitutionSearchBytes bounds the worst-case placeholder window scans.
	MaxBodySubstitutionSearchBytes = 256 * 1024 * 1024
	// MaxBodySubstitutionOccurrences bounds how many placeholders one request may expand.
	MaxBodySubstitutionOccurrences = 65536
	// DefaultBodySubstitutionMaxBodyBytes mirrors Rust's default_max_body_bytes.
	DefaultBodySubstitutionMaxBodyBytes = 8 * 1024 * 1024
	bodySubstitutionMaxBodyBytes        = 16 * 1024 * 1024
	bodySubstitutionMaxReplacements     = 32
	bodySubstitutionReadTimeout         = 30 * time.Second
	bodySubstitutionDefaultPort         = 443
)

// BodySecretReplacement binds an opaque placeholder to a proxy-held secret.
type BodySecretReplacement struct {
	Placeholder string
	Secret      ProxySecretSource
}

// BodySecretSubstitutionConfig selects exact byte-level body substitution for a
// host/method/path MITM hook.
type BodySecretSubstitutionConfig struct {
	Replacements []BodySecretReplacement
	Port         int
	MaxBodyBytes int
}

// BodySecretSubstitution holds resolved secret values in the trusted proxy.
type BodySecretSubstitution struct {
	config  BodySecretSubstitutionConfig
	secrets [][]byte
}

// Validate mirrors Rust's BodySecretSubstitutionConfig::validate.
func (c *BodySecretSubstitutionConfig) Validate() error {
	if c == nil {
		return errors.New("missing body substitution configuration")
	}
	if c.Port == 0 || c.MaxBodyBytes > bodySubstitutionMaxBodyBytes ||
		len(c.Replacements) == 0 || len(c.Replacements) > bodySubstitutionMaxReplacements {
		return errors.New("invalid body substitution port, bindings, or limit (maximum 16 MiB)")
	}
	for index, replacement := range c.Replacements {
		if len(replacement.Placeholder) < 16 || len(replacement.Placeholder) > 256 || !isASCIIString(replacement.Placeholder) {
			return errors.New("body placeholders must be opaque ASCII markers of 16 to 256 bytes")
		}
		if err := replacement.Secret.validate(); err != nil {
			return err
		}
		for _, previous := range c.Replacements[:index] {
			if strings.Contains(previous.Placeholder, replacement.Placeholder) ||
				strings.Contains(replacement.Placeholder, previous.Placeholder) {
				return errors.New("body substitution bindings overlap")
			}
		}
	}
	return nil
}

// Compile resolves the configured secret sources.
func (c *BodySecretSubstitutionConfig) Compile(resolveEnv ProxySecretEnvResolver, readFile ProxySecretFileReader) (*BodySecretSubstitution, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	secrets := make([][]byte, 0, len(c.Replacements))
	for _, replacement := range c.Replacements {
		secret, err := replacement.Secret.resolve(resolveEnv, readFile)
		if err != nil {
			return nil, err
		}
		if len(secret) > c.MaxBodyBytes {
			return nil, errors.New("body substitution secret exceeds limit or contains a placeholder")
		}
		for _, binding := range c.Replacements {
			if strings.Contains(secret, binding.Placeholder) {
				return nil, errors.New("body substitution secret exceeds limit or contains a placeholder")
			}
		}
		secrets = append(secrets, []byte(secret))
	}
	return &BodySecretSubstitution{config: *c, secrets: secrets}, nil
}

// Config exposes the compiled configuration.
func (s *BodySecretSubstitution) Config() BodySecretSubstitutionConfig {
	if s == nil {
		return BodySecretSubstitutionConfig{}
	}
	return s.config
}

// bodySubstitutionHeadersSupported rejects payloads whose integrity or framing
// a byte-exact rewrite would invalidate.
func bodySubstitutionHeadersSupported(headers http.Header) bool {
	for _, value := range headers.Values("Content-Encoding") {
		for _, coding := range strings.Split(value, ",") {
			if !strings.EqualFold(strings.TrimSpace(coding), "identity") {
				return false
			}
		}
	}
	if !supportedProxyTransferFraming(headers) || isAmazonEventStream(headers) {
		return false
	}
	if len(headers.Values("Trailer")) > 0 || len(headers.Values("X-Amz-Trailer")) > 0 || len(headers.Values("X-Amz-Decoded-Content-Length")) > 0 {
		return false
	}
	for name := range headers {
		lower := strings.ToLower(name)
		switch lower {
		case "content-md5", "digest", "content-digest", "repr-digest", "x-amz-content-sha256":
			return false
		}
		if strings.HasPrefix(lower, "x-amz-checksum-") {
			return false
		}
	}
	return true
}

// Apply replaces every configured placeholder in the request body. original
// headers supported captures the framing seen before hook actions rewrote the
// request, mirroring Rust's preserve_body_substitution_headers.
func (s *BodySecretSubstitution) Apply(req *http.Request, originalHeadersSupported bool) error {
	if s == nil || req == nil || req.URL == nil {
		return errors.New("missing body substitution state")
	}
	if req.URL.Scheme != "https" || awsSigV4RequestPort(req) != s.config.Port {
		return errors.New("body substitution requires its configured HTTPS port")
	}
	if !originalHeadersSupported || !bodySubstitutionHeadersSupported(req.Header) {
		return errors.New("body substitution does not support encoded, streaming-signed, or integrity-protected payloads")
	}
	release, err := reserveProxyAWSBuffer(req.Context())
	if err != nil {
		return err
	}
	defer release()
	body, err := collectProxyRequestBodyWithTimeout(req, int64(s.config.MaxBodyBytes), bodySubstitutionReadTimeout)
	if err != nil {
		if strings.Contains(err.Error(), "timed out") {
			return errors.New("body substitution read timed out")
		}
		return errors.New("body substitution exceeds limit or cannot read payload")
	}
	if len(req.Trailer) > 0 {
		return errors.New("body substitution does not support request trailers")
	}
	if !proxyContentLengthMatches(req, len(body)) {
		return errors.New("body substitution content length does not match the request body")
	}
	searchBytes := 0
	for _, binding := range s.config.Replacements {
		windows := len(body) - (len(binding.Placeholder) - 1)
		if windows < 0 {
			windows = 0
		}
		if searchBytes > MaxBodySubstitutionSearchBytes-windows*len(binding.Placeholder) {
			return errors.New("body substitution search exceeds work limit")
		}
		searchBytes += windows * len(binding.Placeholder)
	}
	var matches []bodySubstitutionMatch
	for index, binding := range s.config.Replacements {
		placeholder := []byte(binding.Placeholder)
		for offset := 0; offset+len(placeholder) <= len(body); offset++ {
			if !bytes.Equal(body[offset:offset+len(placeholder)], placeholder) {
				continue
			}
			if len(matches) >= MaxBodySubstitutionOccurrences {
				return errors.New("too many body substitution occurrences")
			}
			matches = append(matches, bodySubstitutionMatch{start: offset, end: offset + len(placeholder), index: index})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		if matches[i].end != matches[j].end {
			return matches[i].end < matches[j].end
		}
		return matches[i].index < matches[j].index
	})
	size := len(body)
	cursor := 0
	for _, candidate := range matches {
		if candidate.start < cursor {
			return errors.New("body substitution matches overlap")
		}
		size = size - (candidate.end - candidate.start) + len(s.secrets[candidate.index])
		if size > s.config.MaxBodyBytes {
			return errors.New("expanded body substitution exceeds limit")
		}
		cursor = candidate.end
	}
	output := make([]byte, 0, size)
	cursor = 0
	for _, candidate := range matches {
		output = append(output, body[cursor:candidate.start]...)
		output = append(output, s.secrets[candidate.index]...)
		cursor = candidate.end
	}
	output = append(output, body[cursor:]...)
	req.Header.Del("Transfer-Encoding")
	req.Header.Del("Trailer")
	req.Header.Set("Content-Length", strconv.Itoa(len(output)))
	req.Body = io.NopCloser(bytes.NewReader(output))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(output)), nil
	}
	req.ContentLength = int64(len(output))
	req.TransferEncoding = nil
	return nil
}

type bodySubstitutionMatch struct {
	start int
	end   int
	index int
}

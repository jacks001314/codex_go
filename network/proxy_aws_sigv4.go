package network

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// AWSProxyCABundleEnvVar is the AWS SDK trust-bundle override mirrored by
	// Rust's aws_sigv4::AWS_CA_BUNDLE.
	AWSProxyCABundleEnvVar = "AWS_CA_BUNDLE"

	awsSigV4DummySecret  = "codex-local-aws-secret"
	awsSigV4DummyToken   = "codex-local-aws-token"
	awsSigV4DefaultPort  = 443
	awsSigV4MaxBodyBytes = 16 * 1024 * 1024

	awsSigV4BodyReadTimeout = 30 * time.Second
	awsSigV4BufferWaitTime  = 5 * time.Second
	awsSigV4BufferSlots     = 8
)

// DefaultAwsSigV4MaxBodyBytes is the signing buffer limit applied when a hook
// action omits max_body_bytes (Rust's default_max_body_bytes).
const DefaultAwsSigV4MaxBodyBytes = 8 * 1024 * 1024

// AWSSigV4Config selects an exact-host AWS SigV4 action for a MITM hook.
type AWSSigV4Config struct {
	PlaceholderAccessKeyID string
	Region                 string
	Service                string
	AccessKeyID            ProxySecretSource
	SecretAccessKey        ProxySecretSource
	SessionToken           *ProxySecretSource
	Port                   int
	MaxBodyBytes           int
}

type awsSigV4Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// AWSSigV4Signer holds resolved credentials in the trusted proxy process.
type AWSSigV4Signer struct {
	config      AWSSigV4Config
	credentials awsSigV4Credentials
}

type awsGuestSignature struct {
	time            time.Time
	signedHeaders   []string
	unsignedPayload bool
}

var errAWSSigV4Unsupported = errors.New("unsupported AWS signing request")

// ValidateAWSSigV4Config mirrors Rust's AwsSigV4Config::validate.
func (c *AWSSigV4Config) Validate() error {
	if c == nil {
		return errors.New("missing AWS signing configuration")
	}
	if c.PlaceholderAccessKeyID == "" || len(c.PlaceholderAccessKeyID) > 128 || !isAWSPlaceholderAccessKeyID(c.PlaceholderAccessKeyID) {
		return errors.New("invalid AWS placeholder access key ID")
	}
	for _, value := range []string{c.Region, c.Service} {
		if value == "" || !isAWSRegionOrService(value) {
			return errors.New("invalid AWS signing region or service")
		}
	}
	if c.Port == 0 {
		return errors.New("invalid AWS signing port")
	}
	if c.MaxBodyBytes > awsSigV4MaxBodyBytes {
		return errors.New("AWS signing body limit exceeds 16 MiB")
	}
	if err := c.AccessKeyID.validate(); err != nil {
		return err
	}
	if err := c.SecretAccessKey.validate(); err != nil {
		return err
	}
	if c.SessionToken != nil {
		if err := c.SessionToken.validate(); err != nil {
			return err
		}
	}
	return nil
}

func isAWSPlaceholderAccessKeyID(value string) bool {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func isAWSRegionOrService(value string) bool {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func (s ProxySecretSource) validate() error {
	switch s.Kind {
	case "env_var":
		if s.Name != "" && !strings.ContainsAny(s.Name, "=\x00") {
			return nil
		}
	case "file":
		if strings.HasPrefix(s.Name, "/") || (len(s.Name) > 2 && s.Name[1] == ':' && (s.Name[2] == '\\' || s.Name[2] == '/')) {
			return nil
		}
	}
	return errors.New("invalid proxy secret source")
}

func (s ProxySecretSource) resolve(resolveEnv ProxySecretEnvResolver, readFile ProxySecretFileReader) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	var value string
	switch s.Kind {
	case "env_var":
		resolved, ok := resolveEnv(s.Name)
		if !ok {
			return "", errors.New("missing proxy credential")
		}
		value = resolved
	case "file":
		contents, err := readFile(s.Name)
		if err != nil {
			return "", err
		}
		value = contents
	default:
		return "", errors.New("invalid proxy secret source")
	}
	if value == "" || len(value) > 65536 {
		return "", errors.New("proxy credential is empty or exceeds 64 KiB")
	}
	return value, nil
}

// CompileAWSSigV4 resolves the configured credential sources.
func (c *AWSSigV4Config) Compile(resolveEnv ProxySecretEnvResolver, readFile ProxySecretFileReader) (*AWSSigV4Signer, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	accessKeyID, err := c.AccessKeyID.resolve(resolveEnv, readFile)
	if err != nil {
		return nil, err
	}
	secretAccessKey, err := c.SecretAccessKey.resolve(resolveEnv, readFile)
	if err != nil {
		return nil, err
	}
	var sessionToken string
	if c.SessionToken != nil {
		sessionToken, err = c.SessionToken.resolve(resolveEnv, readFile)
		if err != nil {
			return nil, err
		}
	}
	return &AWSSigV4Signer{
		config: *c,
		credentials: awsSigV4Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    sessionToken,
		},
	}, nil
}

// Config exposes the compiled action configuration.
func (s *AWSSigV4Signer) Config() AWSSigV4Config {
	if s == nil {
		return AWSSigV4Config{}
	}
	return s.config
}

// ValidateGuestPlaceholders rejects a placeholder that contains a real credential
// of any configured pair.
func ValidateGuestPlaceholders(signers []*AWSSigV4Signer) error {
	for _, signer := range signers {
		if signer == nil {
			continue
		}
		reals := []string{signer.credentials.AccessKeyID, signer.credentials.SecretAccessKey, signer.credentials.SessionToken}
		placeholders := []string{awsSigV4DummySecret, awsSigV4DummyToken}
		for _, other := range signers {
			if other != nil {
				placeholders = append(placeholders, other.config.PlaceholderAccessKeyID)
			}
		}
		for _, placeholder := range placeholders {
			for _, real := range reals {
				if real != "" && strings.Contains(placeholder, real) {
					return errors.New("AWS guest placeholders overlap a real credential")
				}
			}
		}
	}
	return nil
}

// ProxyAWSEnvKeysToSnapshot lists the AWS variables a local process may have
// rewritten and that must be restored after the child exits.
func ProxyAWSEnvKeysToSnapshot(env map[string]string) ([]string, bool) {
	secret, _ := providerEnvValue(env, "AWS_SECRET_ACCESS_KEY")
	caBundle, _ := providerEnvValue(env, AWSProxyCABundleEnvVar)
	if secret != awsSigV4DummySecret && !isManagedMITMCATrustBundlePath(caBundle) {
		return nil, false
	}
	return []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_SECURITY_TOKEN",
		AWSProxyCABundleEnvVar,
	}, true
}

// MaskLocalAWSEnv installs placeholder AWS credentials for a local process.
func MaskLocalAWSEnv(signers []*AWSSigV4Signer, env map[string]string, caPath string) {
	if len(signers) == 0 || env == nil {
		return
	}
	if current, _ := providerEnvValue(env, AWSProxyCABundleEnvVar); current == "" && caPath != "" {
		setProxyEnvValue(env, AWSProxyCABundleEnvVar, caPath)
	}
	access, hasAccess := providerEnvValue(env, "AWS_ACCESS_KEY_ID")
	if access == "" {
		hasAccess = false
	}
	_, hasProfile := providerEnvValue(env, "AWS_PROFILE")
	_, hasDefaultProfile := providerEnvValue(env, "AWS_DEFAULT_PROFILE")
	useDefault := !hasAccess && !hasProfile && !hasDefaultProfile

	var candidates []*AWSSigV4Signer
	for _, signer := range signers {
		if signer == nil {
			continue
		}
		if useDefault || (hasAccess && (access == signer.credentials.AccessKeyID || access == signer.config.PlaceholderAccessKeyID)) {
			candidates = append(candidates, signer)
		}
	}
	var selected *AWSSigV4Signer
	if len(candidates) > 0 {
		selected = candidates[0]
		for _, other := range candidates[1:] {
			if other.config.PlaceholderAccessKeyID != selected.config.PlaceholderAccessKeyID ||
				(other.credentials.SessionToken != "") != (selected.credentials.SessionToken != "") {
				selected = nil
				break
			}
		}
	}
	ambiguousAccess := hasAccess && len(candidates) > 0 && selected == nil
	for key, value := range env {
		env[key] = MaskLocalAWSText(signers, value)
	}
	if ambiguousAccess {
		for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN"} {
			removeProxyEnvValue(env, key)
		}
		return
	}
	if selected == nil {
		return
	}
	setProxyEnvValue(env, "AWS_ACCESS_KEY_ID", selected.config.PlaceholderAccessKeyID)
	setProxyEnvValue(env, "AWS_SECRET_ACCESS_KEY", awsSigV4DummySecret)
	removeProxyEnvValue(env, "AWS_SECURITY_TOKEN")
	if selected.credentials.SessionToken != "" {
		setProxyEnvValue(env, "AWS_SESSION_TOKEN", awsSigV4DummyToken)
	} else {
		removeProxyEnvValue(env, "AWS_SESSION_TOKEN")
	}
}

// MaskLocalAWSText replaces configured real credentials with their placeholders.
func MaskLocalAWSText(signers []*AWSSigV4Signer, text string) string {
	for _, signer := range signers {
		if signer == nil {
			continue
		}
		pairs := [][2]string{
			{signer.credentials.AccessKeyID, signer.config.PlaceholderAccessKeyID},
			{signer.credentials.SecretAccessKey, awsSigV4DummySecret},
			{signer.credentials.SessionToken, awsSigV4DummyToken},
		}
		for _, pair := range pairs {
			if pair[0] != "" && strings.Contains(text, pair[0]) {
				text = strings.ReplaceAll(text, pair[0], pair[1])
			}
		}
	}
	return text
}

func setProxyEnvValue(env map[string]string, key string, value string) {
	if env == nil {
		return
	}
	for candidate := range env {
		if candidate == key || (runtime.GOOS == "windows" && strings.EqualFold(candidate, key)) {
			delete(env, candidate)
		}
	}
	env[key] = value
}

func removeProxyEnvValue(env map[string]string, key string) {
	if env == nil {
		return
	}
	for candidate := range env {
		if candidate == key || (runtime.GOOS == "windows" && strings.EqualFold(candidate, key)) {
			delete(env, candidate)
		}
	}
}

// UnsafeAWSConnectionHeader reports whether a connection-specific header names
// authorization, which makes the guest signature unusable after hop removal.
func unsafeAWSConnectionHeader(headers http.Header) bool {
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "authorization") {
				return true
			}
		}
	}
	return false
}

// PreserveSigningHeaders returns the guest key IDs that cannot be re-signed
// because signing headers are unsupported (Rust's preserve_signing_headers).
func PreserveSigningHeaders(headers http.Header) []string {
	if len(headers.Values("Authorization")) == 0 {
		return nil
	}
	if !UnsupportedAWSSigningHeaders(headers) && !unsafeAWSConnectionHeader(headers) {
		return nil
	}
	var keys []string
	for _, value := range headers.Values("Authorization") {
		keys = append(keys, awsAuthorizationKeys(value)...)
	}
	return keys
}

// UnsupportedAWSSigningHeaders reports framing that cannot be re-signed.
func UnsupportedAWSSigningHeaders(headers http.Header) bool {
	if !supportedProxyTransferFraming(headers) || isAmazonEventStream(headers) {
		return true
	}
	if len(headers.Values("Trailer")) > 0 || len(headers.Values("X-Amz-Trailer")) > 0 || len(headers.Values("X-Amz-Decoded-Content-Length")) > 0 {
		return true
	}
	for _, value := range headers.Values("Content-Encoding") {
		for _, coding := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(coding), "aws-chunked") {
				return true
			}
		}
	}
	for _, value := range headers.Values("X-Amz-Content-Sha256") {
		if strings.HasPrefix(value, "STREAMING-") {
			return true
		}
	}
	return false
}

func awsAuthorizationKeys(auth string) []string {
	fields, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !ok {
		if rest, ok := strings.CutPrefix(auth, "AWS4-ECDSA-P256-SHA256 "); ok {
			fields = rest
		} else {
			return nil
		}
	}
	var keys []string
	for _, field := range strings.Split(fields, ",") {
		trimmed := strings.TrimSpace(field)
		credential, ok := strings.CutPrefix(trimmed, "Credential=")
		if !ok {
			continue
		}
		key, _, _ := strings.Cut(credential, "/")
		keys = append(keys, key)
	}
	return keys
}

// Plan selects the guest signature for this request, or nil when the request
// does not use the configured placeholder.
func (s *AWSSigV4Signer) Plan(req *http.Request, unsupportedKeys []string) (*awsGuestSignature, error) {
	if s == nil || req == nil {
		return nil, nil
	}
	placeholder := s.config.PlaceholderAccessKeyID
	selectsPair := func(scope string) bool {
		key, _, _ := strings.Cut(scope, "/")
		return key == placeholder
	}
	if req.URL != nil {
		for _, pair := range parseProxyRawQuery(req.URL.RawQuery) {
			if strings.EqualFold(pair[0], "x-amz-credential") && selectsPair(pair[1]) {
				return nil, errors.New("cannot re-sign a presigned AWS URL")
			}
		}
	}
	for _, key := range unsupportedKeys {
		if key == placeholder {
			return nil, errors.New("unsupported AWS signing headers")
		}
	}
	auths := req.Header.Values("Authorization")
	var selected string
	for _, auth := range auths {
		for _, key := range awsAuthorizationKeys(auth) {
			if key == placeholder {
				selected = auth
				break
			}
		}
		if selected != "" {
			break
		}
	}
	if selected == "" {
		return nil, nil
	}
	if len(auths) != 1 || UnsupportedAWSSigningHeaders(req.Header) {
		return nil, errors.New("ambiguous or unsupported AWS signing request")
	}
	if req.URL != nil {
		for _, pair := range parseProxyRawQuery(req.URL.RawQuery) {
			if strings.EqualFold(pair[0], "x-amz-signature") {
				return nil, errors.New("ambiguous or unsupported AWS signing request")
			}
		}
	}
	fields, ok := strings.CutPrefix(selected, "AWS4-HMAC-SHA256 ")
	if !ok {
		return nil, errors.New("unsupported AWS signature algorithm")
	}
	var scope, signed, signature string
	for _, field := range strings.Split(fields, ",") {
		trimmed := strings.TrimSpace(field)
		name, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			return nil, errors.New("invalid AWS authorization field")
		}
		switch strings.TrimSpace(name) {
		case "Credential":
			if scope != "" {
				return nil, errors.New("invalid AWS authorization field")
			}
			scope = value
		case "SignedHeaders":
			if signed != "" {
				return nil, errors.New("invalid AWS authorization field")
			}
			signed = value
		case "Signature":
			if signature != "" {
				return nil, errors.New("invalid AWS authorization field")
			}
			signature = value
		default:
			return nil, errors.New("invalid AWS authorization field")
		}
	}
	if len(signature) != 64 || !isHexString(signature) {
		return nil, errors.New("invalid AWS signature")
	}
	dates := req.Header.Values("X-Amz-Date")
	if len(dates) == 0 {
		return nil, errors.New("missing AWS signing date")
	}
	date := dates[0]
	signTime, err := time.Parse("20060102T150405Z", date)
	if err != nil {
		return nil, errors.New("invalid AWS signing date")
	}
	if len(dates) != 1 || len(date) != 16 || !isASCIIString(date) {
		return nil, errors.New("AWS credential scope does not match the configured destination")
	}
	expected := []string{placeholder, date[:8], s.config.Region, s.config.Service, "aws4_request"}
	if len(strings.Split(scope, "/")) != len(expected) {
		return nil, errors.New("AWS credential scope does not match the configured destination")
	}
	for i, part := range strings.Split(scope, "/") {
		if part != expected[i] {
			return nil, errors.New("AWS credential scope does not match the configured destination")
		}
	}
	if signed == "" {
		return nil, errors.New("missing AWS signed headers")
	}
	signedHeaders := strings.Split(signed, ";")
	for i, name := range signedHeaders {
		if name == "" || name != strings.ToLower(name) {
			return nil, errors.New("invalid AWS signed header")
		}
		if i > 0 && !(signedHeaders[i-1] < name) {
			return nil, errors.New("invalid AWS signed header list")
		}
	}
	hasHost := false
	hasDate := false
	for _, name := range signedHeaders {
		switch {
		case name == "host":
			hasHost = true
		case name == "x-amz-date":
			hasDate = true
		case name == "x-amz-security-token" && s.credentials.SessionToken != "":
		case name == "authorization":
			return nil, errors.New("invalid AWS signed header list")
		default:
			if len(req.Header.Values(name)) == 0 && !strings.EqualFold(name, "content-length") {
				return nil, errors.New("invalid AWS signed header list")
			}
		}
	}
	if !hasHost || !hasDate {
		return nil, errors.New("invalid AWS signed header list")
	}
	hashes := req.Header.Values("X-Amz-Content-Sha256")
	if len(hashes) > 1 {
		return nil, errors.New("ambiguous AWS payload hash")
	}
	unsignedPayload := len(hashes) == 1 && hashes[0] == "UNSIGNED-PAYLOAD"
	if unsignedPayload && s.config.Service == "aoss" {
		return nil, errors.New("OpenSearch Serverless requires a signed payload")
	}
	return &awsGuestSignature{time: signTime.UTC(), signedHeaders: signedHeaders, unsignedPayload: unsignedPayload}, nil
}

func isASCIIString(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 0x7f {
			return false
		}
	}
	return true
}

// Sign re-signs a buffered request with the configured credentials.
func (s *AWSSigV4Signer) Sign(req *http.Request, guest *awsGuestSignature) error {
	if s == nil || req == nil || guest == nil {
		return errors.New("missing AWS signing state")
	}
	if req.URL == nil || req.URL.Scheme != "https" {
		return errors.New("AWS signing requires the configured HTTPS origin and supported framing")
	}
	port := awsSigV4RequestPort(req)
	if port != s.config.Port {
		return errors.New("AWS signing requires the configured HTTPS origin and supported framing")
	}
	if UnsupportedAWSSigningHeaders(req.Header) {
		return errors.New("AWS signing requires the configured HTTPS origin and supported framing")
	}
	hadPayloadHash := len(req.Header.Values("X-Amz-Content-Sha256")) > 0
	payloadHash := "UNSIGNED-PAYLOAD"
	if guest.unsignedPayload {
		if !proxyUnsignedPayloadLengthMatches(req) {
			return errors.New("ambiguous AWS unsigned payload length")
		}
	} else {
		body, err := s.readRequestBody(req)
		if err != nil {
			return err
		}
		if !proxyContentLengthMatches(req, len(body)) {
			return errors.New("AWS signing requires unambiguous payload framing")
		}
		sum := sha256.Sum256(body)
		payloadHash = hex.EncodeToString(sum[:])
		req.Body = io.NopCloser(strings.NewReader(string(body)))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(body))), nil
		}
		req.ContentLength = int64(len(body))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Upgrade", "Te"} {
		if len(req.Header.Values(name)) > 0 {
			return errors.New("AWS signing cannot forward connection-specific headers")
		}
	}
	for _, name := range []string{"Keep-Alive", "Sec-Websocket-Key", "Transfer-Encoding"} {
		req.Header.Del(name)
	}
	for _, name := range []string{"Authorization", "X-Amz-Date", "X-Amz-Security-Token", "X-Amz-Content-Sha256"} {
		req.Header.Del(name)
	}
	if cookies := req.Header.Values("Cookie"); len(cookies) > 1 {
		req.Header.Del("Cookie")
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	if hadPayloadHash || awsSigV4IsS3Service(s.config.Service) || s.config.Service == "aoss" {
		req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}
	for _, name := range guest.signedHeaders {
		switch name {
		case "host", "x-amz-date", "x-amz-content-sha256":
			continue
		case "x-amz-security-token":
			if s.credentials.SessionToken != "" {
				continue
			}
		}
		if len(req.Header.Values(name)) == 0 && !strings.EqualFold(name, "content-length") {
			return errors.New("AWS signed header was removed")
		}
	}
	timestamp := guest.time.UTC().Format("20060102T150405Z")
	req.Header.Set("X-Amz-Date", timestamp)
	if s.credentials.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.credentials.SessionToken)
	}
	canonicalRequest, err := s.canonicalRequest(req, guest, payloadHash)
	if err != nil {
		return err
	}
	dateStamp := timestamp[:8]
	scope := strings.Join([]string{dateStamp, s.config.Region, s.config.Service, "aws4_request"}, "/")
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + timestamp + "\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	signature := hmacSHA256Hex(awsSigV4SigningKey(s.credentials.SecretAccessKey, dateStamp, s.config.Region, s.config.Service), stringToSign)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.credentials.AccessKeyID+"/"+scope+
		", SignedHeaders="+strings.Join(guest.signedHeaders, ";")+", Signature="+signature)
	return nil
}

func (s *AWSSigV4Signer) readRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	release, err := reserveProxyAWSBuffer(req.Context())
	if err != nil {
		return nil, err
	}
	defer release()
	return collectProxyRequestBodyWithTimeout(req, int64(s.config.MaxBodyBytes), awsSigV4BodyReadTimeout)
}

func (s *AWSSigV4Signer) canonicalRequest(req *http.Request, guest *awsGuestSignature, payloadHash string) (string, error) {
	canonicalURI := awsSigV4CanonicalURI(req, awsSigV4IsS3Service(s.config.Service))
	canonicalQuery := awsSigV4CanonicalQuery(req.URL.RawQuery)
	var builder strings.Builder
	for _, name := range guest.signedHeaders {
		values := req.Header.Values(name)
		if name == "host" {
			values = []string{awsSigV4RequestHost(req)}
		}
		if len(values) == 0 {
			if strings.EqualFold(name, "content-length") && req.ContentLength >= 0 {
				builder.WriteString(name)
				builder.WriteString(":")
				builder.WriteString(fmt.Sprintf("%d", req.ContentLength))
				builder.WriteString("\n")
				continue
			}
			return "", errors.New("AWS signed header was removed")
		}
		normalized := make([]string, len(values))
		for i, value := range values {
			normalized[i] = awsSigV4NormalizeHeaderValue(value)
		}
		builder.WriteString(name)
		builder.WriteString(":")
		builder.WriteString(strings.Join(normalized, ","))
		builder.WriteString("\n")
	}
	return req.Method + "\n" + canonicalURI + "\n" + canonicalQuery + "\n" + builder.String() + "\n" +
		strings.Join(guest.signedHeaders, ";") + "\n" + payloadHash, nil
}

func awsSigV4RequestPort(req *http.Request) int {
	if req.URL == nil {
		return awsSigV4DefaultPort
	}
	if port := req.URL.Port(); port != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil {
			return 0
		}
		return parsed
	}
	return awsSigV4DefaultPort
}

func awsSigV4RequestHost(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}
	if req.URL != nil && req.URL.Host != "" {
		return req.URL.Host
	}
	return req.Header.Get("Host")
}

func awsSigV4IsS3Service(service string) bool {
	switch service {
	case "s3", "s3-object-lambda", "s3-outposts":
		return true
	}
	return false
}

func awsSigV4SigningKey(secret string, dateStamp string, region string, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func hmacSHA256Hex(key []byte, data string) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func awsSigV4NormalizeHeaderValue(value string) string {
	trimmed := strings.TrimSpace(value)
	var builder strings.Builder
	pendingSpace := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if ch == ' ' || ch == '\t' {
			pendingSpace = true
			continue
		}
		if pendingSpace && builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		pendingSpace = false
		builder.WriteByte(ch)
	}
	return builder.String()
}

func awsSigV4CanonicalURI(req *http.Request, singleEncode bool) string {
	path := ""
	if req.URL != nil {
		path = req.URL.EscapedPath()
	}
	if path == "" {
		path = "/"
	}
	if singleEncode {
		return path
	}
	return awsSigV4Encode(removeProxyDotSegments(path), true)
}

func awsSigV4CanonicalQuery(rawQuery string) string {
	pairs := parseProxyRawQuery(rawQuery)
	if len(pairs) == 0 {
		return ""
	}
	encoded := make([][2]string, len(pairs))
	for i, pair := range pairs {
		encoded[i] = [2]string{awsSigV4Encode(pair[0], false), awsSigV4Encode(pair[1], false)}
	}
	sort.Slice(encoded, func(i, j int) bool {
		if encoded[i][0] != encoded[j][0] {
			return encoded[i][0] < encoded[j][0]
		}
		return encoded[i][1] < encoded[j][1]
	})
	parts := make([]string, len(encoded))
	for i, pair := range encoded {
		parts[i] = pair[0] + "=" + pair[1]
	}
	return strings.Join(parts, "&")
}

func awsSigV4Encode(value string, keepSlash bool) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '.' || ch == '_' || ch == '~' || (keepSlash && ch == '/') {
			builder.WriteByte(ch)
			continue
		}
		builder.WriteByte('%')
		builder.WriteString(strings.ToUpper(hex.EncodeToString([]byte{ch})))
	}
	return builder.String()
}

// parseProxyRawQuery splits a raw query leniently, decoding percent escapes and
// plus signs without failing on malformed input.
func parseProxyRawQuery(rawQuery string) [][2]string {
	if rawQuery == "" {
		return nil
	}
	var pairs [][2]string
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		pairs = append(pairs, [2]string{awsSigV4Unescape(key), awsSigV4Unescape(value)})
	}
	return pairs
}

func awsSigV4Unescape(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == '+' {
			builder.WriteByte(' ')
			continue
		}
		if ch == '%' && i+2 < len(value) && isHexString(value[i+1:i+3]) {
			decoded, err := hex.DecodeString(value[i+1 : i+3])
			if err == nil {
				builder.WriteByte(decoded[0])
				i += 2
				continue
			}
		}
		builder.WriteByte(ch)
	}
	return builder.String()
}

func removeProxyDotSegments(path string) string {
	if !strings.Contains(path, ".") {
		return path
	}
	segments := strings.Split(path, "/")
	stack := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case ".":
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, segment)
		}
	}
	result := strings.Join(stack, "/")
	if strings.HasPrefix(path, "/") && !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	if result == "" {
		result = "/"
	}
	return result
}

func reserveProxyAWSBuffer(ctx context.Context) (func(), error) {
	select {
	case proxyAWSBufferSlotsCh <- struct{}{}:
		return func() { <-proxyAWSBufferSlotsCh }, nil
	default:
	}
	timer := time.NewTimer(awsSigV4BufferWaitTime)
	defer timer.Stop()
	select {
	case proxyAWSBufferSlotsCh <- struct{}{}:
		return func() { <-proxyAWSBufferSlotsCh }, nil
	case <-timer.C:
		return nil, errors.New("request buffer capacity unavailable")
	case <-ctx.Done():
		return nil, errors.New("request buffer capacity unavailable")
	}
}

var proxyAWSBufferSlotsCh = make(chan struct{}, awsSigV4BufferSlots)

func collectProxyRequestBodyWithTimeout(req *http.Request, maxBytes int64, timeout time.Duration) ([]byte, error) {
	type result struct {
		body []byte
		err  error
	}
	done := make(chan result, 1)
	body := req.Body
	go func() {
		collected, err := collectProxyBodyFromReader(body, maxBytes)
		done <- result{body: collected, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value := <-done:
		return value.body, value.err
	case <-timer.C:
		if body != nil {
			_ = body.Close()
		}
		return nil, errors.New("AWS signing body read timed out")
	}
}

func collectProxyBodyFromReader(body io.ReadCloser, maxBytes int64) ([]byte, error) {
	if body == nil || body == http.NoBody {
		return nil, nil
	}
	defer body.Close()
	var buffer []byte
	chunk := make([]byte, 32*1024)
	for {
		n, err := body.Read(chunk)
		if n > 0 {
			if int64(len(buffer))+int64(n) > maxBytes {
				return nil, errors.New("request body exceeds configured limit")
			}
			buffer = append(buffer, chunk[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("cannot read request body")
		}
	}
	return buffer, nil
}

func supportedProxyTransferFraming(headers http.Header) bool {
	codings := headers.Values("Transfer-Encoding")
	switch len(codings) {
	case 0:
		return true
	case 1:
		return len(headers.Values("Content-Length")) == 0 &&
			strings.EqualFold(strings.TrimSpace(codings[0]), "chunked")
	default:
		return false
	}
}

func isAmazonEventStream(headers http.Header) bool {
	for _, value := range headers.Values("Content-Type") {
		mime, _, _ := strings.Cut(value, ";")
		if strings.EqualFold(strings.TrimSpace(mime), "application/vnd.amazon.eventstream") {
			return true
		}
	}
	return false
}

func proxyContentLengthMatches(req *http.Request, size int) bool {
	lengths := req.Header.Values("Content-Length")
	if len(lengths) == 0 {
		if req.ContentLength > 0 && int(req.ContentLength) != size {
			return false
		}
		return true
	}
	if len(lengths) != 1 {
		return false
	}
	value := lengths[0]
	if value == "" || !isAllDigits(value) {
		return false
	}
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed == size
}

func proxyUnsignedPayloadLengthMatches(req *http.Request) bool {
	lengths := req.Header.Values("Content-Length")
	switch len(lengths) {
	case 0:
		return true
	case 1:
		value := lengths[0]
		if value == "" || !isAllDigits(value) {
			return false
		}
		if req.ContentLength >= 0 {
			return value == fmt.Sprintf("%d", req.ContentLength)
		}
		return true
	default:
		return false
	}
}

func isAllDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func isManagedMITMCATrustBundlePath(path string) bool {
	if path == "" {
		return false
	}
	directory, err := proxyManagedCADirectory()
	if err != nil {
		return false
	}
	return IsProxyGeneratedTrustBundlePath(path, directory)
}

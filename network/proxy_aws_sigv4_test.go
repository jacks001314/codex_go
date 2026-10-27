package network

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func awsTestSigner(t *testing.T, service string) *AWSSigV4Signer {
	t.Helper()
	config := AWSSigV4Config{
		PlaceholderAccessKeyID: "MASKEDACCESSKEY",
		Region:                 "us-east-1",
		Service:                service,
		AccessKeyID:            ProxySecretSource{Kind: "env_var", Name: "ACCESS_KEY"},
		SecretAccessKey:        ProxySecretSource{Kind: "env_var", Name: "SECRET_KEY"},
		Port:                   443,
		MaxBodyBytes:           1024,
	}
	signer, err := config.Compile(func(name string) (string, bool) {
		switch name {
		case "ACCESS_KEY":
			return "AKIDEXAMPLE", true
		case "SECRET_KEY":
			return "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", true
		case "SESSION_TOKEN":
			return "secret-session-token", true
		}
		return "", false
	}, func(string) (string, error) {
		t.Fatalf("unexpected file lookup")
		return "", nil
	})
	if err != nil {
		t.Fatalf("compile signer: %v", err)
	}
	return signer
}

func awsGuestAuthorization(service string, signedHeaders string) string {
	return fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=MASKEDACCESSKEY/20150830/us-east-1/%s/aws4_request, SignedHeaders=%s, Signature=%064x",
		service, signedHeaders, 1,
	)
}

func awsTestRequest(t *testing.T, method string, rawURL string, body string) *http.Request {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, parsed.String(), reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.URL = parsed
	request.RequestURI = ""
	request.Header.Set("Host", parsed.Host)
	return request
}

func resignAWS(t *testing.T, signer *AWSSigV4Signer, request *http.Request, signedHeaders string) error {
	t.Helper()
	request.Header.Set("Authorization", awsGuestAuthorization(signer.config.Service, signedHeaders))
	request.Header.Set("X-Amz-Date", "20150830T123600Z")
	request.URL.Scheme = "https"
	guest, err := signer.Plan(request, nil)
	if err != nil {
		return err
	}
	if guest == nil {
		t.Fatalf("expected a selected guest signature")
	}
	return signer.Sign(request, guest)
}

func TestAWSSigV4MatchesPublishedIAMVector(t *testing.T) {
	signer := awsTestSigner(t, "iam")
	request := awsTestRequest(t, http.MethodGet, "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", "")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if err := resignAWS(t, signer, request, "content-type;host;x-amz-date"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	const want = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization mismatch:\n got %s\nwant %s", got, want)
	}
	if got := request.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Fatalf("unexpected x-amz-date %q", got)
	}
	if got := request.Header.Get("X-Amz-Content-Sha256"); got != "" {
		t.Fatalf("unexpected payload hash header %q", got)
	}
}

func TestAWSSigV4SignsPayloadAndReplacesGuestHeaders(t *testing.T) {
	signer := awsTestSigner(t, "s3")
	signer.config.SessionToken = nil
	signer.credentials.SessionToken = "secret-session-token"
	request := awsTestRequest(t, http.MethodPut, "https://bucket.s3.amazonaws.com/a%20b?tag=b&tag=a&empty=", "hello")
	request.Header.Set("Cookie", "a=1")
	request.Header.Add("Cookie", "b=2")
	request.Header.Set("X-Amz-Security-Token", "guest-token")
	request.Header.Set("X-Amz-Content-Sha256", "untrusted-guest-hash")
	if err := resignAWS(t, signer, request, "cookie;host;x-amz-date;x-amz-security-token"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got := request.Header.Get("X-Amz-Security-Token"); got != "secret-session-token" {
		t.Fatalf("unexpected session token %q", got)
	}
	if got := request.Header.Get("X-Amz-Content-Sha256"); got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("unexpected payload hash %q", got)
	}
	if got := request.Header.Values("Cookie"); len(got) != 1 || got[0] != "a=1; b=2" {
		t.Fatalf("unexpected cookies %v", got)
	}
	if !strings.Contains(request.Header.Get("Authorization"), "SignedHeaders=cookie;host;x-amz-date;x-amz-security-token") {
		t.Fatalf("unexpected authorization %q", request.Header.Get("Authorization"))
	}
	body, err := collectProxyBodyFromReader(request.Body, 1024)
	if err != nil || string(body) != "hello" {
		t.Fatalf("body not restored: %q %v", body, err)
	}
}

func TestAWSSigV4UnsignedPayloadOmitsBodyRead(t *testing.T) {
	signer := awsTestSigner(t, "s3")
	request := awsTestRequest(t, http.MethodPut, "https://bucket.s3.amazonaws.com/key", "")
	request.Body = &failingReader{}
	request.ContentLength = 0
	request.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	if err := resignAWS(t, signer, request, "host;x-amz-date"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got := request.Header.Get("X-Amz-Content-Sha256"); got != "UNSIGNED-PAYLOAD" {
		t.Fatalf("unexpected payload hash %q", got)
	}
}

type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) { panic("body must not be read") }
func (f *failingReader) Close() error             { return nil }

func TestAWSSigV4RejectsUnsupportedSigningInputs(t *testing.T) {
	signer := awsTestSigner(t, "s3")
	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"presigned query", func(r *http.Request) {
			r.URL.RawQuery = "X-Amz-Credential=MASKEDACCESSKEY%2F20150830&X-Amz-Signature=guest"
		}},
		{"wrong port", func(r *http.Request) { r.URL.Host = "bucket.s3.amazonaws.com:8443" }},
		{"aws-chunked", func(r *http.Request) { r.Header.Set("Content-Encoding", "aws-chunked") }},
		{"streaming hash", func(r *http.Request) { r.Header.Set("X-Amz-Content-Sha256", "STREAMING-AWS4-HMAC-SHA256-PAYLOAD") }},
		{"connection authorization", func(r *http.Request) { r.Header.Set("Connection", "authorization") }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := awsTestRequest(t, http.MethodGet, "https://bucket.s3.amazonaws.com/key", "")
			request.Header.Set("Authorization", awsGuestAuthorization("s3", "host;x-amz-date"))
			request.Header.Set("X-Amz-Date", "20150830T123600Z")
			request.URL.Scheme = "https"
			testCase.mutate(request)
			keys := PreserveSigningHeaders(request.Header)
			guest, err := signer.Plan(request, keys)
			if err == nil && guest != nil {
				err = signer.Sign(request, guest)
			}
			if err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}
}

func TestAWSSigV4CredentialScopeMustMatch(t *testing.T) {
	signer := awsTestSigner(t, "s3")
	base := awsGuestAuthorization("s3", "host;x-amz-date")
	for _, value := range []string{
		strings.Replace(base, "us-east-1", "eu-west-1", 1),
		strings.Replace(base, "/s3/", "/sts/", 1),
		strings.Replace(base, "20150830/", "20150831/", 1),
		strings.Replace(base, "AWS4-HMAC-SHA256", "AWS4-ECDSA-P256-SHA256", 1),
		strings.Replace(base, "x-amz-date,", "x-amz-date;x-missing,", 1),
	} {
		request := awsTestRequest(t, http.MethodGet, "https://bucket.s3.amazonaws.com/key", "")
		request.URL.Scheme = "https"
		request.Header.Set("X-Amz-Date", "20150830T123600Z")
		request.Header.Set("Authorization", value)
		if _, err := signer.Plan(request, nil); err == nil {
			t.Fatalf("expected scope rejection for %q", value)
		}
	}
}

func TestAWSSigV4RequestsBufferCapacityAndTimeout(t *testing.T) {
	if _, err := collectProxyBodyFromReader(http.NoBody, 10); err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
	if _, err := collectProxyBodyFromReader(io_NopCloser("0123456789"), 10); err != nil {
		t.Fatalf("unexpected error at limit: %v", err)
	}
	if _, err := collectProxyBodyFromReader(io_NopCloser("0123456789a"), 10); err == nil {
		t.Fatalf("expected limit rejection")
	}
}

func io_NopCloser(value string) *readCloserFixture {
	return &readCloserFixture{reader: strings.NewReader(value)}
}

type readCloserFixture struct {
	reader *strings.Reader
	closed bool
}

func (f *readCloserFixture) Read(buffer []byte) (int, error) { return f.reader.Read(buffer) }
func (f *readCloserFixture) Close() error {
	f.closed = true
	return nil
}

func TestAWSSigV4MasksLocalEnvironment(t *testing.T) {
	signer := awsTestSigner(t, "s3")
	signer.credentials.SessionToken = "secret-session-token"
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIDEXAMPLE",
		"SESSION_TOKEN":         "secret-session-token",
		"AWS_SECRET_ACCESS_KEY": "unrelated",
	}
	MaskLocalAWSEnv([]*AWSSigV4Signer{signer}, env, "")
	if env["AWS_ACCESS_KEY_ID"] != "MASKEDACCESSKEY" {
		t.Fatalf("access key not masked: %q", env["AWS_ACCESS_KEY_ID"])
	}
	if env["AWS_SESSION_TOKEN"] == "" || env["AWS_SESSION_TOKEN"] == "secret-session-token" {
		t.Fatalf("session token not masked: %q", env["AWS_SESSION_TOKEN"])
	}
	env["AWS_ACCESS_KEY_ID"] = "OTHERKEY"
	env["AWS_SECRET_ACCESS_KEY"] = "unrelated"
	MaskLocalAWSEnv([]*AWSSigV4Signer{signer}, env, "")
	if env["AWS_SECRET_ACCESS_KEY"] != "unrelated" {
		t.Fatalf("unrelated secret was rewritten: %q", env["AWS_SECRET_ACCESS_KEY"])
	}
}

func TestAWSSigV4ConfigValidation(t *testing.T) {
	config := AWSSigV4Config{
		PlaceholderAccessKeyID: "MASKEDACCESSKEY",
		Region:                 "us-east-1",
		Service:                "s3",
		AccessKeyID:            ProxySecretSource{Kind: "env_var", Name: "ACCESS_KEY"},
		SecretAccessKey:        ProxySecretSource{Kind: "env_var", Name: "SECRET_KEY"},
		Port:                   443,
		MaxBodyBytes:           16*1024*1024 + 1,
	}
	if err := config.Validate(); err == nil || err.Error() != "AWS signing body limit exceeds 16 MiB" {
		t.Fatalf("unexpected error: %v", err)
	}
	config.MaxBodyBytes = 1024
	config.Port = 0
	if err := config.Validate(); err == nil || err.Error() != "invalid AWS signing port" {
		t.Fatalf("unexpected error: %v", err)
	}
	config.Port = 443
	if _, err := config.Compile(func(string) (string, bool) { return "", false }, func(string) (string, error) { return "", nil }); err == nil {
		t.Fatalf("expected missing credential rejection")
	}
	signer := awsTestSigner(t, "s3")
	other := signer.config
	other.PlaceholderAccessKeyID = "GUEST" + signer.credentials.AccessKeyID
	otherSigner, err := other.Compile(func(string) (string, bool) { return "other", true }, func(string) (string, error) { return "", nil })
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := ValidateGuestPlaceholders([]*AWSSigV4Signer{signer, otherSigner}); err == nil {
		t.Fatalf("expected overlapping placeholder rejection")
	}
}

var _ = time.Now

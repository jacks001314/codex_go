package appserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Rust #58165: remote plugin bundle URLs can carry a signature token in their
// query string, so no diagnostic may echo the URL. Errors name the host, the
// redirect scheme, or the status instead.
const signedRemotePluginBundleURL = "https://cdn.example.test/plugin-bundle.tar.gz?sig=signed-token"

type remotePluginBundleStubClient struct {
	response *http.Response
	err      error
	requests []*http.Request
}

func (c *remotePluginBundleStubClient) Do(request *http.Request) (*http.Response, error) {
	c.requests = append(c.requests, request)
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}

func TestRemotePluginBundleInvalidURLDiagnosticsHideSignatureTokenLikeRust(t *testing.T) {
	err := validateRemotePluginBundleURL("https://cdn.example.test/%zz?sig=signed-token")
	if err == nil {
		t.Fatal("validateRemotePluginBundleURL() error = nil, want a parse failure")
	}
	if strings.Contains(err.Error(), "signed-token") {
		t.Fatalf("invalid URL error leaked the signature token: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid URL escape") {
		t.Fatalf("invalid URL error = %v, want the parse cause", err)
	}
}

func TestRemotePluginBundleUnsupportedSchemeDiagnosticsHideSignatureTokenLikeRust(t *testing.T) {
	err := validateRemotePluginBundleURL("ftp://cdn.example.test/plugin-bundle.tar.gz?sig=signed-token")
	if err == nil {
		t.Fatal("validateRemotePluginBundleURL() error = nil, want an unsupported scheme")
	}
	if !strings.Contains(err.Error(), "\"ftp\"") {
		t.Fatalf("unsupported scheme error = %v, want the scheme only", err)
	}
	if strings.Contains(err.Error(), "signed-token") {
		t.Fatalf("unsupported scheme error leaked the signature token: %v", err)
	}
}

func TestRemotePluginBundleDownloadRequestDiagnosticsHideSignatureTokenLikeRust(t *testing.T) {
	client := &remotePluginBundleStubClient{err: &url.Error{
		Op:  "Get",
		URL: signedRemotePluginBundleURL,
		Err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused"),
	}}
	_, err := downloadRemotePluginBundle(context.Background(), client, signedRemotePluginBundleURL)
	if err == nil {
		t.Fatal("downloadRemotePluginBundle() error = nil, want a transport failure")
	}
	if strings.Contains(err.Error(), "signed-token") {
		t.Fatalf("download request error leaked the signature token: %v", err)
	}
	if !strings.Contains(err.Error(), "cdn.example.test") {
		t.Fatalf("download request error = %v, want the download host", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("download request error = %v, want the transport cause", err)
	}
	if len(client.requests) != 1 || client.requests[0].URL.RawQuery != "sig=signed-token" {
		t.Fatalf("download request = %+v, want the signed query preserved on the wire", client.requests)
	}
}

func TestRemotePluginBundleFinalURLDiagnosticsHideSignatureTokenLikeRust(t *testing.T) {
	finalURL, err := url.Parse("ftp://cdn.example.test/plugin-bundle.tar.gz?sig=signed-token")
	if err != nil {
		t.Fatalf("parse final URL: %v", err)
	}
	client := &remotePluginBundleStubClient{response: &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(strings.NewReader("bundle")),
		ContentLength: 6,
		Request:       &http.Request{URL: finalURL},
	}}
	_, err = downloadRemotePluginBundle(context.Background(), client, signedRemotePluginBundleURL)
	if err == nil {
		t.Fatal("downloadRemotePluginBundle() error = nil, want a rejected redirect scheme")
	}
	if !strings.Contains(err.Error(), "\"ftp\"") {
		t.Fatalf("final URL error = %v, want the redirect scheme", err)
	}
	if strings.Contains(err.Error(), "signed-token") {
		t.Fatalf("final URL error leaked the signature token: %v", err)
	}
}

func TestRemotePluginBundleStatusDiagnosticsNameHostLikeRust(t *testing.T) {
	requestURL, err := url.Parse(signedRemotePluginBundleURL)
	if err != nil {
		t.Fatalf("parse bundle URL: %v", err)
	}
	client := &remotePluginBundleStubClient{response: &http.Response{
		StatusCode:    http.StatusServiceUnavailable,
		Body:          io.NopCloser(strings.NewReader("upstream unavailable")),
		ContentLength: -1,
		Request:       &http.Request{URL: requestURL},
	}}
	_, err = downloadRemotePluginBundle(context.Background(), client, signedRemotePluginBundleURL)
	if err == nil {
		t.Fatal("downloadRemotePluginBundle() error = nil, want a status failure")
	}
	if !strings.Contains(err.Error(), "cdn.example.test") || !strings.Contains(err.Error(), "503") {
		t.Fatalf("status error = %v, want host and status", err)
	}
	if strings.Contains(err.Error(), "signed-token") {
		t.Fatalf("status error leaked the signature token: %v", err)
	}
}

func TestRemotePluginBundleSignedURLStillDownloadsLikeRust(t *testing.T) {
	requestURL, err := url.Parse(signedRemotePluginBundleURL)
	if err != nil {
		t.Fatalf("parse bundle URL: %v", err)
	}
	client := &remotePluginBundleStubClient{response: &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(strings.NewReader("bundle")),
		ContentLength: 6,
		Request:       &http.Request{URL: requestURL},
	}}
	contents, err := downloadRemotePluginBundle(context.Background(), client, signedRemotePluginBundleURL)
	if err != nil {
		t.Fatalf("downloadRemotePluginBundle() error = %v", err)
	}
	if string(contents) != "bundle" {
		t.Fatalf("downloadRemotePluginBundle() = %q, want the signed bundle bytes", contents)
	}
}

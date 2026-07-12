package execserver

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const customCAHint = "If you set CODEX_CA_CERTIFICATE or SSL_CERT_FILE, ensure it points to a PEM file containing one or more CERTIFICATE blocks, or unset it to use system roots."

func newExecServerHTTPClient() (*http.Client, error) {
	client := &http.Client{Jar: sharedChatGPTCloudflareCookieJar}
	sourceEnv, path := configuredCustomCA()
	if path == "" {
		return client, nil
	}
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read CA certificate file %s selected by %s: %v. %s", path, sourceEnv, err, customCAHint)
	}
	pemData = normalizeTrustedCertificatePEM(pemData)
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if !rootCAs.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("Failed to load CA certificates from %s selected by %s: no usable CERTIFICATE blocks found. %s", path, sourceEnv, customCAHint)
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("Failed to build HTTP client while using CA bundle from %s (%s): default HTTP transport is not configurable", sourceEnv, path)
	}
	transport = transport.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.RootCAs = rootCAs
	}
	client.Transport = transport
	return client, nil
}

func configuredCustomCA() (string, string) {
	for _, name := range []string{"CODEX_CA_CERTIFICATE", "SSL_CERT_FILE"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return name, value
		}
	}
	return "", ""
}

func normalizeTrustedCertificatePEM(data []byte) []byte {
	normalized := bytes.ReplaceAll(data, []byte("-----BEGIN TRUSTED CERTIFICATE-----"), []byte("-----BEGIN CERTIFICATE-----"))
	return bytes.ReplaceAll(normalized, []byte("-----END TRUSTED CERTIFICATE-----"), []byte("-----END CERTIFICATE-----"))
}

package network

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProxyMITMCAConfigParsingAndValidationLikeRust(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	keyPath := filepath.Join(t.TempDir(), "ca.key")
	config, err := ProxyConfigFromConfigValues(map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{
			"mitm": true,
			"mitm_ca": map[string]any{
				"certificate_file": certPath,
				"private_key_file": keyPath,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Network.MITMCA == nil || config.Network.MITMCA.CertificateFile != certPath || config.Network.MITMCA.PrivateKeyFile != keyPath {
		t.Fatalf("MITMCA = %#v", config.Network.MITMCA)
	}
	t.Logf("cert=%q abs=%v parsed=%q", certPath, filepath.IsAbs(certPath), config.Network.MITMCA.CertificateFile)
	if !filepath.IsAbs(config.Network.MITMCA.CertificateFile) {
		t.Fatalf("parsed certificate path is not absolute: %q", config.Network.MITMCA.CertificateFile)
	}
	if err := ValidateProxyMITMHookConfig(*config); err != nil {
		t.Fatal(err)
	}

	_, err = ProxyConfigFromConfigValues(map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{
			"mitm": true,
			"mitm_ca": map[string]any{
				"certificate_file": "ca.pem",
				"private_key_file": "ca.key",
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative MITM CA error = %v", err)
	}

	withoutMITM := *config
	withoutMITM.Network.MITM = false
	if err := ValidateProxyMITMHookConfig(withoutMITM); err == nil || !strings.Contains(err.Error(), "requires network.mitm") {
		t.Fatalf("MITM-disabled CA error = %v", err)
	}
}

func TestExternalProxyMITMCAProducesManagedTrustBundleLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external MITM private keys are intentionally unsupported on Windows")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	certPath, keyPath := writeTestProxyMITMCA(t)
	mitm, err := newProxyMITMRuntime(map[string]string{}, &ProxyMITMCAConfig{CertificateFile: certPath, PrivateKeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	defer mitm.Close()
	if _, err := mitm.TLSConfig("example.com", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mitm.trustBundlePath, filepath.Join("proxy", "external")) {
		t.Fatalf("trust bundle path = %q", mitm.trustBundlePath)
	}
	if _, err := os.Stat(mitm.trustBundlePath); err != nil {
		t.Fatal(err)
	}
}

func TestProxyConfigRejectsUnknownFieldsLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{
			name: "top level",
			values: map[string]any{
				"features": map[string]any{"network_proxy": map[string]any{
					"enabled": true,
					"unknown": "value",
				}},
			},
			want: "unknown field `unknown` in features.network_proxy",
		},
		{
			name: "mitm ca",
			values: map[string]any{
				"features": map[string]any{"network_proxy": map[string]any{
					"mitm": true,
					"mitm_ca": map[string]any{
						"certificate_file": "/tmp/ca.pem",
						"private_key_file": "/tmp/ca.key",
						"extra":            true,
					},
				}},
			},
			want: "unknown field `extra` in features.network_proxy.mitm_ca",
		},
		{
			name: "mitm hook",
			values: map[string]any{
				"features": map[string]any{"network_proxy": map[string]any{
					"mitm_hooks": []any{map[string]any{
						"host":       "example.com",
						"unexpected": "value",
					}},
				}},
			},
			want: "unknown field `unexpected`",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ProxyConfigFromConfigValues(testCase.values)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func writeTestProxyMITMCA(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test MITM CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

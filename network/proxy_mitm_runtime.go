package network

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/gofrs/flock"
	martianmitm "github.com/google/martian/v3/mitm"
)

type proxyManagedCA struct {
	config          *martianmitm.Config
	certificatePath string
	artifactLease   *flock.Flock
	references      int
}

var proxyManagedCAs = struct {
	sync.Mutex
	byDirectory map[string]*proxyManagedCA
}{byDirectory: map[string]*proxyManagedCA{}}

type proxyMITMRuntime struct {
	config           *martianmitm.Config
	managedCA        *proxyManagedCA
	certificatePath  string
	trustBundlePath  string
	startupEnvValues map[string]string
	closeOnce        sync.Once
}

func newProxyMITMRuntime(baseEnv map[string]string, caConfig *ProxyMITMCAConfig) (*proxyMITMRuntime, error) {
	managedCA, err := loadProxyMITMCA(caConfig)
	if err != nil {
		return nil, err
	}
	certificatePath := managedCA.certificatePath
	startupEnvValues := ProxyStartupCAFileEnvValues(baseEnv)
	trustBundle, err := BuildProxyManagedCATrustBundle(certificatePath, startupEnvValues, baseEnv[ProxySSLCertDirEnvKey])
	if err != nil {
		releaseProxyManagedCA(managedCA)
		return nil, fmt.Errorf("build managed MITM CA trust bundle: %w", err)
	}
	trustBundlePath, err := persistProxyMITMCATrustBundle(certificatePath, trustBundle, caConfig != nil)
	if err != nil {
		releaseProxyManagedCA(managedCA)
		return nil, fmt.Errorf("persist managed MITM CA trust bundle: %w", err)
	}
	return &proxyMITMRuntime{
		config:           managedCA.config,
		managedCA:        managedCA,
		certificatePath:  certificatePath,
		trustBundlePath:  trustBundlePath,
		startupEnvValues: startupEnvValues,
	}, nil
}

func loadProxyMITMCA(caConfig *ProxyMITMCAConfig) (*proxyManagedCA, error) {
	if caConfig == nil {
		return loadOrCreateProxyManagedCA()
	}
	certificatePEM, err := readExternalProxyCAFile(caConfig.CertificateFile, false)
	if err != nil {
		return nil, err
	}
	if bytesContains(certificatePEM, []byte("PRIVATE KEY-----")) {
		return nil, fmt.Errorf("network.mitm_ca.certificate_file must not contain a private key")
	}
	privateKeyPEM, err := readExternalProxyCAFile(caConfig.PrivateKeyFile, true)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("network.mitm_ca certificate and private key do not match: %w", err)
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse network.mitm_ca.certificate_file: %w", err)
	}
	config, err := martianmitm.NewConfig(certificate, pair.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("configure external MITM CA: %w", err)
	}
	return &proxyManagedCA{config: config, certificatePath: caConfig.CertificateFile}, nil
}

func readExternalProxyCAFile(path string, private bool) ([]byte, error) {
	const maxBytes = 1024 * 1024
	if runtime.GOOS == "windows" && private {
		return nil, fmt.Errorf("network.mitm_ca.private_key_file is not supported on this platform because private-file permissions cannot be verified")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect external MITM CA file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("external MITM CA file must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("external MITM CA file must be a regular file")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("external MITM CA file exceeds %d bytes", maxBytes)
	}
	if private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("external MITM CA private key must not be accessible by group or other users")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read external MITM CA file: %w", err)
	}
	return contents, nil
}

func persistProxyMITMCATrustBundle(certificatePath string, trustBundle string, external bool) (string, error) {
	if !external {
		return PersistProxyManagedCATrustBundle(certificatePath, trustBundle)
	}
	directory, err := proxyManagedCADirectory()
	if err != nil {
		return "", err
	}
	directory = filepath.Join(directory, "external")
	return PersistProxyCATrustBundleInDirectory(directory, trustBundle)
}

func loadOrCreateProxyManagedCA() (*proxyManagedCA, error) {
	directory, err := proxyManagedCADirectory()
	if err != nil {
		return nil, err
	}
	proxyManagedCAs.Lock()
	defer proxyManagedCAs.Unlock()
	if managed := proxyManagedCAs.byDirectory[directory]; managed != nil {
		managed.references++
		return managed, nil
	}
	managed, err := createProxyManagedCA(directory)
	if err != nil {
		return nil, err
	}
	proxyManagedCAs.byDirectory[directory] = managed
	managed.references = 1
	return managed, nil
}

func releaseProxyManagedCA(managed *proxyManagedCA) {
	if managed == nil {
		return
	}
	proxyManagedCAs.Lock()
	defer proxyManagedCAs.Unlock()
	if managed.references > 0 {
		managed.references--
	}
	if managed.references != 0 {
		return
	}
	directory := filepath.Dir(managed.certificatePath)
	if proxyManagedCAs.byDirectory[directory] == managed {
		delete(proxyManagedCAs.byDirectory, directory)
	}
	if managed.artifactLease != nil {
		_ = managed.artifactLease.Unlock()
	}
}

func createProxyManagedCA(directory string) (*proxyManagedCA, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create managed MITM CA directory: %w", err)
	}
	ca, privateKey, err := martianmitm.NewAuthority("network_proxy MITM CA", "Codex", 365*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate managed MITM CA: %w", err)
	}
	config, err := martianmitm.NewConfig(ca, privateKey)
	if err != nil {
		return nil, fmt.Errorf("configure managed MITM CA: %w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	artifactLock, lockErr := lockProxyManagedCAArtifact(filepath.Join(directory, proxyManagedMITMCAArtifactLock))
	if lockErr != nil {
		slog.Warn("failed to lock managed MITM CA artifacts; skipping pruning", "error", lockErr)
	} else {
		defer artifactLock.Unlock()
	}
	certificatePath, err := persistProxyManagedCACertificateInDirectory(directory, certificatePEM)
	if err != nil {
		return nil, err
	}
	artifactLease, err := lockProxyManagedCAArtifact(proxyManagedCACertificateLockPath(certificatePath))
	if err != nil {
		return nil, fmt.Errorf("lock managed MITM CA certificate: %w", err)
	}
	if artifactLock != nil {
		pruneProxyManagedCAArtifacts(directory, certificatePath)
	}
	return &proxyManagedCA{config: config, certificatePath: certificatePath, artifactLease: artifactLease}, nil
}

func (m *proxyMITMRuntime) TLSConfig(host string, _ *goproxy.ProxyCtx) (*tls.Config, error) {
	if m == nil || m.config == nil {
		return nil, fmt.Errorf("managed MITM is unavailable")
	}
	return m.config.TLSForHost(host), nil
}

func (m *proxyMITMRuntime) ApplyChildEnv(env map[string]string) {
	if m == nil || m.trustBundlePath == "" {
		return
	}
	for _, key := range ProxyCustomCAEnvKeys {
		if current := env[key]; current != "" && current != m.trustBundlePath && m.startupEnvValues[key] != current {
			continue
		}
		env[key] = m.trustBundlePath
	}
}

func (m *proxyMITMRuntime) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() {
		releaseProxyManagedCA(m.managedCA)
	})
}

func persistProxyManagedCACertificate(contents []byte) (string, error) {
	directory, err := proxyManagedCADirectory()
	if err != nil {
		return "", err
	}
	return persistProxyManagedCACertificateInDirectory(directory, contents)
}

func proxyManagedCADirectory() (string, error) {
	home := os.Getenv("GCODE_HOME")
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve CODEX_HOME for managed MITM CA: %w", err)
		}
		home = filepath.Join(userHome, ".gcode")
	}
	return filepath.Join(home, ProxyManagedMITMCADir), nil
}

func persistProxyManagedCACertificateInDirectory(directory string, contents []byte) (string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create managed MITM CA directory: %w", err)
	}
	sum := sha256.Sum256(contents)
	path := filepath.Join(directory, fmt.Sprintf("%s-%x.pem", ProxyManagedMITMCACertPrefix, sum[:]))
	if err := writeAtomicCreateNewOrReuse(path, contents, 0o644); err != nil {
		return "", fmt.Errorf("persist managed MITM CA certificate: %w", err)
	}
	return path, nil
}

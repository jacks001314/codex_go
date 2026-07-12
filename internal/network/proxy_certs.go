package network

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

const (
	ProxyManagedMITMCADir               = "proxy"
	ProxyManagedMITMCACertPrefix        = "ca"
	ProxyManagedMITMCATrustBundlePrefix = "ca-bundle"
	ProxySSLCertDirEnvKey               = "SSL_CERT_DIR"
	proxyManagedMITMCAArtifactLock      = ".artifacts.lock"
)

var ProxyCustomCAEnvKeys = []string{
	"CODEX_CA_CERTIFICATE",
	"SSL_CERT_FILE",
	"REQUESTS_CA_BUNDLE",
	"CURL_CA_BUNDLE",
	"NODE_EXTRA_CA_CERTS",
	"GIT_SSL_CAINFO",
	"PIP_CERT",
	"BUNDLE_SSL_CA_CERT",
	"npm_config_cafile",
	"NPM_CONFIG_CAFILE",
}

type ProxyManagedMITMCATrustBundle struct {
	Path             string
	StartupEnvValues map[string]string
}

func ProxyCAEnvFromMap(env map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range append(append([]string{}, ProxyCustomCAEnvKeys...), ProxySSLCertDirEnvKey) {
		if value, ok := env[key]; ok {
			out[key] = value
		}
	}
	return out
}

func ProxyStartupCAFileEnvValues(env map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range ProxyCustomCAEnvKeys {
		if value := env[key]; value != "" {
			out[key] = value
		}
	}
	return out
}

func BuildProxyManagedCATrustBundle(managedCACertPath string, startupEnvValues map[string]string, startupCertDir string) (string, error) {
	var builder strings.Builder
	if platformRoots, err := loadProxyPlatformRootBundle(); err == nil && platformRoots != "" {
		builder.WriteString(platformRoots)
		if !strings.HasSuffix(platformRoots, "\n") {
			builder.WriteByte('\n')
		}
	}
	appended := map[string]bool{}
	for _, key := range ProxyCustomCAEnvKeys {
		path := startupEnvValues[key]
		if path == "" || appended[path] || IsCurrentProxyGeneratedTrustBundlePath(path, managedCACertPath) {
			continue
		}
		appended[path] = true
		if err := appendPEMFile(&builder, path); err != nil {
			return "", err
		}
	}
	if startupCertDir != "" {
		for _, directory := range filepath.SplitList(startupCertDir) {
			entries, err := os.ReadDir(directory)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					path := filepath.Join(directory, entry.Name())
					if appended[path] {
						continue
					}
					appended[path] = true
					_ = appendPEMFile(&builder, path)
				}
			}
		}
	}
	if err := appendPEMFile(&builder, managedCACertPath); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func PersistProxyManagedCATrustBundle(managedCACertPath string, trustBundle string) (string, error) {
	proxyDir := filepath.Dir(managedCACertPath)
	if err := os.MkdirAll(proxyDir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(trustBundle))
	path := filepath.Join(proxyDir, fmt.Sprintf("%s-%x.pem", ProxyManagedMITMCATrustBundlePrefix, sum[:]))
	if err := writeAtomicCreateNewOrReuse(path, []byte(trustBundle), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func lockProxyManagedCAArtifact(path string) (*flock.Flock, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to use symlink lock file %s", path)
	}
	lock := flock.New(path)
	if err := lock.Lock(); err != nil {
		return nil, err
	}
	return lock, nil
}

func proxyManagedCACertificateLockPath(certificatePath string) string {
	return filepath.Join(filepath.Dir(certificatePath), "."+filepath.Base(certificatePath)+".lock")
}

func pruneProxyManagedCAArtifacts(proxyDir string, activeCertificatePath string) {
	for _, certificatePath := range generatedProxyManagedCAArtifactPaths(proxyDir, ProxyManagedMITMCACertPrefix) {
		if filepath.Clean(certificatePath) == filepath.Clean(activeCertificatePath) {
			continue
		}
		lockPath := proxyManagedCACertificateLockPath(certificatePath)
		if info, err := os.Lstat(lockPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		lease := flock.New(lockPath)
		locked, err := lease.TryLock()
		if err != nil || !locked {
			continue
		}
		removed := os.Remove(certificatePath) == nil || !fileExists(certificatePath)
		_ = lease.Unlock()
		if removed {
			_ = os.Remove(lockPath)
		}
	}

	remaining := make([][]byte, 0)
	for _, certificatePath := range generatedProxyManagedCAArtifactPaths(proxyDir, ProxyManagedMITMCACertPrefix) {
		if certificate, err := os.ReadFile(certificatePath); err == nil && len(certificate) > 0 {
			remaining = append(remaining, certificate)
		}
	}
	for _, bundlePath := range generatedProxyManagedCAArtifactPaths(proxyDir, ProxyManagedMITMCATrustBundlePrefix) {
		contents, err := os.ReadFile(bundlePath)
		if err != nil {
			continue
		}
		keep := false
		for _, certificate := range remaining {
			if bytesContains(contents, certificate) {
				keep = true
				break
			}
		}
		if !keep {
			if err := os.Remove(bundlePath); err != nil && !os.IsNotExist(err) {
				slog.Warn("failed to prune stale managed MITM CA trust bundle", "path", bundlePath, "error", err)
			}
		}
	}
}

func generatedProxyManagedCAArtifactPaths(proxyDir string, prefix string) []string {
	entries, err := os.ReadDir(proxyDir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(proxyDir, entry.Name())
		if isGeneratedManagedCAArtifactPath(path, proxyDir, prefix) {
			out = append(out, path)
		}
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func IsProxyManagedMITMCATrustBundlePath(path string, proxyDir string) bool {
	return IsProxyGeneratedTrustBundlePath(path, proxyDir)
}

func IsCurrentProxyGeneratedTrustBundlePath(path string, managedCACertPath string) bool {
	proxyDir := filepath.Dir(managedCACertPath)
	if IsProxyGeneratedTrustBundlePath(path, proxyDir) {
		return true
	}
	fileName := filepath.Base(path)
	if filepath.Dir(path) != proxyDir ||
		!strings.HasPrefix(fileName, ProxyManagedMITMCATrustBundlePrefix) ||
		!strings.HasSuffix(fileName, ".pem") {
		return false
	}
	trustBundle, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	managedCA, err := os.ReadFile(managedCACertPath)
	if err != nil || len(managedCA) == 0 {
		return false
	}
	return bytesContains(trustBundle, managedCA)
}

func IsProxyGeneratedTrustBundlePath(path string, proxyDir string) bool {
	return isGeneratedManagedCAArtifactPath(path, proxyDir, ProxyManagedMITMCATrustBundlePrefix)
}

func IsProxyGeneratedManagedCAArtifactPath(path string, proxyDir string, prefix string) bool {
	return isGeneratedManagedCAArtifactPath(path, proxyDir, prefix)
}

func PushProxyCertificatePEM(builder *strings.Builder, der []byte) {
	if builder == nil {
		return
	}
	builder.WriteString("-----BEGIN CERTIFICATE-----\n")
	encoded := base64.StdEncoding.EncodeToString(der)
	for len(encoded) > 64 {
		builder.WriteString(encoded[:64])
		builder.WriteByte('\n')
		encoded = encoded[64:]
	}
	if encoded != "" {
		builder.WriteString(encoded)
		builder.WriteByte('\n')
	}
	builder.WriteString("-----END CERTIFICATE-----\n")
}

func appendPEMFile(builder *strings.Builder, path string) error {
	if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	builder.Write(data)
	if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
	return nil
}

func isGeneratedManagedCAArtifactPath(path string, proxyDir string, prefix string) bool {
	fileName := filepath.Base(path)
	expectedHash, ok := strings.CutPrefix(fileName, prefix+"-")
	if !ok {
		return false
	}
	expectedHash, ok = strings.CutSuffix(expectedHash, ".pem")
	if !ok || len(expectedHash) != 64 || !isHexString(expectedHash) {
		return false
	}
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(proxyDir) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]) == strings.ToLower(expectedHash)
}

func writeAtomicCreateNewOrReuse(path string, contents []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to reuse symlink %s", path)
		}
		existing, err := os.ReadFile(path)
		if err == nil && string(existing) == string(contents) {
			return nil
		}
		return fmt.Errorf("refusing to overwrite existing managed CA artifact %s", path)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(contents); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, path); err == nil {
		return os.Remove(tempName)
	}
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(contents) {
			return nil
		}
		return fmt.Errorf("refusing to overwrite existing managed CA artifact %s", path)
	}
	if err := os.Rename(tempName, path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) == string(contents) {
			return nil
		}
		return err
	}
	return nil
}

func isHexString(value string) bool {
	for _, ch := range value {
		if ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F' {
			continue
		}
		return false
	}
	return true
}

func bytesContains(haystack []byte, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if string(haystack[index:index+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

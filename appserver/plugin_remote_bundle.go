package appserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	remotePluginBundleMaxDownloadBytes  int64 = 50 * 1024 * 1024
	remotePluginBundleMaxExtractedBytes int64 = 250 * 1024 * 1024
	remotePluginInstallMetadataFilename       = ".codex-remote-plugin-install.json"
)

func ensureRemoteInstalledPluginCache(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, codexHome string, marketplaceName string, candidate remoteInstalledPlugin) (string, error) {
	name := strings.TrimSpace(candidate.Name)
	pluginBase := filepath.Join(codexHome, "plugins", "cache", marketplaceName, name)
	activeRoot := activeRemotePluginRoot(pluginBase)
	version := ""
	if candidate.Release.Version != nil {
		version = strings.TrimSpace(*candidate.Release.Version)
	}
	if version != "" && filepath.Base(activeRoot) == version {
		if err := validateCachedRemotePluginRoot(activeRoot, name); err != nil {
			return "", err
		}
		if err := writeRemotePluginInstallMetadata(pluginBase, candidate.ID); err != nil {
			return activeRoot, err
		}
		return activeRoot, nil
	}
	if version == "" || candidate.Release.BundleDownloadURL == nil || strings.TrimSpace(*candidate.Release.BundleDownloadURL) == "" {
		if activeRoot == "" {
			return "", nil
		}
		if err := validateCachedRemotePluginRoot(activeRoot, name); err != nil {
			return "", err
		}
		return activeRoot, nil
	}
	if err := validateRemotePluginPathSegment(name); err != nil {
		return activeRoot, err
	}
	if err := validateRemotePluginPathSegment(marketplaceName); err != nil {
		return activeRoot, err
	}
	if err := validateRemotePluginVersion(version); err != nil {
		return activeRoot, err
	}
	bundleURL := strings.TrimSpace(*candidate.Release.BundleDownloadURL)
	if err := validateRemotePluginBundleURL(bundleURL); err != nil {
		return activeRoot, err
	}
	bundle, err := downloadRemotePluginBundle(ctx, client, bundleURL)
	if err != nil {
		return activeRoot, err
	}
	installedRoot, err := installRemotePluginBundle(codexHome, marketplaceName, name, version, candidate.ID, candidate.Release.AppManifest, bundle)
	if err != nil {
		return activeRoot, err
	}
	return installedRoot, nil
}

func activeRemotePluginRoot(pluginBase string) string {
	entries, err := os.ReadDir(pluginBase)
	if err != nil {
		return ""
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || validateRemotePluginVersion(entry.Name()) != nil {
			continue
		}
		versions = append(versions, entry.Name())
	}
	if len(versions) == 0 {
		return ""
	}
	for _, version := range versions {
		if version == "local" {
			return filepath.Join(pluginBase, version)
		}
	}
	sort.SliceStable(versions, func(i int, j int) bool {
		return compareRemotePluginVersions(versions[i], versions[j]) < 0
	})
	return filepath.Join(pluginBase, versions[len(versions)-1])
}

func validateCachedRemotePluginRoot(root string, expectedName string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("remote plugin cache root is empty")
	}
	contents, err := os.ReadFile(filepath.Join(root, ".codex-plugin", "plugin.json"))
	if err != nil {
		return fmt.Errorf("remote plugin cache is missing plugin.json: %w", err)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return fmt.Errorf("remote plugin cache has invalid plugin.json: %w", err)
	}
	if strings.TrimSpace(manifest.Name) != expectedName {
		return fmt.Errorf("plugin.json name %q does not match remote plugin name %q", manifest.Name, expectedName)
	}
	return nil
}

func compareRemotePluginVersions(left string, right string) int {
	leftVersion, leftOK := parseRemoteSemver(left)
	rightVersion, rightOK := parseRemoteSemver(right)
	if !leftOK || !rightOK {
		return strings.Compare(left, right)
	}
	for index := range leftVersion.core {
		if leftVersion.core[index] < rightVersion.core[index] {
			return -1
		}
		if leftVersion.core[index] > rightVersion.core[index] {
			return 1
		}
	}
	if len(leftVersion.pre) == 0 && len(rightVersion.pre) != 0 {
		return 1
	}
	if len(leftVersion.pre) != 0 && len(rightVersion.pre) == 0 {
		return -1
	}
	for index := 0; index < len(leftVersion.pre) && index < len(rightVersion.pre); index++ {
		leftPart, rightPart := leftVersion.pre[index], rightVersion.pre[index]
		leftNumber, leftNumeric := parseRemoteSemverNumber(leftPart)
		rightNumber, rightNumeric := parseRemoteSemverNumber(rightPart)
		switch {
		case leftNumeric && rightNumeric && leftNumber != rightNumber:
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		case leftNumeric != rightNumeric:
			if leftNumeric {
				return -1
			}
			return 1
		case leftPart != rightPart:
			return strings.Compare(leftPart, rightPart)
		}
	}
	if len(leftVersion.pre) < len(rightVersion.pre) {
		return -1
	}
	if len(leftVersion.pre) > len(rightVersion.pre) {
		return 1
	}
	return 0
}

type remoteSemver struct {
	core [3]uint64
	pre  []string
}

func parseRemoteSemver(value string) (remoteSemver, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "v") {
		return remoteSemver{}, false
	}
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(value, "-", 2)
	coreParts := strings.Split(parts[0], ".")
	if len(coreParts) != 3 {
		return remoteSemver{}, false
	}
	var parsed remoteSemver
	for index, part := range coreParts {
		number, ok := parseRemoteSemverNumber(part)
		if !ok || len(part) > 1 && part[0] == '0' {
			return remoteSemver{}, false
		}
		parsed.core[index] = number
	}
	if len(parts) == 2 {
		if parts[1] == "" {
			return remoteSemver{}, false
		}
		parsed.pre = strings.Split(parts[1], ".")
		for _, identifier := range parsed.pre {
			if identifier == "" {
				return remoteSemver{}, false
			}
			for _, character := range identifier {
				if character != '-' && (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
					return remoteSemver{}, false
				}
			}
			if _, numeric := parseRemoteSemverNumber(identifier); numeric && len(identifier) > 1 && identifier[0] == '0' {
				return remoteSemver{}, false
			}
		}
	}
	return parsed, true
}

func parseRemoteSemverNumber(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return number, err == nil
}

func validateRemotePluginPathSegment(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("invalid remote plugin path segment %q", value)
	}
	return nil
}

func validateRemotePluginVersion(version string) error {
	if err := validateRemotePluginPathSegment(version); err != nil {
		return err
	}
	return nil
}

func validateRemotePluginBundleURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid remote plugin bundle URL %q", raw)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && os.Getenv("CODEX_TEST_ALLOW_HTTP_REMOTE_PLUGIN_BUNDLE_DOWNLOADS") == "1" && isLoopbackBundleHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("unsupported remote plugin bundle URL scheme %q", parsed.Scheme)
}

func isLoopbackBundleHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host).IsLoopback()
}

func downloadRemotePluginBundle(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, bundleURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.Request != nil && response.Request.URL != nil {
		if err := validateRemotePluginBundleURL(response.Request.URL.String()); err != nil {
			return nil, err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8*1024))
		return nil, fmt.Errorf("remote plugin bundle download failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if response.ContentLength > remotePluginBundleMaxDownloadBytes {
		return nil, fmt.Errorf("remote plugin bundle exceeds maximum download size")
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, remotePluginBundleMaxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > remotePluginBundleMaxDownloadBytes {
		return nil, fmt.Errorf("remote plugin bundle exceeds maximum download size")
	}
	return contents, nil
}

func installRemotePluginBundle(codexHome string, marketplaceName string, pluginName string, version string, remotePluginID string, appManifest json.RawMessage, bundle []byte) (string, error) {
	stagingRoot := filepath.Join(codexHome, "plugins", ".remote-plugin-install-staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", err
	}
	extractRoot, err := os.MkdirTemp(stagingRoot, "remote-plugin-bundle-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(extractRoot)
	if err := extractRemotePluginBundle(bundle, extractRoot); err != nil {
		return "", err
	}
	manifestPath := filepath.Join(extractRoot, ".codex-plugin", "plugin.json")
	manifestContents, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("remote plugin bundle did not contain .codex-plugin/plugin.json: %w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestContents, &manifest); err != nil {
		return "", fmt.Errorf("invalid remote plugin manifest: %w", err)
	}
	if strings.TrimSpace(fmt.Sprint(manifest["name"])) != pluginName {
		return "", fmt.Errorf("plugin.json name does not match remote plugin name %q", pluginName)
	}
	if marketplaceName == remoteInstalledGlobalMarketplace {
		manifest["version"] = version
		updated, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return "", err
		}
		updated = append(updated, '\n')
		if err := os.WriteFile(manifestPath, updated, 0o600); err != nil {
			return "", err
		}
		if len(appManifest) != 0 && string(appManifest) != "null" {
			appsPath := ".app.json"
			if configured, ok := manifest["apps"].(string); ok && strings.TrimSpace(configured) != "" {
				appsPath = strings.TrimPrefix(filepath.Clean(configured), "."+string(filepath.Separator))
			}
			if filepath.IsAbs(appsPath) || strings.HasPrefix(appsPath, ".."+string(filepath.Separator)) || appsPath == ".." {
				return "", fmt.Errorf("plugin apps path escapes plugin root")
			}
			outputPath := filepath.Join(extractRoot, appsPath)
			if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
				return "", err
			}
			var value any
			if err := json.Unmarshal(appManifest, &value); err != nil {
				return "", err
			}
			contents, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(outputPath, append(contents, '\n'), 0o600); err != nil {
				return "", err
			}
		}
	}

	pluginBase := filepath.Join(codexHome, "plugins", "cache", marketplaceName, pluginName)
	targetRoot := filepath.Join(pluginBase, version)
	if err := os.MkdirAll(pluginBase, 0o755); err != nil {
		return "", err
	}
	backupRoot := ""
	if info, err := os.Stat(targetRoot); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("remote plugin cache target is not a directory")
		}
		backupRoot = targetRoot + ".backup"
		_ = os.RemoveAll(backupRoot)
		if err := os.Rename(targetRoot, backupRoot); err != nil {
			return "", err
		}
	}
	if err := os.Rename(extractRoot, targetRoot); err != nil {
		if backupRoot != "" {
			_ = os.Rename(backupRoot, targetRoot)
		}
		return "", err
	}
	if backupRoot != "" {
		_ = os.RemoveAll(backupRoot)
	}
	for _, entry := range mustReadDir(pluginBase) {
		if entry.IsDir() && entry.Name() != version && validateRemotePluginVersion(entry.Name()) == nil {
			_ = os.RemoveAll(filepath.Join(pluginBase, entry.Name()))
		}
	}
	if err := writeRemotePluginInstallMetadata(pluginBase, remotePluginID); err != nil {
		return targetRoot, err
	}
	return targetRoot, nil
}

func extractRemotePluginBundle(bundle []byte, destination string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	var extracted int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("plugin bundle entry %q escapes extraction root", header.Name)
		}
		outputPath := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(outputPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			extracted += header.Size
			if extracted > remotePluginBundleMaxExtractedBytes {
				return fmt.Errorf("remote plugin bundle exceeds maximum extracted size")
			}
			if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("plugin bundle entry %q is a link", header.Name)
		default:
			return fmt.Errorf("plugin bundle entry %q has unsupported type", header.Name)
		}
	}
}

func writeRemotePluginInstallMetadata(pluginBase string, remotePluginID string) error {
	remotePluginID = strings.TrimSpace(remotePluginID)
	if remotePluginID == "" {
		return fmt.Errorf("remote plugin id must not be blank")
	}
	if err := os.MkdirAll(pluginBase, 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(map[string]any{"schema_version": 1, "remote_plugin_id": remotePluginID}, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(pluginBase, ".remote-plugin-install-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(contents, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	target := filepath.Join(pluginBase, remotePluginInstallMetadataFilename)
	_ = os.Remove(target)
	return os.Rename(temporaryPath, target)
}

func removeStaleRemoteInstalledPluginCaches(codexHome string, installed map[string]map[string]struct{}) error {
	for _, marketplaceName := range []string{
		remoteInstalledGlobalMarketplace,
		remoteInstalledUserMarketplace,
		remoteInstalledWorkspaceMarketplace,
		remoteInstalledWorkspaceSharedMarketplace,
	} {
		marketplaceRoot := filepath.Join(codexHome, "plugins", "cache", marketplaceName)
		entries, err := os.ReadDir(marketplaceRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if _, ok := installed[marketplaceName][entry.Name()]; ok {
				continue
			}
			path := filepath.Join(marketplaceRoot, entry.Name())
			if entry.IsDir() {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			} else if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func mustReadDir(path string) []os.DirEntry {
	entries, _ := os.ReadDir(path)
	return entries
}

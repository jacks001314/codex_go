package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	npmPluginSourceStagingDir        = "plugins/.marketplace-plugin-source-staging"
	npmPluginSourceMaxArchiveBytes   = 50 * 1024 * 1024  // 50 MiB
	npmPluginSourceMaxExtractedBytes = 250 * 1024 * 1024 // 250 MiB
	npmPackageArchiveRoot            = "package"
)

// NpmPluginSourceResult holds the result of materializing an npm plugin source.
type NpmPluginSourceResult struct {
	// PluginRoot is the absolute path to the extracted plugin root directory.
	PluginRoot string
	// StagingDir is the temporary staging directory that must remain alive while the source is used.
	StagingDir string
}

// MaterializeNpmPluginSource downloads and unpacks an npm package as a plugin source.
// It runs `npm pack`, reads the resulting .tgz, unpacks it, and validates the package.
//
// codexHome is the Codex home directory used for staging.
// packageName is the npm package name (e.g., "@acme/plugin").
// version is an optional version specifier.
// registry is an optional npm registry URL.
func MaterializeNpmPluginSource(codexHome string, packageName string, version string, registry string) (*NpmPluginSourceResult, error) {
	return materializeNpmPluginSourceWithCommand(codexHome, packageName, version, registry, npmCommand())
}

func materializeNpmPluginSourceWithCommand(codexHome string, packageName string, version string, registry string, npmCmd string) (*NpmPluginSourceResult, error) {
	codexHome = strings.TrimSpace(codexHome)
	packageName = strings.TrimSpace(packageName)
	if codexHome == "" || packageName == "" {
		return nil, fmt.Errorf("codex home and package name are required for npm plugin source materialization")
	}

	stagingRoot := filepath.Join(codexHome, npmPluginSourceStagingDir)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create marketplace plugin source staging directory %s: %w", stagingRoot, err)
	}

	tempDir, err := os.MkdirTemp(stagingRoot, "marketplace-plugin-source-")
	if err != nil {
		return nil, fmt.Errorf("failed to create marketplace plugin source staging directory in %s: %w", stagingRoot, err)
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			os.RemoveAll(tempDir)
		}
	}()

	if err := packNpmPackage(tempDir, packageName, version, registry, npmCmd); err != nil {
		return nil, err
	}

	archivePath, err := findNpmPackageArchive(tempDir)
	if err != nil {
		return nil, err
	}

	archiveBytes, err := readNpmPackageArchive(archivePath)
	if err != nil {
		return nil, err
	}

	extractionRoot := filepath.Join(tempDir, "extracted")
	if err := UnpackPluginBundleTarGz(archiveBytes, extractionRoot, npmPluginSourceMaxExtractedBytes); err != nil {
		return nil, fmt.Errorf("failed to extract npm plugin package: %w", err)
	}

	pluginRoot := filepath.Join(extractionRoot, npmPackageArchiveRoot)
	if info, err := os.Stat(pluginRoot); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("npm pack completed without creating plugin package directory %s", pluginRoot)
	}

	if err := validateNpmPackageMetadata(pluginRoot, packageName); err != nil {
		return nil, err
	}

	cleanupOnError = false
	return &NpmPluginSourceResult{
		PluginRoot: pluginRoot,
		StagingDir: tempDir,
	}, nil
}

func packNpmPackage(destination string, packageName string, version string, registry string, npmCmd string) error {
	packageSpec := packageName
	if strings.TrimSpace(version) != "" {
		packageSpec = packageName + "@" + strings.TrimSpace(version)
	}

	args := []string{"pack", "--ignore-scripts", "--pack-destination", destination}
	if strings.TrimSpace(registry) != "" {
		args = append(args, "--registry", strings.TrimSpace(registry))
	}
	args = append(args, "--", packageSpec)

	cmd := exec.Command(npmCmd, args...)
	cmd.Dir = destination
	cmd.Env = append(os.Environ(), "COREPACK_ENABLE_STRICT=0")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm pack failed with status: %w\nstdout:\n%s\nstderr:\n%s",
			err, strings.TrimSpace(string(output)), "")
	}
	return nil
}

func findNpmPackageArchive(destination string) (string, error) {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return "", fmt.Errorf("failed to read npm pack destination: %w", err)
	}

	var archives []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".tgz" {
			archives = append(archives, filepath.Join(destination, entry.Name()))
		}
	}
	if len(archives) != 1 {
		return "", fmt.Errorf("npm pack completed with %d package archives; expected exactly one", len(archives))
	}
	return archives[0], nil
}

func readNpmPackageArchive(archivePath string) ([]byte, error) {
	info, err := os.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect npm package archive: %w", err)
	}
	if info.Size() > npmPluginSourceMaxArchiveBytes {
		return nil, fmt.Errorf("npm package archive is %d bytes, exceeding maximum size of %d bytes",
			info.Size(), npmPluginSourceMaxArchiveBytes)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read npm package archive: %w", err)
	}
	return data, nil
}

func validateNpmPackageMetadata(pluginRoot string, packageName string) error {
	packageJSONPath := filepath.Join(pluginRoot, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read npm plugin package metadata %s: %w", packageJSONPath, err)
	}
	var metadata struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("failed to parse npm plugin package metadata %s: %w", packageJSONPath, err)
	}
	if metadata.Name != packageName {
		return fmt.Errorf("npm plugin package name %q does not match requested package %q", metadata.Name, packageName)
	}
	return nil
}

// npmCommand returns the npm command appropriate for the current platform.
func npmCommand() string {
	if _, err := exec.LookPath("npm.cmd"); err == nil {
		return "npm.cmd"
	}
	return "npm"
}

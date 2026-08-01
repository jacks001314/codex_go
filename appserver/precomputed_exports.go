package appserver

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const generatedTypeScriptHeader = "// GENERATED CODE! DO NOT MODIFY BY HAND!\n\n"

//go:embed schema/precomputed/app-server-exports-stable.json.zst
var stablePrecomputedExports []byte

//go:embed schema/precomputed/app-server-exports-experimental.json.zst
var experimentalPrecomputedExports []byte

type precomputedProtocolExports struct {
	TypeScript         map[string]string `json:"typescript"`
	JSONSchema         map[string]string `json:"json_schema"`
	InternalJSONSchema map[string]string `json:"internal_json_schema"`
}

type GenerateTypeScriptOptions struct {
	GenerateIndices bool
	EnsureHeaders   bool
	RunPrettier     bool
	Experimental    bool
}

func DefaultGenerateTypeScriptOptions() GenerateTypeScriptOptions {
	return GenerateTypeScriptOptions{
		GenerateIndices: true,
		EnsureHeaders:   true,
		RunPrettier:     true,
	}
}

func loadPrecomputedProtocolExports(experimental bool) (*precomputedProtocolExports, error) {
	compressed := stablePrecomputedExports
	if experimental {
		compressed = experimentalPrecomputedExports
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("decompress precomputed app-server protocol exports: %w", err)
	}
	data, err := decoder.DecodeAll(compressed, nil)
	decoder.Close()
	if err != nil {
		return nil, fmt.Errorf("decompress precomputed app-server protocol exports: %w", err)
	}
	var exports precomputedProtocolExports
	if err := json.Unmarshal(data, &exports); err != nil {
		return nil, fmt.Errorf("decode precomputed app-server protocol exports: %w", err)
	}
	if exports.TypeScript == nil || exports.JSONSchema == nil || exports.InternalJSONSchema == nil {
		return nil, fmt.Errorf("decode precomputed app-server protocol exports: missing export set")
	}
	return &exports, nil
}

func generatePrecomputedTypeScript(outDir string, prettier string, options GenerateTypeScriptOptions) error {
	exports, err := loadPrecomputedProtocolExports(options.Experimental)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(exports.TypeScript))
	for _, relativePath := range sortedExportPaths(exports.TypeScript) {
		isIndex := filepath.Base(filepath.FromSlash(relativePath)) == "index.ts"
		if isIndex && !options.GenerateIndices {
			continue
		}
		contents := exports.TypeScript[relativePath]
		if !options.EnsureHeaders && !isIndex {
			contents = strings.TrimPrefix(contents, generatedTypeScriptHeader)
		}
		path, err := writePrecomputedExport(outDir, relativePath, contents)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if options.RunPrettier && strings.TrimSpace(prettier) != "" && len(paths) > 0 {
		args := append([]string{"--write", "--log-level", "warn"}, paths...)
		command := exec.Command(prettier, args...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("Prettier failed: %w", err)
		}
	}
	return trimTrailingWhitespaceInTypeScriptFiles(paths)
}

func generatePrecomputedJSON(outDir string, experimental bool, internal bool) error {
	// Rust's internal schema export is intentionally stable-only.
	exports, err := loadPrecomputedProtocolExports(experimental && !internal)
	if err != nil {
		return err
	}
	files := exports.JSONSchema
	if internal {
		files = exports.InternalJSONSchema
	}
	for _, relativePath := range sortedExportPaths(files) {
		if _, err := writePrecomputedExport(outDir, relativePath, files[relativePath]); err != nil {
			return err
		}
	}
	return nil
}

func sortedExportPaths(exports map[string]string) []string {
	paths := make([]string, 0, len(exports))
	for path := range exports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func writePrecomputedExport(outDir string, relativePath string, contents string) (string, error) {
	nativePath, err := validatedPrecomputedExportPath(relativePath)
	if err != nil {
		return "", err
	}
	outputPath := filepath.Join(outDir, nativePath)
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("failed to create %s: %w", filepath.Dir(outputPath), err)
	}
	if err := os.WriteFile(outputPath, []byte(contents), 0o644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	return outputPath, nil
}

func validatedPrecomputedExportPath(relativePath string) (string, error) {
	if relativePath == "" || strings.Contains(relativePath, "\\") || strings.HasPrefix(relativePath, "/") {
		return "", fmt.Errorf("invalid precomputed export path: %s", relativePath)
	}
	parts := strings.Split(relativePath, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid precomputed export path: %s", relativePath)
		}
	}
	nativePath := filepath.FromSlash(relativePath)
	if filepath.IsAbs(nativePath) || filepath.VolumeName(nativePath) != "" {
		return "", fmt.Errorf("invalid precomputed export path: %s", relativePath)
	}
	return nativePath, nil
}

func trimTrailingWhitespaceInTypeScriptFiles(paths []string) error {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		content := string(data)
		trimmed := trimTrailingLineWhitespace(content)
		if trimmed == content {
			continue
		}
		if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
	}
	return nil
}

func trimTrailingLineWhitespace(content string) string {
	var trimmed strings.Builder
	trimmed.Grow(len(content))
	for len(content) > 0 {
		line := content
		newline := ""
		if index := strings.IndexByte(content, '\n'); index >= 0 {
			line = content[:index]
			newline = "\n"
			content = content[index+1:]
		} else {
			content = ""
		}
		trimmed.WriteString(strings.TrimRight(line, " \t"))
		trimmed.WriteString(newline)
	}
	return trimmed.String()
}

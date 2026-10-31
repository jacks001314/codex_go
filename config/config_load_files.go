package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ValidateLoadedConfigFiles replays the file reads Config::load performs after
// the typed pass: Rust resolves `model_instructions_file`,
// `experimental_compact_prompt_file` and `model_catalog_json` against the codex
// home, and a document naming a file that cannot be read fails the load.
func ValidateLoadedConfigFiles(values map[string]any, codexHome string) error {
	for _, field := range []struct {
		key     string
		subject string
	}{
		{"model_instructions_file", "model instructions file"},
		{"experimental_compact_prompt_file", "experimental compact prompt file"},
	} {
		raw, ok := values[field.key].(string)
		if !ok {
			continue
		}
		path := ResolveConfigFilePath(raw, codexHome)
		data, err := readConfigFilePath(path)
		if err != nil {
			return fmt.Errorf("failed to read %s %s: %s", field.subject, path, configOSErrorText(err))
		}
		if err := configValidateUTF8(data); err != nil {
			return fmt.Errorf("failed to read %s %s: %s", field.subject, path, err.Error())
		}
		if strings.TrimSpace(string(data)) == "" {
			return fmt.Errorf("%s is empty: %s", field.subject, path)
		}
	}
	raw, ok := values["model_catalog_json"].(string)
	if !ok {
		return nil
	}
	path := ResolveConfigFilePath(raw, codexHome)
	data, err := readConfigFilePath(path)
	if err != nil {
		// load_catalog_json propagates the io error unchanged.
		return fmt.Errorf("%s", configOSErrorText(err))
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("stream did not contain valid UTF-8")
	}
	models, err := validateModelCatalogJSON(data)
	if err != nil {
		return fmt.Errorf("failed to parse model_catalog_json path `%s` as JSON: %s", path, err.Error())
	}
	if models == 0 {
		return fmt.Errorf("model_catalog_json path `%s` must contain at least one model", path)
	}
	return nil
}

// ResolveConfigFilePath resolves a path-valued setting the way the serde
// AbsolutePathBuf guard does: `~` expands to the user's home and a relative
// path is joined to the codex home.
func ResolveConfigFilePath(value string, codexHome string) string {
	path := value
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
			return filepath.Clean(path)
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(codexHome, path)
	}
	return filepath.Clean(path)
}

// readConfigFilePath reads a path setting. Rust opens the target with read
// access only, so a directory reports ERROR_ACCESS_DENIED instead of the
// handle error Go's directory-aware open produces.
func readConfigFilePath(path string) ([]byte, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, &os.PathError{Op: "open", Path: path, Err: configAccessDeniedErrno()}
	}
	return os.ReadFile(path)
}

// configValidateUTF8 renders Rust's Utf8Error, which names the invalid
// sequence length and the byte index.
func configValidateUTF8(data []byte) error {
	if utf8.Valid(data) {
		return nil
	}
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || size != 1 {
			i += size
			continue
		}
		length := 1
		switch lead := data[i]; {
		case lead >= 0xc2 && lead <= 0xdf:
			length = 2
		case lead >= 0xe0 && lead <= 0xef:
			length = 3
		case lead >= 0xf0 && lead <= 0xf4:
			length = 4
		}
		if remaining := len(data) - i; remaining < length {
			length = remaining
		}
		return fmt.Errorf("invalid utf-8 sequence of %d bytes from index %d", length, i)
	}
	return fmt.Errorf("invalid utf-8 sequence")
}

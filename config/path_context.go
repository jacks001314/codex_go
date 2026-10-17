package config

import (
	"errors"
	"os"
	"runtime"

	"codex_go/utils"
)

// ConfigPathContext carries the path convention, base directory, and home
// directory of the environment that owns a configuration layer or request.
// Supplying these facts keeps path resolution independent of the host that
// happens to parse the configuration.
type ConfigPathContext struct {
	convention  utils.PathConvention
	baseDir     *utils.PathURI
	userHomeDir *utils.PathURI
}

// NewConfigPathContext builds a context from URI or absolute native path text.
// Empty directory strings leave the corresponding fact unset.
func NewConfigPathContext(convention utils.PathConvention, baseDir string, userHomeDir string) (*ConfigPathContext, error) {
	base, err := configPathFactURI(baseDir)
	if err != nil {
		return nil, err
	}
	home, err := configPathFactURI(userHomeDir)
	if err != nil {
		return nil, err
	}
	return NewConfigPathContextFromURIs(convention, base, home), nil
}

// NewConfigPathContextFromURIs builds a context from already parsed path facts.
func NewConfigPathContextFromURIs(convention utils.PathConvention, baseDir *utils.PathURI, userHomeDir *utils.PathURI) *ConfigPathContext {
	return &ConfigPathContext{
		convention:  convention,
		baseDir:     baseDir,
		userHomeDir: userHomeDir,
	}
}

// NewNativeConfigPathContext builds a native context rooted at cwd. An empty
// cwd falls back to the process working directory; the process home directory
// is used when available.
func NewNativeConfigPathContext(cwd string) (*ConfigPathContext, error) {
	if cwd == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cwd = workingDir
	}
	base, err := utils.FromHostNativePath(cwd)
	if err != nil {
		inferred, ok := utils.InferredPathURI(cwd)
		if !ok || inferred == nil {
			return nil, err
		}
		base = inferred
	}
	convention, ok := base.InferConvention()
	if !ok {
		return nil, errors.New("config path context requires a base with a known path convention")
	}
	var home *utils.PathURI
	if userHome, err := os.UserHomeDir(); err == nil {
		home, _ = configPathFactURI(userHome)
	}
	return NewConfigPathContextFromURIs(convention, base, home), nil
}

// NewHostConfigPathContext builds a host-native context. Unlike
// NewNativeConfigPathContext, an empty base leaves the base unset so relative
// path validation can fail instead of implicitly using the process cwd.
func NewHostConfigPathContext(baseDir string) (*ConfigPathContext, error) {
	convention := utils.ConventionPosix
	if runtime.GOOS == "windows" {
		convention = utils.ConventionWindows
	}
	base, err := configPathFactURI(baseDir)
	if err != nil {
		return nil, err
	}
	var home *utils.PathURI
	if userHome, err := os.UserHomeDir(); err == nil {
		home, _ = configPathFactURI(userHome)
	}
	return NewConfigPathContextFromURIs(convention, base, home), nil
}

// Convention returns the path grammar supplied by the owning environment.
func (c *ConfigPathContext) Convention() utils.PathConvention {
	if c == nil {
		return utils.PathConvention("")
	}
	return c.convention
}

// ResolvePath resolves configuration text using this context's base and home.
func (c *ConfigPathContext) ResolvePath(input string) (*utils.PathURI, error) {
	if c == nil {
		return nil, errors.New("config path context is nil")
	}
	return utils.ResolveConfigPathURI(input, c.convention, c.baseDir, c.userHomeDir)
}

// ResolvePathString resolves configuration text and renders it using the
// context's native path convention.
func (c *ConfigPathContext) ResolvePathString(input string) (string, error) {
	resolved, err := c.ResolvePath(input)
	if err != nil {
		return "", err
	}
	return resolved.ToConfigPathString(c.convention)
}

// ResolveAgainst resolves input against an explicit base while retaining this
// context's grammar and home directory.
func (c *ConfigPathContext) ResolveAgainst(input string, base *utils.PathURI) (*utils.PathURI, error) {
	if c == nil {
		return nil, errors.New("config path context is nil")
	}
	return utils.ResolveConfigPathURI(input, c.convention, base, c.userHomeDir)
}

func configPathFactURI(value string) (*utils.PathURI, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, ok := utils.InferredPathURI(value); ok {
		return parsed, nil
	}
	return nil, &utils.ParseError{Reason: "config path fact must be an absolute path or file URI", Path: value}
}

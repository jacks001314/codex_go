package plugin

import (
	"context"
	"fmt"
	"strings"
)

// PluginResourceLocator represents a plugin resource paired with the environment
// that owns its filesystem. This binds a resource path to a specific authority/source.
type PluginResourceLocator struct {
	// EnvironmentID identifies the environment whose filesystem owns the resource.
	EnvironmentID string
	// Path is the resource path within that environment.
	Path string
}

// ResolvedPluginLocation describes the authority-bound location of a resolved plugin package.
type ResolvedPluginLocation struct {
	// EnvironmentID identifies the environment whose filesystem owns the package.
	EnvironmentID string
	// Root is the package root within that environment.
	Root string
}

// ResolvedPlugin is an inert plugin descriptor whose resources retain their source authority.
type ResolvedPlugin struct {
	SelectedRootID string
	Location       ResolvedPluginLocation
	ManifestPath   PluginResourceLocator
	Manifest       PluginManifestWithResources
}

// PluginManifestWithResources is a plugin manifest whose resource fields carry
// authority-bound locators. Mirrors the Rust PluginManifest<PluginResourceLocator>.
type PluginManifestWithResources struct {
	Name        string
	Version     string
	Description string
	Keywords    []string
	Paths       PluginManifestPathsWithResources
	Interface   *PluginManifestInterfaceWithResources
}

// PluginManifestPathsWithResources holds resource paths for plugin components.
type PluginManifestPathsWithResources struct {
	Skills     []PluginResourceLocator
	MCPServers *PluginManifestMCPServersWithResources
	Apps       *PluginResourceLocator
	Hooks      *PluginManifestHooksWithResources
}

// PluginManifestMCPServersWithResources represents an MCP server declaration.
type PluginManifestMCPServersWithResources struct {
	IsPath bool
	Path   PluginResourceLocator
	Object string
}

// PluginManifestHooksWithResources represents hook declarations.
type PluginManifestHooksWithResources struct {
	IsInline bool
	Paths    []PluginResourceLocator
	Inline   []string
}

// PluginManifestInterfaceWithResources holds UI-facing plugin metadata with
// authority-bound resources.
type PluginManifestInterfaceWithResources struct {
	DisplayName      string
	ShortDescription string
	LongDescription  string
	DeveloperName    string
	Category         string
	Capabilities     []string
	WebsiteURL       string
	PrivacyPolicyURL string
	TermsOfServiceURL string
	DefaultPrompt    []string
	BrandColor       string
	ComposerIcon     *PluginResourceLocator
	Logo             *PluginResourceLocator
	LogoDark         *PluginResourceLocator
	Screenshots      []PluginResourceLocator
}

// ResolvedPluginError represents an error when constructing a resolved plugin.
type ResolvedPluginError struct {
	message string
}

func (e *ResolvedPluginError) Error() string {
	return e.message
}

func newResolvedPluginError(root, path string) *ResolvedPluginError {
	return &ResolvedPluginError{
		message: fmt.Sprintf("plugin resource path %q is outside package root %q", path, root),
	}
}

// NewResolvedPluginFromEnvironment creates an environment-owned descriptor from a validated plugin manifest.
func NewResolvedPluginFromEnvironment(
	selectedRootID string,
	environmentID string,
	root string,
	manifestPath string,
	manifest PluginManifestWithResources,
) (*ResolvedPlugin, error) {
	manifestLoc, err := environmentResource(environmentID, root, manifestPath)
	if err != nil {
		return nil, err
	}

	// Map all resource paths in the manifest
	mappedManifest, err := mapManifestResources(manifest, func(path string) (PluginResourceLocator, error) {
		return environmentResource(environmentID, root, path)
	})
	if err != nil {
		return nil, err
	}

	return &ResolvedPlugin{
		SelectedRootID: selectedRootID,
		Location: ResolvedPluginLocation{
			EnvironmentID: environmentID,
			Root:          root,
		},
		ManifestPath: manifestLoc,
		Manifest:     mappedManifest,
	}, nil
}

// SelectedRootID returns the opaque ID supplied for the selected capability root.
func (r *ResolvedPlugin) GetSelectedRootID() string {
	return r.SelectedRootID
}

// PluginProvider abstracts resolution of plugin packages from capability roots.
//
// Implementations must perform all filesystem access through the authority
// named by the selected root.
type PluginProvider interface {
	// Resolve resolves a selected capability root into a resolved plugin descriptor.
	// Returns nil if the root contains no plugin manifest and may be handled as
	// another standalone capability.
	Resolve(ctx context.Context, rootID string) (*ResolvedPlugin, error)
}

// environmentResource constructs a PluginResourceLocator for a resource.
// Returns an error if the path is outside the root.
func environmentResource(environmentID string, root string, path string) (PluginResourceLocator, error) {
	if !strings.HasPrefix(path, root) {
		return PluginResourceLocator{}, newResolvedPluginError(root, path)
	}
	return PluginResourceLocator{
		EnvironmentID: environmentID,
		Path:          path,
	}, nil
}

func mapManifestResources(manifest PluginManifestWithResources, mapper func(string) (PluginResourceLocator, error)) (PluginManifestWithResources, error) {
	out := manifest

	// Map skills
	out.Paths.Skills = nil
	for _, skill := range manifest.Paths.Skills {
		loc, err := mapper(skill.Path)
		if err != nil {
			return PluginManifestWithResources{}, err
		}
		out.Paths.Skills = append(out.Paths.Skills, loc)
	}

	// Map MCP servers
	if manifest.Paths.MCPServers != nil && manifest.Paths.MCPServers.IsPath {
		loc, err := mapper(manifest.Paths.MCPServers.Path.Path)
		if err != nil {
			return PluginManifestWithResources{}, err
		}
		out.Paths.MCPServers = &PluginManifestMCPServersWithResources{
			IsPath: true,
			Path:   loc,
		}
	} else {
		out.Paths.MCPServers = manifest.Paths.MCPServers
	}

	// Map apps
	if manifest.Paths.Apps != nil {
		loc, err := mapper(manifest.Paths.Apps.Path)
		if err != nil {
			return PluginManifestWithResources{}, err
		}
		out.Paths.Apps = &PluginResourceLocator{
			EnvironmentID: loc.EnvironmentID,
			Path:          loc.Path,
		}
	}

	// Map hooks
	if manifest.Paths.Hooks != nil && !manifest.Paths.Hooks.IsInline {
		out.Paths.Hooks = &PluginManifestHooksWithResources{
			IsInline: false,
		}
		for _, hook := range manifest.Paths.Hooks.Paths {
			loc, err := mapper(hook.Path)
			if err != nil {
				return PluginManifestWithResources{}, err
			}
			out.Paths.Hooks.Paths = append(out.Paths.Hooks.Paths, loc)
		}
	} else {
		out.Paths.Hooks = manifest.Paths.Hooks
	}

	// Map interface resources
	if manifest.Interface != nil {
		iface := *manifest.Interface
		if iface.ComposerIcon != nil {
			loc, err := mapper(iface.ComposerIcon.Path)
			if err != nil {
				return PluginManifestWithResources{}, err
			}
			iface.ComposerIcon = &loc
		}
		if iface.Logo != nil {
			loc, err := mapper(iface.Logo.Path)
			if err != nil {
				return PluginManifestWithResources{}, err
			}
			iface.Logo = &loc
		}
		if iface.LogoDark != nil {
			loc, err := mapper(iface.LogoDark.Path)
			if err != nil {
				return PluginManifestWithResources{}, err
			}
			iface.LogoDark = &loc
		}
		iface.Screenshots = nil
		for _, screenshot := range manifest.Interface.Screenshots {
			loc, err := mapper(screenshot.Path)
			if err != nil {
				return PluginManifestWithResources{}, err
			}
			iface.Screenshots = append(iface.Screenshots, loc)
		}
		out.Interface = &iface
	}

	return out, nil
}

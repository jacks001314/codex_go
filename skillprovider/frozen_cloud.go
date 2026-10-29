package skillprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	// FrozenCloudSnapshotMaxBytes caps the snapshot file (Rust
	// FrozenCloudSkillProvider::MAX_SNAPSHOT_BYTES).
	FrozenCloudSnapshotMaxBytes = 64 << 20
	// FrozenCloudResourceMaxBytes caps one declared resource's contents
	// (Rust MAX_SKILL_RESOURCE_CONTENT_BYTES).
	FrozenCloudResourceMaxBytes = 1 << 20
	// FrozenCloudServerName is the Codex Apps authority that owns a frozen
	// cloud skill snapshot (Rust CODEX_APPS_MCP_SERVER_NAME).
	FrozenCloudServerName = "codex_apps"
)

// frozenCloudSnapshot is the on-disk shape of a host-owned skill snapshot.
// Every field is a pointer so a missing key is rejected like serde's required
// fields, and unknown keys are rejected with DisallowUnknownFields.
type frozenCloudSnapshot struct {
	Skills *[]frozenCloudSkill `json:"skills"`
}

type frozenCloudSkill struct {
	Package       *string            `json:"package"`
	Name          *string            `json:"name"`
	Description   *string            `json:"description"`
	AliasRoot     *string            `json:"alias_root"`
	MainResource  *string            `json:"main_resource"`
	PromptVisible *bool              `json:"prompt_visible"`
	Resources     *map[string]string `json:"resources"`
}

// FrozenCloudProvider supplies an immutable cloud skill catalog and resource
// contents without contacting a skill service (Rust #55631). It never reads
// the filesystem after construction.
type FrozenCloudProvider struct {
	catalog   Catalog
	resources map[string]map[string]string
}

// LoadFrozenCloudProvider reads a snapshot once. The file is bounded before
// decoding so an oversized source cannot exhaust memory.
func LoadFrozenCloudProvider(path string) (*FrozenCloudProvider, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read skill snapshot: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, FrozenCloudSnapshotMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read skill snapshot: %w", err)
	}
	return FrozenCloudProviderFromJSON(contents)
}

// FrozenCloudProviderFromJSON validates and freezes a snapshot. It rejects
// missing main resources, duplicate packages, and resources outside their
// package.
func FrozenCloudProviderFromJSON(contents []byte) (*FrozenCloudProvider, error) {
	if len(contents) > FrozenCloudSnapshotMaxBytes {
		return nil, errors.New("skill snapshot exceeds 64 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var snapshot frozenCloudSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("invalid skill snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid skill snapshot: trailing data")
	}
	if snapshot.Skills == nil {
		return nil, errors.New("invalid skill snapshot: missing skills")
	}
	provider := &FrozenCloudProvider{resources: map[string]map[string]string{}}
	for _, skill := range *snapshot.Skills {
		entry, resources, err := frozenCloudSkillEntry(skill)
		if err != nil {
			return nil, err
		}
		if _, duplicate := provider.resources[entry.PackageID]; duplicate {
			return nil, errors.New("duplicate skill package")
		}
		provider.catalog.Entries = append(provider.catalog.Entries, entry)
		provider.resources[entry.PackageID] = resources
	}
	// Rust sorts the list result by package id, so the frozen catalog renders
	// with the same alias order regardless of snapshot ordering.
	sort.SliceStable(provider.catalog.Entries, func(i, j int) bool {
		return provider.catalog.Entries[i].PackageID < provider.catalog.Entries[j].PackageID
	})
	return provider, nil
}

func frozenCloudSkillEntry(skill frozenCloudSkill) (CatalogEntry, map[string]string, error) {
	invalid := errors.New("invalid skill package or resource")
	if skill.Package == nil || skill.Name == nil || skill.Description == nil ||
		skill.AliasRoot == nil || skill.MainResource == nil || skill.Resources == nil {
		return CatalogEntry{}, nil, invalid
	}
	packageID := *skill.Package
	resources := *skill.Resources
	if !strings.HasPrefix(*skill.AliasRoot, "skill://") ||
		!frozenResourceWithin(*skill.AliasRoot, packageID, false) ||
		strings.TrimSpace(*skill.Name) == "" {
		return CatalogEntry{}, nil, invalid
	}
	if _, ok := resources[*skill.MainResource]; !ok {
		return CatalogEntry{}, nil, invalid
	}
	for resource, contents := range resources {
		if !frozenResourceWithin(packageID, resource, true) || len(contents) > FrozenCloudResourceMaxBytes {
			return CatalogEntry{}, nil, invalid
		}
	}
	entry := CatalogEntry{
		PackageID:     packageID,
		Authority:     Authority{Kind: SourceCloud, ID: FrozenCloudServerName},
		Name:          *skill.Name,
		Description:   *skill.Description,
		MainResource:  *skill.MainResource,
		DisplayPath:   packageID,
		AliasRoot:     *skill.AliasRoot,
		Enabled:       true,
		PromptVisible: skill.PromptVisible == nil || *skill.PromptVisible,
	}
	return entry, resources, nil
}

// frozenResourceWithin reports whether resource is a non-empty descendant of
// root. When allowRoot is false the resource must also differ from root, so an
// alias root cannot be used as a package.
func frozenResourceWithin(root string, resource string, allowRoot bool) bool {
	prefix := strings.TrimRight(root, "/")
	if prefix == "" || !strings.HasPrefix(resource, prefix) {
		return false
	}
	rest, ok := strings.CutPrefix(resource, prefix)
	if !ok || !strings.HasPrefix(rest, "/") {
		return false
	}
	rest = rest[1:]
	if rest == "" {
		return allowRoot
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// UsesMCPResources reports that a frozen snapshot never needs the MCP resource
// client (Rust SkillProvider::uses_mcp_resources).
func (p *FrozenCloudProvider) UsesMCPResources() bool { return false }

func (p *FrozenCloudProvider) List(ctx context.Context, query ListQuery) (Catalog, error) {
	if p == nil {
		return Catalog{}, nil
	}
	out := Catalog{Warnings: append([]string(nil), p.catalog.Warnings...)}
	out.Entries = append([]CatalogEntry(nil), p.catalog.Entries...)
	return out, nil
}

func (p *FrozenCloudProvider) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	if request.Authority != (Authority{Kind: SourceCloud, ID: FrozenCloudServerName}) {
		return ReadResult{}, errors.New("invalid frozen skill authority")
	}
	resources, ok := p.resources[request.PackageID]
	if !ok {
		resources = nil
	}
	contents, ok := resources[request.Resource]
	if !ok {
		return ReadResult{}, errors.New("unknown frozen skill resource")
	}
	return ReadResult{Resource: request.Resource, Contents: contents}, nil
}

func (p *FrozenCloudProvider) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	return SearchResult{}, errors.New("frozen skill snapshots do not support search")
}

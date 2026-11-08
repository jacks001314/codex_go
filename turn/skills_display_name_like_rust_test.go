package turn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/skillprovider"
)

// Mirrors Rust #59954 (`ext/skills/src/tools/mod.rs` matching_packages): a
// skills.read locator resolves the exact package, then a short alias, then the
// model-visible display name skills.list returned. A display name that matches
// several packages is an error, never a silent first match.
func TestSkillsReadResolvesListedDisplayNameLikeRust(t *testing.T) {
	longName := strings.Repeat("n", maxOrchestratorQualifiedNameChars+40)
	listedName := truncateUTF8Bytes(longName, maxOrchestratorQualifiedNameChars)
	entries := []skillprovider.CatalogEntry{
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "root-1"}, PackageID: "skill://root-1/skills/foo/SKILL.md", Name: "writer", MainResource: "skill://root-1/skills/foo/SKILL.md", Enabled: true, PromptVisible: true},
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "root-1"}, PackageID: "skill://root-1/skills/long/SKILL.md", Name: longName, MainResource: "skill://root-1/skills/long/SKILL.md", Enabled: true, PromptVisible: true},
	}
	candidates := []listedSkill{
		{Authority: skillsToolAuthority{Kind: "executor", ID: "root-1"}, Package: "skill://root-1/skills/foo/SKILL.md", Name: truncateUTF8Bytes("writer", maxOrchestratorQualifiedNameChars), MainResource: "skill://root-1/skills/foo/SKILL.md"},
		{Authority: skillsToolAuthority{Kind: "executor", ID: "root-1"}, Package: "skill://root-1/skills/long/SKILL.md", Name: listedName, MainResource: "skill://root-1/skills/long/SKILL.md"},
	}

	// Display name resolves to its package.
	if resolved, ok, err := resolveListedSkill(candidates, entries, "writer"); err != nil || !ok || resolved.Package != "skill://root-1/skills/foo/SKILL.md" {
		t.Fatalf("display name resolved to %#v ok=%t err=%v, want the writer package", resolved, ok, err)
	}
	// The name skills.list returned (truncated on a character boundary) resolves
	// to the same package as the untruncated name.
	byListed, ok, err := resolveListedSkill(candidates, entries, listedName)
	if err != nil || !ok || byListed.Package != "skill://root-1/skills/long/SKILL.md" {
		t.Fatalf("listed name resolved to %#v ok=%t err=%v, want the long-name package", byListed, ok, err)
	}
	if byFull, ok, err := resolveListedSkill(candidates, entries, longName); err != nil || !ok || byFull.Package != byListed.Package {
		t.Fatalf("full name resolved to %#v ok=%t err=%v, want the same package", byFull, ok, err)
	}
	// The short alias still wins before the display name (regression guard).
	if resolved, ok, err := resolveListedSkill(candidates, entries, "e0/foo/SKILL.md"); err != nil || !ok || resolved.Package != "skill://root-1/skills/foo/SKILL.md" {
		t.Fatalf("alias resolved to %#v ok=%t err=%v", resolved, ok, err)
	}
	// A `skill://` locator never falls through to looser matching.
	if resolved, ok, err := resolveListedSkill(candidates, entries, "skill://root-1/skills/missing/SKILL.md"); err != nil || ok {
		t.Fatalf("skill:// miss resolved to %#v ok=%t err=%v, want not found", resolved, ok, err)
	}

	// Two packages exposing the same display name are ambiguous.
	shared := []skillprovider.CatalogEntry{
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "root-1"}, PackageID: "skill://root-1/skills/a/SKILL.md", Name: "shared-name", Enabled: true, PromptVisible: true},
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "root-1"}, PackageID: "skill://root-1/skills/b/SKILL.md", Name: "shared-name", Enabled: true, PromptVisible: true},
	}
	ambiguous := []listedSkill{
		{Authority: skillsToolAuthority{Kind: "executor", ID: "root-1"}, Package: "skill://root-1/skills/a/SKILL.md", Name: "shared-name"},
		{Authority: skillsToolAuthority{Kind: "executor", ID: "root-1"}, Package: "skill://root-1/skills/b/SKILL.md", Name: "shared-name"},
	}
	if resolved, ok, err := resolveListedSkill(ambiguous, shared, "shared-name"); err == nil || ok {
		t.Fatalf("ambiguous name resolved to %#v ok=%t err=%v, want an ambiguity error", resolved, ok, err)
	} else if err.Error() != skillNameAmbiguousMessage {
		t.Fatalf("ambiguity error = %q, want %q", err.Error(), skillNameAmbiguousMessage)
	}
	// Naming the package directly is unambiguous.
	if resolved, ok, err := resolveListedSkill(ambiguous, shared, "skill://root-1/skills/b/SKILL.md"); err != nil || !ok || resolved.Package != "skill://root-1/skills/b/SKILL.md" {
		t.Fatalf("exact package resolved to %#v ok=%t err=%v", resolved, ok, err)
	}
}

// staticSkillProvider serves one fixed catalog listing to the registry.
type staticSkillProvider struct {
	catalog skillprovider.Catalog
}

func (s staticSkillProvider) List(context.Context, skillprovider.ListQuery) (skillprovider.Catalog, error) {
	return s.catalog, nil
}

func (s staticSkillProvider) Read(context.Context, skillprovider.ReadRequest) (skillprovider.ReadResult, error) {
	return skillprovider.ReadResult{}, errors.New("static skill provider has no bodies")
}

func (s staticSkillProvider) Search(context.Context, skillprovider.SearchRequest) (skillprovider.SearchResult, error) {
	return skillprovider.SearchResult{}, nil
}

// TestSkillsReadOmittingAuthorityRejectsAmbiguousDisplayNameLikeRust mirrors
// Rust `matching_packages`: when skills.read omits the source, a display name
// owned by two installations must fail instead of silently reading whichever
// environment the catalog happened to list first.
func TestSkillsReadOmittingAuthorityRejectsAmbiguousDisplayNameLikeRust(t *testing.T) {
	entries := []skillprovider.CatalogEntry{
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "env-a"}, PackageID: "skill://env-a/skills/a/SKILL.md", Name: "writer", MainResource: "skill://env-a/skills/a/SKILL.md", Enabled: true, PromptVisible: true},
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "env-b"}, PackageID: "skill://env-b/skills/a/SKILL.md", Name: "writer", MainResource: "skill://env-b/skills/a/SKILL.md", Enabled: true, PromptVisible: true},
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "env-a"}, PackageID: "skill://env-a/skills/unique/SKILL.md", Name: "unique", MainResource: "skill://env-a/skills/unique/SKILL.md", Enabled: true, PromptVisible: true},
	}
	source := skillprovider.Source{
		Kind:     skillprovider.SourceExecutor,
		Provider: staticSkillProvider{catalog: skillprovider.Catalog{Entries: entries}},
	}
	executor := &skillsToolExecutor{providers: skillprovider.NewRegistry(source)}

	if authority, ok, err := executor.skillReadAuthorityForPackage(context.Background(), "writer"); err == nil || ok {
		t.Fatalf("ambiguous display name resolved to %#v ok=%t err=%v, want an ambiguity error", authority, ok, err)
	} else if err.Error() != skillNameAmbiguousMessage {
		t.Fatalf("ambiguity error = %q, want %q", err.Error(), skillNameAmbiguousMessage)
	}
	authority, ok, err := executor.skillReadAuthorityForPackage(context.Background(), "unique")
	if err != nil || !ok || authority.Kind != "executor" || authority.ID != "env-a" {
		t.Fatalf("unique display name resolved to %#v ok=%t err=%v, want env-a", authority, ok, err)
	}
	// An exact package locator keeps its own tier, so it never becomes ambiguous.
	if authority, ok, err := executor.skillReadAuthorityForPackage(context.Background(), "skill://env-b/skills/a/SKILL.md"); err != nil || !ok || authority.ID != "env-b" {
		t.Fatalf("exact package resolved to %#v ok=%t err=%v, want env-b", authority, ok, err)
	}
	if _, ok, err := executor.skillReadAuthorityForPackage(context.Background(), "missing"); err != nil || ok {
		t.Fatalf("unknown locator ok=%t err=%v, want not found", ok, err)
	}
}

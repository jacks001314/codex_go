package skillprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func frozenCloudSnapshotJSON() string {
	return `{"skills":[{"package":"skill://demo/work","name":"demo:work",` +
		`"description":"Do the work.","alias_root":"skill://demo",` +
		`"main_resource":"skill://demo/work/SKILL.md",` +
		`"resources":{"skill://demo/work/SKILL.md":"# Work\nKeep it readable.\n"}}]}`
}

func frozenCloudRequest() ReadRequest {
	return ReadRequest{
		Authority: Authority{Kind: SourceCloud, ID: FrozenCloudServerName},
		PackageID: "skill://demo/work",
		Resource:  "skill://demo/work/SKILL.md",
	}
}

func TestFrozenCloudFileIsLoadedOnceAndReadsPreserveContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	if err := os.WriteFile(path, []byte(frozenCloudSnapshotJSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := LoadFrozenCloudProvider(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result, err := provider.Read(context.Background(), frozenCloudRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource != frozenCloudRequest().Resource || result.Contents != "# Work\nKeep it readable.\n" {
		t.Fatalf("Read() = %#v", result)
	}
	if provider.UsesMCPResources() {
		t.Fatal("frozen snapshots must not use MCP resources")
	}
	catalog, err := provider.List(context.Background(), ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Entries) != 1 {
		t.Fatalf("catalog entries = %d, want 1", len(catalog.Entries))
	}
	entry := catalog.Entries[0]
	if !entry.PromptVisible || !entry.Enabled || entry.DisplayPath != "skill://demo/work" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Authority != (Authority{Kind: SourceCloud, ID: FrozenCloudServerName}) {
		t.Fatalf("authority = %#v", entry.Authority)
	}
	if _, err := provider.Search(context.Background(), SearchRequest{PackageID: entry.PackageID, Query: "work"}); err == nil {
		t.Fatal("frozen snapshots must reject search")
	}
}

func TestFrozenCloudHiddenSkillStaysReadable(t *testing.T) {
	snapshot := strings.Replace(frozenCloudSnapshotJSON(), `"alias_root"`, `"prompt_visible":false,"alias_root"`, 1)
	provider, err := FrozenCloudProviderFromJSON([]byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := provider.List(context.Background(), ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Entries[0].PromptVisible {
		t.Fatal("prompt_visible=false must hide the entry")
	}
	result, err := provider.Read(context.Background(), frozenCloudRequest())
	if err != nil || result.Contents != "# Work\nKeep it readable.\n" {
		t.Fatalf("Read() = %#v err=%v", result, err)
	}
}

func TestFrozenCloudReadsRejectAuthorityAndBoundaryCrossings(t *testing.T) {
	provider, err := FrozenCloudProviderFromJSON([]byte(frozenCloudSnapshotJSON()))
	if err != nil {
		t.Fatal(err)
	}
	otherAuthority := frozenCloudRequest()
	otherAuthority.Authority = Authority{Kind: SourceExecutor, ID: "demo"}
	otherPackage := frozenCloudRequest()
	otherPackage.PackageID = "skill://other/work"
	otherResource := frozenCloudRequest()
	otherResource.Resource = "skill://demo/work/../SKILL.md"
	for _, request := range []ReadRequest{otherAuthority, otherPackage, otherResource} {
		if _, err := provider.Read(context.Background(), request); err == nil {
			t.Fatalf("Read(%#v) succeeded, want an error", request)
		}
	}
}

func TestFrozenCloudRejectsMalformedSnapshots(t *testing.T) {
	oversized := strings.Replace(frozenCloudSnapshotJSON(),
		`"# Work\nKeep it readable.\n"`,
		`"`+strings.Repeat("x", FrozenCloudResourceMaxBytes+1)+`"`, 1)
	cases := map[string]string{
		"missing main resource": strings.Replace(frozenCloudSnapshotJSON(),
			`"main_resource":"skill://demo/work/SKILL.md"`,
			`"main_resource":"skill://demo/work/missing.md"`, 1),
		"resource outside package": strings.Replace(frozenCloudSnapshotJSON(),
			`"skill://demo/work/SKILL.md":"# Work\nKeep it readable.\n"`,
			`"skill://demo/work/SKILL.md":"# Work\n","skill://other/secret.md":"secret"`, 1),
		"oversized resource": oversized,
		"duplicate package": `{"skills":[` + strings.TrimSuffix(strings.TrimPrefix(frozenCloudSnapshotJSON(), `{"skills":[`), `]}`) + `,` +
			strings.TrimSuffix(strings.TrimPrefix(frozenCloudSnapshotJSON(), `{"skills":[`), `]}`) + `]}`,
		"alias root outside package": strings.Replace(frozenCloudSnapshotJSON(),
			`"alias_root":"skill://demo"`, `"alias_root":"skill://other"`, 1),
		"empty name": strings.Replace(frozenCloudSnapshotJSON(),
			`"name":"demo:work"`, `"name":"  "`, 1),
		"unknown field": strings.Replace(frozenCloudSnapshotJSON(),
			`"name":"demo:work"`, `"name":"demo:work","extra":true`, 1),
		"missing skills": `{}`,
		"invalid json":   `not JSON`,
		"trailing data":  frozenCloudSnapshotJSON() + `{}`,
	}
	for name, snapshot := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := FrozenCloudProviderFromJSON([]byte(snapshot)); err == nil {
				t.Fatal("snapshot accepted, want an error")
			}
		})
	}
}

func TestFrozenCloudCatalogSortsByPackage(t *testing.T) {
	snapshot := `{"skills":[` +
		`{"package":"skill://demo/zeta","name":"demo:zeta","description":"z","alias_root":"skill://demo",` +
		`"main_resource":"skill://demo/zeta/SKILL.md","resources":{"skill://demo/zeta/SKILL.md":"z"}},` +
		`{"package":"skill://demo/alpha","name":"demo:alpha","description":"a","alias_root":"skill://demo",` +
		`"main_resource":"skill://demo/alpha/SKILL.md","resources":{"skill://demo/alpha/SKILL.md":"a"}}]}`
	provider, err := FrozenCloudProviderFromJSON([]byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := provider.List(context.Background(), ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Entries) != 2 || catalog.Entries[0].PackageID != "skill://demo/alpha" || catalog.Entries[1].PackageID != "skill://demo/zeta" {
		t.Fatalf("catalog order = %#v", catalog.Entries)
	}
}

func TestFrozenCloudLoadReportsUnreadableFile(t *testing.T) {
	_, err := LoadFrozenCloudProvider(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !strings.Contains(err.Error(), "cannot read skill snapshot") {
		t.Fatalf("LoadFrozenCloudProvider() error = %v", err)
	}
}

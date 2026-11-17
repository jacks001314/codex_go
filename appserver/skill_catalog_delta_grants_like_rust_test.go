package appserver

import (
	"strings"
	"testing"

	promptctx "codex_go/prompt"
	"codex_go/sandbox"
)

func executorGrantSkill(name string, description string, environmentID string) promptctx.InstructionsSkillMetadata {
	return promptctx.InstructionsSkillMetadata{
		Name:          name,
		Description:   description,
		Path:          "/skills/" + name + "/SKILL.md",
		LocatorKind:   "file",
		AuthorityKind: "executor",
		AuthorityID:   environmentID,
		EnvironmentID: environmentID,
		PackageID:     "skills",
		ResourceID:    name,
	}
}

func renderExecutorGrantCatalog(t *testing.T, budget promptctx.SkillMetadataBudget, grants map[string]struct{}, skills ...promptctx.InstructionsSkillMetadata) *promptctx.AvailableSkills {
	t.Helper()
	return promptctx.RenderExtensionAvailableSkillsWithOptions(skills, promptctx.AvailableSkillsRenderOptions{
		Budget:                   budget,
		IncludeUsageInstructions: true,
		PrimaryEnvironment:       "env-1",
		GrantedEnvironmentIDs:    grants,
	})
}

func grantedEnvironment(environmentID string) map[string]struct{} {
	return map[string]struct{}{environmentID: {}}
}

func skillCatalogGrantCounts(snapshot *skillCatalogSnapshot) (active int, withdrawn int) {
	if snapshot == nil {
		return 0, 0
	}
	for _, entry := range snapshot.Entries {
		switch entry.Grant {
		case skillCatalogGrantActive:
			active++
		case skillCatalogGrantWithdrawn:
			withdrawn++
		}
	}
	return active, withdrawn
}

// Rust ext/skills/tests/skills_extension/catalog_delta_tests.rs
// grant_expiry_withdraws_only_missing_skills_when_metadata_budget_is_exhausted.
// A row that disappears while it carried a filesystem grant must be withdrawn
// once, and its return announces a restoration exactly once.
func TestSkillCatalogDeltaGrantWithdrawalAndRestorationLikeRust(t *testing.T) {
	const source = "Selected-environment"
	wide := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	tight := charBudget(1)
	granted := grantedEnvironment("env-1")
	var snapshot *skillCatalogSnapshot

	step := func(skills []promptctx.InstructionsSkillMetadata, grants map[string]struct{}, budget promptctx.SkillMetadataBudget) string {
		t.Helper()
		available := renderExecutorGrantCatalog(t, budget, grants, skills...)
		remaining := budget.Limit
		if snapshot != nil {
			remaining -= skillCatalogUsageCost(snapshot, budget)
			if remaining < 0 {
				remaining = 0
			}
		}
		result := buildSkillCatalogDelta(source, available, true, snapshot, &remaining, budget)
		snapshot = result.Snapshot
		return result.Body
	}

	baseline := step([]promptctx.InstructionsSkillMetadata{executorGrantSkill("unrelated", "Removed independently.", "env-1")}, nil, wide)
	if !strings.Contains(baseline, "- unrelated:") {
		t.Fatalf("baseline listing should carry the unrelated skill:\n%s", baseline)
	}
	if active, withdrawn := skillCatalogGrantCounts(snapshot); active != 0 || withdrawn != 0 {
		t.Fatalf("an ungranted listing must not track grants: active=%d withdrawn=%d", active, withdrawn)
	}

	grantedUpdate := step([]promptctx.InstructionsSkillMetadata{
		executorGrantSkill("unrelated", "Removed independently.", "env-1"),
		executorGrantSkill("permanent", "Always available.", "env-1"),
		executorGrantSkill("temporary", "Turn only.", "env-1"),
		executorGrantSkill("independent", "Available without the grant on env-1.", "env-b"),
	}, granted, wide)
	for _, want := range []string{"- permanent:", "- temporary:", "- independent:"} {
		if !strings.Contains(grantedUpdate, want) {
			t.Fatalf("granted update missing %q:\n%s", want, grantedUpdate)
		}
	}
	if active, withdrawn := skillCatalogGrantCounts(snapshot); active != 2 || withdrawn != 0 {
		t.Fatalf("only env-1 rows are gated by the primary grant: active=%d withdrawn=%d", active, withdrawn)
	}

	wantRevalidation := "\n## " + source + " skills update\n" + skillCatalogRevalidateNotice
	expired := step([]promptctx.InstructionsSkillMetadata{executorGrantSkill("permanent", "Always available.", "env-1")}, nil, tight)
	if expired != wantRevalidation {
		t.Fatalf("grant expiry = %q, want %q", expired, wantRevalidation)
	}
	// Rust records the grant of the current render: the surviving row is no
	// longer gated, and only the missing row is remembered as withdrawn.
	if active, withdrawn := skillCatalogGrantCounts(snapshot); active != 0 || withdrawn != 1 {
		t.Fatalf("expiry should withdraw exactly the missing row: active=%d withdrawn=%d", active, withdrawn)
	}
	if again := step([]promptctx.InstructionsSkillMetadata{executorGrantSkill("permanent", "Always available.", "env-1")}, nil, tight); again != "" {
		t.Fatalf("a repeated expiry must stay silent, got %q", again)
	}

	wantRestoration := "\n## " + source + " skills update\n" + skillCatalogRestoredNotice
	restored := step([]promptctx.InstructionsSkillMetadata{
		executorGrantSkill("permanent", "Always available.", "env-1"),
		executorGrantSkill("temporary", "Turn only.", "env-1"),
	}, granted, tight)
	if restored != wantRestoration {
		t.Fatalf("grant restoration = %q, want %q", restored, wantRestoration)
	}
	if again := step([]promptctx.InstructionsSkillMetadata{
		executorGrantSkill("permanent", "Always available.", "env-1"),
		executorGrantSkill("temporary", "Turn only.", "env-1"),
	}, granted, tight); again != "" {
		t.Fatalf("a repeated restoration must stay silent, got %q", again)
	}
	if expiredAgain := step([]promptctx.InstructionsSkillMetadata{executorGrantSkill("permanent", "Always available.", "env-1")}, nil, tight); expiredAgain != wantRevalidation {
		t.Fatalf("second expiry = %q, want %q", expiredAgain, wantRevalidation)
	}
}

// Rust catalog_delta_tests.rs legacy_filesystem_catalog_revalidates_missing_rows_once:
// a snapshot written before grant tracking cannot tell which shown rows were
// gated, so the first update revalidates every missing row exactly once.
func TestSkillCatalogDeltaLegacyFilesystemRevalidatesMissingRowsOnceLikeRust(t *testing.T) {
	budget := charBudget(5_000)
	executorSkills := []promptctx.InstructionsSkillMetadata{
		executorGrantSkill("stay", "Keep this skill.", "env-1"),
		executorGrantSkill("gone", "Temporarily available.", "env-1"),
	}
	hostSkills := []promptctx.InstructionsSkillMetadata{
		deltaSkill("stay", "Keep this skill.", "/skills/stay/SKILL.md", ""),
		deltaSkill("gone", "Temporarily available.", "/skills/gone/SKILL.md", ""),
	}
	renderHost := func(skills ...promptctx.InstructionsSkillMetadata) *promptctx.AvailableSkills {
		return promptctx.RenderAvailableSkillsWithOptions(skills, promptctx.AvailableSkillsRenderOptions{Budget: budget, IncludeUsageInstructions: true})
	}

	executorInitial := buildSkillCatalogDelta("Selected-environment", renderExecutorGrantCatalog(t, budget, nil, executorSkills...), true, nil, new(int), budget)
	hostInitial := buildSkillCatalogDelta("Host", renderHost(hostSkills...), true, nil, new(int), budget)
	// Simulate the pre-grant snapshot layout: the key is absent, so decoding
	// leaves the flag false (Rust removes "tracksGrants" from both snapshots).
	executorInitial.Snapshot.TracksGrants = false
	hostInitial.Snapshot.TracksGrants = false

	used := skillCatalogUsageCost(executorInitial.Snapshot, budget) + skillCatalogUsageCost(hostInitial.Snapshot, budget)
	remaining := budget.Limit - used
	executorUpdate := buildSkillCatalogDelta("Selected-environment",
		renderExecutorGrantCatalog(t, budget, nil, executorSkills[0]), true, executorInitial.Snapshot, &remaining, budget)
	hostUpdate := buildSkillCatalogDelta("Host", renderHost(hostSkills[0]), true, hostInitial.Snapshot, &remaining, budget)
	want := "\n## Selected-environment skills update\n" + skillCatalogRevalidateNotice +
		"\n## Host skills update\n" + skillCatalogRevalidateNotice
	if got := executorUpdate.Body + hostUpdate.Body; got != want {
		t.Fatalf("legacy snapshot revalidation = %q, want %q", got, want)
	}
	if !executorUpdate.Snapshot.TracksGrants || !hostUpdate.Snapshot.TracksGrants {
		t.Fatal("the update must record that grants are tracked from now on")
	}

	remaining = budget.Limit - skillCatalogUsageCost(executorUpdate.Snapshot, budget) - skillCatalogUsageCost(hostUpdate.Snapshot, budget)
	executorAgain := buildSkillCatalogDelta("Selected-environment",
		renderExecutorGrantCatalog(t, budget, nil, executorSkills[0]), true, executorUpdate.Snapshot, &remaining, budget)
	hostAgain := buildSkillCatalogDelta("Host", renderHost(hostSkills[0]), true, hostUpdate.Snapshot, &remaining, budget)
	if got := executorAgain.Body + hostAgain.Body; got != "" {
		t.Fatalf("legacy revalidation must happen only once, got %q", got)
	}
}

// A catalog that becomes unavailable still has to withdraw the rows the model
// saw while their grant held.
func TestSkillCatalogDeltaUnavailableCatalogWithdrawsGrantsLikeRust(t *testing.T) {
	const source = "Selected-environment"
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	initial := buildSkillCatalogDelta(source,
		renderExecutorGrantCatalog(t, budget, grantedEnvironment("env-1"), executorGrantSkill("temporary", "Turn only.", "env-1")),
		true, nil, new(int), budget)
	if active, _ := skillCatalogGrantCounts(initial.Snapshot); active != 1 {
		t.Fatalf("expected one active grant, got %d", active)
	}
	remaining := budget.Limit - skillCatalogUsageCost(initial.Snapshot, budget)
	update := buildSkillCatalogDelta(source, nil, true, initial.Snapshot, &remaining, budget)
	if want := "\n## " + source + " skills update\n" + skillCatalogRevalidateNotice; update.Body != want {
		t.Fatalf("unavailable catalog = %q, want %q", update.Body, want)
	}
	if _, withdrawn := skillCatalogGrantCounts(update.Snapshot); withdrawn != 1 {
		t.Fatalf("expected the row to be recorded as withdrawn, got %d", withdrawn)
	}
	remaining = budget.Limit - skillCatalogUsageCost(update.Snapshot, budget)
	if again := buildSkillCatalogDelta(source, nil, true, update.Snapshot, &remaining, budget); again.Body != "" {
		t.Fatalf("a repeated unavailable catalog must stay silent, got %q", again.Body)
	}

	fresh := buildSkillCatalogDelta(source, nil, true, nil, new(int), budget)
	if fresh.Snapshot == nil || !fresh.Snapshot.TracksGrants {
		t.Fatal("first sight of an unavailable catalog must still record grant tracking")
	}
	if fresh.Body != "" {
		t.Fatalf("an unavailable catalog publishes no body, got %q", fresh.Body)
	}
}

// Rust catalog_delta.rs build_catalog_update: "A grant does not make a skill
// temporary if the model already saw it without one."
func TestSkillCatalogDeltaGrantNotAppliedToPreviouslyShownUngrantedLikeRust(t *testing.T) {
	const source = "Selected-environment"
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	skill := executorGrantSkill("alpha", "Alpha skill.", "env-1")
	initial := buildSkillCatalogDelta(source, renderExecutorGrantCatalog(t, budget, nil, skill), true, nil, new(int), budget)
	if active, _ := skillCatalogGrantCounts(initial.Snapshot); active != 0 {
		t.Fatalf("an ungranted listing must not record grants, got %d", active)
	}

	remaining := budget.Limit - skillCatalogUsageCost(initial.Snapshot, budget)
	granted := buildSkillCatalogDelta(source, renderExecutorGrantCatalog(t, budget, grantedEnvironment("env-1"), skill), true, initial.Snapshot, &remaining, budget)
	if granted.Body != "" {
		t.Fatalf("an unchanged row must stay silent, got %q", granted.Body)
	}
	if active, withdrawn := skillCatalogGrantCounts(granted.Snapshot); active != 0 || withdrawn != 0 {
		t.Fatalf("a previously shown ungranted row keeps no grant: active=%d withdrawn=%d", active, withdrawn)
	}

	remaining = budget.Limit - skillCatalogUsageCost(granted.Snapshot, budget)
	missing := buildSkillCatalogDelta(source, nil, true, granted.Snapshot, &remaining, budget)
	if missing.Body != "" {
		t.Fatalf("a row that never depended on a grant is not withdrawn, got %q", missing.Body)
	}
}

// Rust core/src/session/turn_context.rs::turn_skill_filesystem_grants: only
// turn-scoped approvals with a non-empty filesystem profile gate catalog rows,
// and the primary environment is the first turn environment.
func TestSkillFilesystemGrantsForTurnLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	primary, granted := router.skillFilesystemGrantsForTurn("thread-grants", "turn-1", nil)
	if primary != localEnvironmentID {
		t.Fatalf("primary environment = %q, want %q", primary, localEnvironmentID)
	}
	if len(granted) != 0 {
		t.Fatalf("no grant should be reported, got %v", granted)
	}

	router.recordPermissionGrant("thread-grants", "turn-1", "local", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{t.TempDir()}})
	if _, granted := router.skillFilesystemGrantsForTurn("thread-grants", "turn-1", nil); granted[localEnvironmentID] != struct{}{} {
		t.Fatalf("a turn filesystem grant must gate the local environment, got %v", granted)
	}
	if _, granted := router.skillFilesystemGrantsForTurn("thread-grants", "turn-2", nil); len(granted) != 0 {
		t.Fatalf("another turn must not inherit the grant, got %v", granted)
	}

	readOnly := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	readOnly.recordPermissionGrant("thread-read", "turn-1", "local", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{ReadFileSystem: []string{t.TempDir()}})
	if _, granted := readOnly.skillFilesystemGrantsForTurn("thread-read", "turn-1", nil); len(granted) != 1 {
		t.Fatalf("a read-only filesystem grant still counts as a filesystem grant, got %v", granted)
	}

	sessionOnly := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	sessionOnly.recordPermissionGrant("thread-session", "turn-1", "local", string(PermissionGrantScopeSession),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{t.TempDir()}})
	if _, granted := sessionOnly.skillFilesystemGrantsForTurn("thread-session", "turn-1", nil); len(granted) != 0 {
		t.Fatalf("session-scope grants never expire with a turn and must not gate rows, got %v", granted)
	}

	networkOnly := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	enabled := true
	networkOnly.recordPermissionGrant("thread-net", "turn-1", "local", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{Network: &enabled})
	if _, granted := networkOnly.skillFilesystemGrantsForTurn("thread-net", "turn-1", nil); len(granted) != 0 {
		t.Fatalf("a network-only grant is not a filesystem grant, got %v", granted)
	}
}

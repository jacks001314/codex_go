package config

// Rust parity: codex-rs/config/src/cloud_config_bundle.rs and
// codex-rs/cloud-config/src/cache.rs (#49269).
//
// The delivered enterprise policy carries a revision binding through config
// loading, and the cloud bundle cache is staged in a temporary file that is
// published atomically only while the observed policy revision is still current.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func policyTestBundle(id string) CloudConfigBundle {
	return CloudConfigBundle{ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
		ID: id, Name: id, Contents: `model = "gpt-5"`,
	}}}}
}

func stagedCacheFiles(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", home, err)
	}
	var files []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), cloudConfigBundleCacheFilename+".tmp-") {
			files = append(files, entry.Name())
		}
	}
	return files
}

func TestCloudConfigBundlePolicySuspendsSupersededRevisionsLikeRust(t *testing.T) {
	policy := NewCloudConfigBundlePolicy()
	first := policyTestBundle("first")
	revision := policy.ObserveRemoteBundle(&first)
	if revision == nil {
		t.Fatal("first observation returned no revision")
	}
	firstSnapshot := CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&firstSnapshot)
	if firstSnapshot.Binding == nil {
		t.Fatal("published snapshot carries no binding")
	}
	if got := firstSnapshot.Binding.Status(); got != CloudConfigBundleBindingCurrent {
		t.Fatalf("binding status = %v, want current after publication", got)
	}

	// Re-observing the same delivered bundle keeps the active revision current.
	if policy.ObserveRemoteBundle(&first) == nil {
		t.Fatal("equal observation returned no revision")
	}
	if got := firstSnapshot.Binding.Status(); got != CloudConfigBundleBindingCurrent {
		t.Fatalf("binding status = %v, want current after an equal observation", got)
	}

	committed := 0
	if err := revision.CommitIfCurrent(func() error { committed++; return nil }); err != nil {
		t.Fatalf("CommitIfCurrent(current) error = %v", err)
	}
	if committed != 1 {
		t.Fatalf("current revision commits = %d, want 1", committed)
	}

	// A changed delivered bundle suspends the previous revision before it is
	// validated or cached.
	second := policyTestBundle("second")
	newer := policy.ObserveRemoteBundle(&second)
	if newer == nil {
		t.Fatal("changed observation returned no revision")
	}
	if got := firstSnapshot.Binding.Status(); got != CloudConfigBundleBindingSuspended {
		t.Fatalf("binding status = %v, want suspended while the newer bundle is validated", got)
	}
	if err := revision.CommitIfCurrent(func() error { committed++; return nil }); err != nil {
		t.Fatalf("CommitIfCurrent(superseded) error = %v", err)
	}
	if committed != 1 {
		t.Fatalf("superseded revision commits = %d, want 1 (superseded commits must be dropped)", committed)
	}
	if err := newer.CommitIfCurrent(func() error { committed++; return nil }); err != nil {
		t.Fatalf("CommitIfCurrent(newer) error = %v", err)
	}
	if committed != 2 {
		t.Fatalf("newer revision commits = %d, want 2", committed)
	}

	// A load result that no longer matches the observed bundle leaves admission
	// suspended instead of reporting a current revision.
	supersededSnapshot := CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&supersededSnapshot)
	if supersededSnapshot.Binding == nil {
		t.Fatal("superseded snapshot carries no binding")
	}
	if got := supersededSnapshot.Binding.Status(); got != CloudConfigBundleBindingSuspended {
		t.Fatalf("binding status = %v, want suspended for a superseded load result", got)
	}
	secondSnapshot := CloudConfigBundleSnapshot{Bundle: &second}
	policy.PublishSnapshot(&secondSnapshot)
	if got := secondSnapshot.Binding.Status(); got != CloudConfigBundleBindingCurrent {
		t.Fatalf("binding status = %v, want current for the delivered bundle", got)
	}
	if got := firstSnapshot.Binding.Status(); got != CloudConfigBundleBindingStale {
		t.Fatalf("binding status = %v, want stale once a newer revision is active", got)
	}
	if got := firstSnapshot.Binding.Status(); got != CloudConfigBundleBindingStale {
		t.Fatalf("binding status = %v, want stale for the superseded revision", got)
	}
}

func TestCloudConfigBundlePolicyRetireStopsAdmissionAndCommitsLikeRust(t *testing.T) {
	policy := NewCloudConfigBundlePolicy()
	first := policyTestBundle("first")
	revision := policy.ObserveRemoteBundle(&first)
	if revision == nil {
		t.Fatal("observation returned no revision")
	}
	policy.Retire()
	second := policyTestBundle("second")
	if got := policy.ObserveRemoteBundle(&second); got != nil {
		t.Fatal("retired policy observed a new revision")
	}
	committed := false
	if err := revision.CommitIfCurrent(func() error { committed = true; return nil }); err != nil {
		t.Fatalf("CommitIfCurrent(retired) error = %v", err)
	}
	if committed {
		t.Fatal("retired policy committed staged persistence")
	}
	snapshot := CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&snapshot)
	if snapshot.Binding == nil || snapshot.Binding.Status() != CloudConfigBundleBindingSuspended {
		t.Fatalf("retired policy published an active binding: %#v", snapshot.Binding)
	}
}

// cachePermMatchesPublishedMode reports whether the published cache file carries
// the permission bits the atomic publish asks for (0600).
//
// Rust parity note (#49269): PublishIfCurrent chmods the staged file to 0600 and
// os.CreateTemp already creates it that way. Windows cannot express POSIX
// permission bits and reports 0666 for every file, so the platform rule lives in
// this pure function: POSIX platforms keep the strict assertion, Windows accepts
// the platform's report. Everything else about the Windows run (the destination
// exists, its bytes are the published payload, staging never touches it) stays
// asserted by the caller.
func cachePermMatchesPublishedMode(goos string, perm os.FileMode) bool {
	if goos == "windows" {
		return true
	}
	return perm == 0o600
}

func TestCachePermMatchesPublishedModeIsPlatformAware(t *testing.T) {
	if !cachePermMatchesPublishedMode("windows", 0o666) {
		t.Fatal("windows reports 0666 for every file; the rule must accept it")
	}
	if !cachePermMatchesPublishedMode("linux", 0o600) {
		t.Fatal("0600 is the mode the publish requests")
	}
	if cachePermMatchesPublishedMode("linux", 0o666) {
		t.Fatal("an over-permissive published cache must fail on POSIX platforms")
	}
	if cachePermMatchesPublishedMode("darwin", 0o644) {
		t.Fatal("0644 is not the requested mode")
	}
}

func TestStagedCloudConfigBundleCachePublishesAtomicallyLikeRust(t *testing.T) {
	home := t.TempDir()
	opts := CloudConfigFetchOptions{CodexHome: home, ChatGPTUserID: "user-1", AccountID: "account-1"}
	policy := NewCloudConfigBundlePolicy()
	first := policyTestBundle("first")
	revision := policy.ObserveRemoteBundle(&first)

	staged, err := prepareCloudConfigBundleCache(opts, first)
	if err != nil {
		t.Fatalf("prepareCloudConfigBundleCache() error = %v", err)
	}
	destination := filepath.Join(home, cloudConfigBundleCacheFilename)
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged cache exposed the destination before publication: stat error = %v", err)
	}
	if files := stagedCacheFiles(t, home); len(files) != 1 {
		t.Fatalf("staged temporary files = %v, want exactly one", files)
	}
	if err := staged.PublishIfCurrent(revision); err != nil {
		t.Fatalf("PublishIfCurrent() error = %v", err)
	}
	if files := stagedCacheFiles(t, home); len(files) != 0 {
		t.Fatalf("temporary files left after publication = %v", files)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat(destination) error = %v", err)
	}
	if perm := info.Mode().Perm(); !cachePermMatchesPublishedMode(runtime.GOOS, perm) {
		t.Fatalf("published cache permissions = %o, want 600", perm)
	}
	if loaded := loadCloudConfigBundleCache(opts); loaded == nil || !cloudConfigBundlesEqual(loaded, &first) {
		t.Fatalf("published cache did not verify: %#v", loaded)
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}

	// A second staged payload must not touch the published one until it is
	// published itself.
	second := policyTestBundle("second")
	restaged, err := prepareCloudConfigBundleCache(opts, second)
	if err != nil {
		t.Fatalf("prepareCloudConfigBundleCache(second) error = %v", err)
	}
	during, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}
	if !bytes.Equal(before, during) {
		t.Fatal("staging a new payload modified the published cache")
	}
	if err := restaged.PublishIfCurrent(revision); err != nil {
		t.Fatalf("PublishIfCurrent(second) error = %v", err)
	}
	if loaded := loadCloudConfigBundleCache(opts); loaded == nil || !cloudConfigBundlesEqual(loaded, &second) {
		t.Fatalf("republished cache did not verify: %#v", loaded)
	}
}

func TestStagedCloudConfigBundleCacheSuppressesSupersededAndRetiredWritesLikeRust(t *testing.T) {
	home := t.TempDir()
	opts := CloudConfigFetchOptions{CodexHome: home, ChatGPTUserID: "user-1", AccountID: "account-1"}
	destination := filepath.Join(home, cloudConfigBundleCacheFilename)
	policy := NewCloudConfigBundlePolicy()
	first := policyTestBundle("first")
	revision := policy.ObserveRemoteBundle(&first)
	initial, err := prepareCloudConfigBundleCache(opts, first)
	if err != nil {
		t.Fatalf("prepareCloudConfigBundleCache() error = %v", err)
	}
	if err := initial.PublishIfCurrent(revision); err != nil {
		t.Fatalf("PublishIfCurrent() error = %v", err)
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}

	// A concurrent refresh delivered a newer bundle, so the older staged write
	// must be dropped instead of overwriting the newer publication.
	second := policyTestBundle("second")
	if policy.ObserveRemoteBundle(&second) == nil {
		t.Fatal("changed observation returned no revision")
	}
	superseded, err := prepareCloudConfigBundleCache(opts, second)
	if err != nil {
		t.Fatalf("prepareCloudConfigBundleCache(superseded) error = %v", err)
	}
	if err := superseded.PublishIfCurrent(revision); err != nil {
		t.Fatalf("PublishIfCurrent(superseded) error = %v", err)
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a superseded revision overwrote the published cache")
	}
	if files := stagedCacheFiles(t, home); len(files) != 0 {
		t.Fatalf("superseded write left temporary files = %v", files)
	}

	// A retired policy may not publish at all.
	retired := NewCloudConfigBundlePolicy()
	retired.ObserveRemoteBundle(&first)
	retired.Retire()
	staged, err := prepareCloudConfigBundleCache(opts, second)
	if err != nil {
		t.Fatalf("prepareCloudConfigBundleCache(retired) error = %v", err)
	}
	if err := staged.PublishIfCurrent(nil); err != nil {
		t.Fatalf("PublishIfCurrent(retired) error = %v", err)
	}
	after, err = os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a retired policy published a cache write")
	}
	if files := stagedCacheFiles(t, home); len(files) != 0 {
		t.Fatalf("retired write left temporary files = %v", files)
	}
}

func TestLoadCloudConfigBundlePublishesStagedCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(policyTestBundle("fetched"))
	}))
	defer server.Close()
	policy := NewCloudConfigBundlePolicy()
	opts := CloudConfigFetchOptions{
		CodexHome:     home,
		BaseURL:       server.URL + "/backend-api",
		ChatGPTUserID: "user-1",
		AccountID:     "account-1",
		HTTPClient:    server.Client(),
		Policy:        policy,
	}
	bundle, err := LoadCloudConfigBundle(t.Context(), opts)
	if err != nil {
		t.Fatalf("LoadCloudConfigBundle() error = %v", err)
	}
	if bundle == nil {
		t.Fatal("LoadCloudConfigBundle() returned no bundle")
	}
	if files := stagedCacheFiles(t, home); len(files) != 0 {
		t.Fatalf("fetch left temporary cache files = %v", files)
	}
	if loaded := loadCloudConfigBundleCache(opts); loaded == nil || !cloudConfigBundlesEqual(loaded, bundle) {
		t.Fatalf("fetched bundle was not published to the cache: %#v", loaded)
	}

	// A retired policy suppresses the cache write entirely.
	retiredHome := t.TempDir()
	retiredPolicy := NewCloudConfigBundlePolicy()
	retiredPolicy.Retire()
	retiredOpts := opts
	retiredOpts.CodexHome = retiredHome
	retiredOpts.Policy = retiredPolicy
	if _, err := LoadCloudConfigBundle(t.Context(), retiredOpts); err != nil {
		t.Fatalf("LoadCloudConfigBundle(retired) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(retiredHome, cloudConfigBundleCacheFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired policy wrote a cache entry: stat error = %v", err)
	}
}

func TestLoadWithOptionsCarriesCloudConfigBindingLikeRust(t *testing.T) {
	home := t.TempDir()
	policy := NewCloudConfigBundlePolicy()
	first := policyTestBundle("first")
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) { return &first, nil })
	loader.WithEMAPolicySnapshots(policy, func() CloudConfigBundleSnapshot {
		snapshot := CloudConfigBundleSnapshot{Bundle: &first}
		policy.PublishSnapshot(&snapshot)
		return snapshot
	})
	cfg, err := LoadWithOptions(home, &LoadOptions{
		IncludeManagedConfig: true,
		ManagedConfigPath:    filepath.Join(home, "managed_config.toml"),
		IgnoreProjectConfig:  true,
		CloudConfigBundle:    loader,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	binding := cfg.CloudConfigBinding()
	if binding == nil {
		t.Fatal("loaded configuration carries no cloud config binding")
	}
	if got := binding.Status(); got != CloudConfigBundleBindingCurrent {
		t.Fatalf("binding status = %v, want current", got)
	}
	// The binding stays live: a newer delivered bundle suspends the
	// configuration's policy instead of leaving it silently current.
	second := policyTestBundle("second")
	if policy.ObserveRemoteBundle(&second) == nil {
		t.Fatal("changed observation returned no revision")
	}
	if got := cfg.CloudConfigBinding().Status(); got != CloudConfigBundleBindingSuspended {
		t.Fatalf("binding status = %v, want suspended after a newer bundle", got)
	}
	if got := binding.Status(); got != CloudConfigBundleBindingSuspended {
		t.Fatalf("binding status = %v, want suspended for the superseded revision", got)
	}
	newerSnapshot := CloudConfigBundleSnapshot{Bundle: &second}
	policy.PublishSnapshot(&newerSnapshot)
	if got := cfg.CloudConfigBinding().Status(); got != CloudConfigBundleBindingStale {
		t.Fatalf("binding status = %v, want stale once a newer revision is active", got)
	}
	// Retiring the loader's policy suspends admission for that configuration.
	loader.RetireEMAPolicy()
	if got := cfg.CloudConfigBinding().Status(); got != CloudConfigBundleBindingSuspended {
		t.Fatalf("binding status = %v, want suspended after retirement", got)
	}
}

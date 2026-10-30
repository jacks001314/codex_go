package execserver

import (
	"context"
	"testing"
)

// Mirrors Rust #40343: initialization metadata seeds a lifetime cache that is
// shared by cached lookups, while the app-server probe path remains uncached.
func TestEnvironmentInfoCacheUsesInitializationMetadataLikeRust(t *testing.T) {
	client := &Client{}
	initial := &EnvironmentInfo{Shell: ShellInfo{Name: "bash", Path: "/bin/bash"}}
	client.rememberEnvironmentInfo(initial)

	cached, err := client.cachedEnvironmentInfo(context.Background())
	if err != nil {
		t.Fatalf("cachedEnvironmentInfo() error = %v", err)
	}
	if cached != initial {
		t.Fatalf("cached environment info = %#v, want the initialization value", cached)
	}

	replacement := &EnvironmentInfo{Shell: ShellInfo{Name: "zsh", Path: "/bin/zsh"}}
	client.rememberEnvironmentInfo(replacement)
	cached, err = client.cachedEnvironmentInfo(context.Background())
	if err != nil || cached != initial {
		t.Fatalf("cache was replaced: info=%#v err=%v", cached, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.EnvironmentInfo(cancelled); err == nil {
		t.Fatal("uncached EnvironmentInfo probe unexpectedly used the cache")
	}
}

// Mirrors Rust #49798: cached executor metadata is shared rather than copied,
// so every cached lookup returns the same value (Rust compares the Arc with
// ptr_eq across a cloned client). The forced probe stays a fresh value.
func TestEnvironmentInfoCacheSharesCachedValueLikeRust(t *testing.T) {
	client := &Client{}
	initial := &EnvironmentInfo{Shell: ShellInfo{Name: "bash", Path: "/bin/bash"}}
	client.rememberEnvironmentInfo(initial)

	first, err := client.cachedEnvironmentInfo(context.Background())
	if err != nil {
		t.Fatalf("cachedEnvironmentInfo() error = %v", err)
	}
	second, err := client.cachedEnvironmentInfo(context.Background())
	if err != nil {
		t.Fatalf("cachedEnvironmentInfo() second error = %v", err)
	}
	if first != initial || second != first {
		t.Fatalf("cached lookups were not shared: initial=%p first=%p second=%p", initial, first, second)
	}
}

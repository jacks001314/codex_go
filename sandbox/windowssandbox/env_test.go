package windowssandbox

import (
	"testing"
)

func TestNormalizeNullDeviceEnv(t *testing.T) {
	env := map[string]string{"A": "/dev/null", "B": `\\dev\\null`, "C": "keep"}
	NormalizeNullDeviceEnv(env)
	if env["A"] != "NUL" || env["B"] != "NUL" || env["C"] != "keep" {
		t.Fatalf("env = %#v", env)
	}
}

func TestEnsureNonInteractivePagerPreservesExisting(t *testing.T) {
	env := map[string]string{"PAGER": "cat"}
	EnsureNonInteractivePager(env)
	if env["PAGER"] != "cat" || env["GIT_PAGER"] != "more.com" {
		t.Fatalf("env = %#v", env)
	}
	if _, ok := env["LESS"]; !ok {
		t.Fatalf("LESS missing: %#v", env)
	}
}

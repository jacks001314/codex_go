package parity

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"codex_go/features"
)

// rustFeatureSpec is one parsed `FeatureSpec { .. }` entry from Rust's registry.
type rustFeatureSpec struct {
	// stage is the simple `Stage::<Variant>` name, empty for the OS-conditional
	// entry (see stageOS).
	stage string
	// stageOS, when non-nil, is the `cfg!(any(target_os = ...))` set that makes
	// the stage experimental on those hosts and under development elsewhere.
	stageOS []string
	// defaultExpr is Rust's `default_enabled` expression verbatim (`true`,
	// `false`, or `cfg!(windows)`).
	defaultExpr string
}

var (
	rustFeatureSpecPattern = regexp.MustCompile(`(?s)FeatureSpec \{\s*(.*?)\n    \},`)
	rustFeatureKeyPattern  = regexp.MustCompile(`key: "([^"]+)"`)
	rustFeatureStageLine   = regexp.MustCompile(`(?m)^\s*stage: ([^\n]+)`)
	rustFeatureDefault     = regexp.MustCompile(`default_enabled: ([^,\n]+)`)
	rustFeatureTargetOS    = regexp.MustCompile(`target_os = "([^"]+)"`)
)

// parseRustFeatureSpecs extracts the FEATURES registry from Rust's features
// crate source.
func parseRustFeatureSpecs(t *testing.T, source string) map[string]rustFeatureSpec {
	t.Helper()
	start := strings.Index(source, "pub const FEATURES: &[FeatureSpec] = &[")
	if start < 0 {
		t.Fatal("Rust features source is missing the FEATURES registry")
	}
	matches := rustFeatureSpecPattern.FindAllStringSubmatch(source[start:], -1)
	specs := make(map[string]rustFeatureSpec, len(matches))
	for _, match := range matches {
		body := match[1]
		key := rustFeatureKeyPattern.FindStringSubmatch(body)
		if key == nil {
			continue
		}
		spec := rustFeatureSpec{}
		if stage := rustFeatureStageLine.FindStringSubmatch(body); stage != nil {
			raw := strings.TrimRight(strings.TrimSpace(stage[1]), ",")
			switch {
			case strings.HasPrefix(raw, "if cfg!("):
				for _, os := range rustFeatureTargetOS.FindAllStringSubmatch(body, -1) {
					spec.stageOS = append(spec.stageOS, os[1])
				}
			case strings.HasPrefix(raw, "Stage::"):
				variant := strings.TrimPrefix(raw, "Stage::")
				if index := strings.IndexAny(variant, " {"); index >= 0 {
					variant = variant[:index]
				}
				spec.stage = variant
			}
		}
		if def := rustFeatureDefault.FindStringSubmatch(body); def != nil {
			spec.defaultExpr = strings.TrimSpace(def[1])
		}
		specs[key[1]] = spec
	}
	return specs
}

// expectedStage resolves a parsed Rust stage for the running host to Go's
// Stage value.
func (s rustFeatureSpec) expectedStage() features.Stage {
	if s.stage != "" {
		return goStageFor(s.stage)
	}
	for _, os := range s.stageOS {
		if runtime.GOOS == os {
			return features.StageExperimental
		}
	}
	return features.StageUnderDevelopment
}

// goStageFor maps a Rust Stage variant name to Go's Stage value.
func goStageFor(variant string) features.Stage {
	switch variant {
	case "Experimental":
		return features.StageExperimental
	case "Stable":
		return features.StageStable
	case "Deprecated":
		return features.StageDeprecated
	case "Removed":
		return features.StageRemoved
	default:
		return features.StageUnderDevelopment
	}
}

// expectedDefault resolves Rust's default_enabled expression for this host.
func (s rustFeatureSpec) expectedDefault() bool {
	switch s.defaultExpr {
	case "true":
		return true
	case "false", "":
		return false
	case "cfg!(windows)":
		return runtime.GOOS == "windows"
	default:
		return false
	}
}

// TestFeaturesRegistryMatchesRust is the L0 static check for the feature
// registry: every `FeatureSpec` in Rust's features crate must exist in Go's
// `features.Registry` with the same stage and default enablement (resolving the
// OS-conditional stage and `cfg!(windows)` default for the running host), and Go
// must not carry a feature key Rust retired.
func TestFeaturesRegistryMatchesRust(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "features", "src", "lib.rs")))
	rustSpecs := parseRustFeatureSpecs(t, source)
	if len(rustSpecs) < 100 {
		t.Fatalf("parsed only %d Rust feature specs", len(rustSpecs))
	}
	goByKey := make(map[string]features.Spec, len(features.Registry))
	for _, spec := range features.Registry {
		goByKey[spec.Key] = spec
	}
	for key, rustSpec := range rustSpecs {
		goSpec, ok := goByKey[key]
		if !ok {
			t.Errorf("feature %q is missing from Go's registry", key)
			continue
		}
		if want, got := rustSpec.expectedStage(), goSpec.Stage; want != got {
			t.Errorf("feature %q stage = %q, want %q", key, got, want)
		}
		if want, got := rustSpec.expectedDefault(), goSpec.DefaultEnabled; want != got {
			t.Errorf("feature %q defaultEnabled = %v, want %v", key, got, want)
		}
	}
	for key := range goByKey {
		if _, ok := rustSpecs[key]; !ok {
			t.Errorf("Go registry has feature %q that Rust does not declare", key)
		}
	}
}

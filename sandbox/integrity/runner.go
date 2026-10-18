package integrity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/metrics"
	"codex_go/sandbox"
)

const (
	// RunnerTimeout bounds how long a launch waits for observational checks.
	RunnerTimeout = 5 * time.Second
	// RunnerCheckMetric counts one dependency check outcome.
	RunnerCheckMetric = "codex.sandbox_integrity.check"
	// RunnerPreparationMetric records dependency/policy preparation duration.
	RunnerPreparationMetric = "codex.sandbox_integrity.preparation_ms"
	// RunnerDurationMetric records total runner duration.
	RunnerDurationMetric = "codex.sandbox_integrity.duration_ms"
)

// RunnerRequest is the executor-local observation input. It is independent of
// the process launch and never changes its result.
type RunnerRequest struct {
	SandboxType     sandbox.SandboxType
	SandboxOverride string
	Entries         []sandbox.FileSystemSandboxEntry
	CWD             string
	Command         []string
	Env             map[string]string
}

// RunnerMetrics is the bounded telemetry surface used by the runner.
type RunnerMetrics interface {
	Counter(name string, inc int, tags map[string]string)
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}

var integrityRunnerWork = executeIntegrityChecks

// RunIntegrityChecks observes containment dependencies without affecting
// execution. Preparation and checks run off the caller's goroutine; timing out
// stops waiting but does not cancel the observational work.
func RunIntegrityChecks(ctx context.Context, request *RunnerRequest) {
	runIntegrityChecks(ctx, request, metrics.Global())
}

func runIntegrityChecks(ctx context.Context, request *RunnerRequest, recorder RunnerMetrics) {
	if request == nil || request.SandboxType != platformSandboxType() ||
		request.SandboxOverride == "escalatedSandboxWithRestrictions" ||
		policyHasFullDiskWrite(request.Entries, request.CWD) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tags := map[string]string{
		"backend":        string(request.SandboxType),
		"has_deny_globs": boolTag(hasGlobDenials(request.Entries)),
	}
	startedAt := time.Now()
	work := make(chan runnerResult, 1)
	go func() {
		work <- integrityRunnerWork(request)
	}()
	timer := time.NewTimer(RunnerTimeout)
	defer timer.Stop()
	var result runnerResult
	select {
	case result = <-work:
	case <-timer.C:
		recordRunnerPreparation(recorder, time.Since(startedAt), false, tags)
		recordRunnerCheck(recorder, "preparation", "error", "timeout", tags, nil, nil)
		recordRunnerDuration(recorder, time.Since(startedAt), tags)
		return
	case <-ctx.Done():
		recordRunnerPreparation(recorder, 0, false, tags)
		recordRunnerCheck(recorder, "preparation", "error", "timeout", tags, nil, nil)
		recordRunnerDuration(recorder, time.Since(startedAt), tags)
		return
	}
	recordRunnerPreparation(recorder, result.preparation, result.preparationOK, tags)
	for _, check := range result.checks {
		recordRunnerCheck(recorder, check.dependency, check.outcome, check.errorKind, tags, check.target, check.finding)
	}
	recordRunnerDuration(recorder, time.Since(startedAt), tags)
}

type runnerResult struct {
	preparation   time.Duration
	preparationOK bool
	checks        []runnerCheck
}

type runnerCheck struct {
	dependency string
	target     *dependencyTarget
	finding    *Finding
	outcome    string
	errorKind  string
}

type dependencyTarget struct {
	path     string
	resolved string
	info     os.FileInfo
}

func executeIntegrityChecks(request *RunnerRequest) runnerResult {
	startedAt := time.Now()
	result := runnerResult{}
	policy, err := PreparePolicyWithEnvironment(request.Entries, request.CWD, request.Env)
	if err != nil {
		result.preparation = time.Since(startedAt)
		result.checks = []runnerCheck{{dependency: "preparation", outcome: "error", errorKind: "preparation"}}
		return result
	}
	if len(policy.Roots) == 0 {
		result.preparation = time.Since(startedAt)
		result.preparationOK = true
		return result
	}
	checker, err := NewFileContentsChecker(request.Entries, request.CWD)
	if err != nil {
		result.preparation = time.Since(startedAt)
		result.checks = []runnerCheck{{dependency: "preparation", outcome: "error", errorKind: "preparation"}}
		return result
	}
	result.preparationOK = true
	result.preparation = time.Since(startedAt)

	dependencies := baseIntegrityDependencies(request)
	dependencies = append(dependencies, platformIntegrityDependencies(request, policy)...)
	for _, dependency := range dependencies {
		check := runnerCheck{dependency: dependency.name}
		if dependency.err != nil {
			check.outcome = "error"
			check.errorKind = errorKind(dependency.err)
			result.checks = append(result.checks, check)
			continue
		}
		target, err := inspectDependency(dependency.path)
		if err != nil {
			check.outcome = outcomeForError(err)
			check.errorKind = errorKind(err)
			result.checks = append(result.checks, check)
			continue
		}
		check.target = &target
		finding, err := checker.Check(target.path)
		switch {
		case err != nil:
			check.outcome = "error"
			check.errorKind = errorKind(err)
		case finding != nil:
			check.outcome = "finding"
			check.finding = finding
		default:
			check.outcome = "clean"
		}
		result.checks = append(result.checks, check)
	}
	return result
}

type integrityDependency struct {
	name string
	path string
	err  error
}

func baseIntegrityDependencies(request *RunnerRequest) []integrityDependency {
	dependencies := []integrityDependency{}
	if executable, err := os.Executable(); err != nil {
		dependencies = append(dependencies, integrityDependency{name: "codex", err: err})
	} else {
		dependencies = append(dependencies, integrityDependency{name: "codex", path: executable})
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "windows") && len(request.Command) > 0 {
		dependencies = append(dependencies, integrityDependency{name: "sandbox_launcher", path: request.Command[0]})
	}
	return dependencies
}

func inspectDependency(path string) (dependencyTarget, error) {
	path = strings.TrimSpace(path)
	info, err := os.Stat(path)
	if err != nil {
		return dependencyTarget{}, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return dependencyTarget{}, err
	}
	return dependencyTarget{path: path, resolved: resolved, info: info}, nil
}

func policyHasFullDiskWrite(entries []sandbox.FileSystemSandboxEntry, cwd string) bool {
	if len(entries) == 0 {
		return false
	}
	hasRootWrite := false
	for _, entry := range entries {
		if entry.Access == sandbox.FileSystemAccessWrite && isRootEntry(entry.Path) {
			hasRootWrite = true
			continue
		}
		if entry.Access != sandbox.FileSystemAccessWrite {
			return false
		}
	}
	return hasRootWrite
}

func isRootEntry(path sandbox.FileSystemPath) bool {
	return path.Type == "special" && path.Value != nil && path.Value.Kind == "root"
}

func hasGlobDenials(entries []sandbox.FileSystemSandboxEntry) bool {
	for _, entry := range entries {
		if entry.Path.Type == "glob_pattern" {
			return true
		}
	}
	return false
}

func platformSandboxType() sandbox.SandboxType {
	switch runtime.GOOS {
	case "linux":
		return sandbox.SandboxTypeLinuxSeccomp
	case "darwin":
		return sandbox.SandboxTypeMacosSeatbelt
	case "windows":
		return sandbox.SandboxTypeWindowsRestrictedToken
	default:
		return sandbox.SandboxTypeNone
	}
}

func outcomeForError(err error) string {
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	return "error"
}

func errorKind(err error) string {
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return "other"
	}
	return "other"
}

func recordRunnerPreparation(recorder RunnerMetrics, duration time.Duration, ok bool, tags map[string]string) {
	if recorder == nil {
		return
	}
	outcome := "error"
	if ok {
		outcome = "ok"
	}
	recorder.RecordDuration(RunnerPreparationMetric, duration, mergeTags(tags, map[string]string{"outcome": outcome}))
}

func recordRunnerDuration(recorder RunnerMetrics, duration time.Duration, tags map[string]string) {
	if recorder != nil {
		recorder.RecordDuration(RunnerDurationMetric, duration, cloneTags(tags))
	}
}

func recordRunnerCheck(
	recorder RunnerMetrics,
	dependency string,
	outcome string,
	errorKind string,
	tags map[string]string,
	target *dependencyTarget,
	finding *Finding,
) {
	if recorder == nil {
		return
	}
	checkTags := mergeTags(tags, map[string]string{
		"checker":                   "contents",
		"dependency":                dependency,
		"outcome":                   outcome,
		"error_kind":                errorKind,
		"target_resolution_changed": "unknown",
		"multiple_hard_links":       "unknown",
		"multiple_write_roots":      "unknown",
	})
	if target != nil {
		checkTags["target_resolution_changed"] = boolTag(target.resolved != target.path)
		checkTags["multiple_hard_links"] = multipleHardLinksTag(target.info)
	}
	if finding != nil {
		checkTags["multiple_write_roots"] = boolTag(len(finding.WriteRoots) > 1)
	}
	recorder.Counter(RunnerCheckMetric, 1, checkTags)
}

func mergeTags(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	return mergeTags(tags, nil)
}

func boolTag(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

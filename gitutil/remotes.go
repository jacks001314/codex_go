package gitutil

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codex_go/envutil"
)

const (
	// maxConcurrentRemoteLookups bounds the native `git ls-remote --get-url`
	// fan-out when configured URLs need Git to resolve rewrites or resets.
	maxConcurrentRemoteLookups = 8
)

// GetGitRemoteURLs collects fetch remotes in a name -> sanitized URL map
// (Rust codex_git_utils::get_git_remote_urls, #61488). It returns nil when cwd
// is not inside a Git repository.
func GetGitRemoteURLs(ctx context.Context, cwd string) map[string]string {
	if _, err := RunWithTimeout(ctx, defaultGitTimeout, cwd, "rev-parse", "--git-dir"); err != nil {
		return nil
	}
	return GetGitRemoteURLsAssumeGitRepo(ctx, cwd)
}

// GetGitRemoteURLsAssumeGitRepo collects fetch remotes without checking
// whether cwd is a Git repository.
func GetGitRemoteURLsAssumeGitRepo(ctx context.Context, cwd string) map[string]string {
	return collectGitRemoteURLs(ctx, cwd, nil)
}

// ObserveGitRemoteURLs publishes verified fetch URLs as Git resolves them so a
// caller can update metadata progressively. Previously published names are kept
// by the consumer; every callback receives only the URLs resolved so far.
func ObserveGitRemoteURLs(ctx context.Context, cwd string, publish func(map[string]string)) {
	collectGitRemoteURLs(ctx, cwd, publish)
}

// collectGitRemoteURLs reads fetch URLs from NUL-delimited Git configuration
// instead of parsing `git remote -v`, so partial-clone filter text can never be
// mistaken for a remote entry (Rust #61488).
func collectGitRemoteURLs(ctx context.Context, cwd string, publish func(map[string]string)) map[string]string {
	deadline := time.Now().Add(defaultGitTimeout)
	notify := func(urls map[string]string) {
		if publish == nil || len(urls) == 0 {
			return
		}
		cloned := make(map[string]string, len(urls))
		for name, url := range urls {
			cloned[name] = url
		}
		publish(cloned)
	}

	// `git remote` ignores GIT_CONFIG, but `git config` treats it as an explicit
	// file. Clear it so the machine-readable query observes the same
	// configuration as `git remote`.
	stdout, status, err := runGitOutputWithEnv(ctx, cwd, []string{"config", "--null", "--get-regexp", `^(remote\.|url\.)`}, gitEnvOptions{
		remove: []string{"GIT_CONFIG"},
	})
	if err != nil || status != 0 {
		return nil
	}
	configured, rewritePrefixes, ok := parseGitRemoteConfig(stdout)
	if !ok {
		return nil
	}

	remotes := make(map[string]string)
	pending := make(map[string]bool)
	for _, name := range sortedStringKeys(configured) {
		values := configured[name]
		first := values[0]
		lastEmpty := len(values[len(values)-1]) == 0
		hasEmpty := lastEmpty
		for _, value := range values[:len(values)-1] {
			if len(value) == 0 {
				hasEmpty = true
				break
			}
		}
		if hasEmpty || hasRewritePrefix(first, rewritePrefixes) {
			pending[name] = lastEmpty
			continue
		}
		raw := string(first)
		if !utf8.ValidString(raw) || strings.ContainsAny(raw, "\r\n") {
			continue
		}
		raw = strings.TrimLeft(raw, " \t")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if url, sanitizeErr := SanitizeGitURL(raw); sanitizeErr == nil {
			remotes[name] = url
		}
	}
	if len(remotes) > 0 {
		notify(remotes)
	}

	// Let Git implement insteadOf precedence and version-dependent URL resets.
	order := make([]string, 0, len(pending))
	if _, ok := pending["origin"]; ok {
		order = append(order, "origin")
	}
	names := make([]string, 0, len(pending))
	for name := range pending {
		if name != "origin" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if !pending[name] {
			order = append(order, name)
		}
	}
	for _, name := range names {
		if pending[name] {
			order = append(order, name)
		}
	}

	var (
		resetOnce   sync.Once
		resetsEmpty bool
	)
	resetProbe := func() bool {
		resetOnce.Do(func() { resetsEmpty = gitResetsEmptyFetchURL(ctx, cwd) })
		return resetsEmpty
	}

	workers := maxConcurrentRemoteLookups
	if len(order) < workers {
		workers = len(order)
	}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]string)
	)
	jobs := make(chan string)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				url, ok := resolveGitRemoteURL(ctx, cwd, name, pending[name], resetProbe)
				if !ok {
					continue
				}
				mu.Lock()
				results[name] = url
				mu.Unlock()
				notify(map[string]string{name: url})
			}
		}()
	}
	for _, name := range order {
		select {
		case jobs <- name:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return finishGitRemoteCollect(remotes, results)
		case <-time.After(time.Until(deadline)):
			close(jobs)
			wg.Wait()
			return finishGitRemoteCollect(remotes, results)
		}
	}
	close(jobs)
	wg.Wait()
	return finishGitRemoteCollect(remotes, results)
}

func finishGitRemoteCollect(remotes map[string]string, resolved map[string]string) map[string]string {
	for name, url := range resolved {
		remotes[name] = url
	}
	if len(remotes) == 0 {
		return nil
	}
	return remotes
}

// parseGitRemoteConfig splits `git config --null --get-regexp "^(remote\.|url\.)"`
// output into remote fetch URL lists and insteadOf rewrite prefixes. A missing
// key/value separator aborts the whole result like Rust's parser.
func parseGitRemoteConfig(output []byte) (map[string][][]byte, [][]byte, bool) {
	configured := map[string][][]byte{}
	var rewritePrefixes [][]byte
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		separator := bytes.IndexByte(entry, '\n')
		if separator < 0 {
			return nil, nil, false
		}
		key, value := entry[:separator], entry[separator:]
		if len(value) == 0 || value[0] != '\n' {
			return nil, nil, false
		}
		value = value[1:]
		if name, ok := strings.CutPrefix(string(key), "remote."); ok {
			if base, found := strings.CutSuffix(name, ".url"); found {
				if !utf8.ValidString(base) {
					continue
				}
				// Git interprets an empty remote subsection as the name `.url`.
				if base == "" {
					base = ".url"
				}
				configured[base] = append(configured[base], value)
				continue
			}
		}
		if urlKey, ok := strings.CutPrefix(string(key), "url."); ok {
			if _, found := strings.CutSuffix(urlKey, ".insteadof"); found {
				rewritePrefixes = append(rewritePrefixes, value)
			}
		}
	}
	return configured, rewritePrefixes, true
}

func hasRewritePrefix(first []byte, prefixes [][]byte) bool {
	for _, prefix := range prefixes {
		if bytes.HasPrefix(first, prefix) {
			return true
		}
	}
	return false
}

// resolveGitRemoteURL asks Git for the effective fetch URL so insteadOf rewrites
// and version-dependent empty resets are applied exactly as Git does. An empty
// trailing URL clears the remote on newer Git; older Git keeps the first URL.
func resolveGitRemoteURL(ctx context.Context, cwd string, name string, lastEmpty bool, resetProbe func() bool) (string, bool) {
	if lastEmpty && resetProbe() {
		// A trailing empty URL clears the remote on newer Git.
		return "", false
	}
	stdout, status, err := runGitOutputWithEnv(ctx, cwd, []string{"ls-remote", "--get-url", "--", name}, gitEnvOptions{})
	if err != nil || status != 0 {
		return "", false
	}
	lines := strings.Split(strings.TrimSuffix(string(stdout), "\n"), "\n")
	if len(lines) != 1 {
		return "", false
	}
	line := lines[0]
	if strings.ContainsRune(line, '\r') {
		return "", false
	}
	line = strings.TrimLeft(line, " \t")
	if strings.TrimSpace(line) == "" {
		return "", false
	}
	url, err := SanitizeGitURL(line)
	if err != nil {
		return "", false
	}
	return url, true
}

// gitResetsEmptyFetchURL probes whether this Git version treats a trailing
// empty remote URL as clearing the remote (Rust #61488).
func gitResetsEmptyFetchURL(ctx context.Context, cwd string) bool {
	nullDevice := os.DevNull
	stdout, status, err := runGitOutputWithEnv(ctx, cwd, []string{
		"--git-dir", nullDevice,
		"-c", "remote.codex-reset-probe.url=before-reset",
		"-c", "remote.codex-reset-probe.url=",
		"ls-remote", "--get-url", "--", "codex-reset-probe",
	}, gitEnvOptions{
		set: map[string]string{
			"GIT_CONFIG_GLOBAL":   nullDevice,
			"GIT_CONFIG_NOSYSTEM": "1",
			"GIT_CONFIG_COUNT":    "0",
		},
		remove: []string{"GIT_CONFIG_PARAMETERS", "GIT_COMMON_DIR", "GIT_WORK_TREE"},
	})
	if err != nil || status != 0 {
		return false
	}
	switch strings.TrimRight(string(stdout), "\r\n") {
	case "before-reset":
		return false
	case "codex-reset-probe":
		return true
	default:
		return false
	}
}

type gitEnvOptions struct {
	set    map[string]string
	remove []string
}

// runGitOutputWithEnv runs a git command with the package's process-tree
// containment, returning raw stdout and the exit status.
func runGitOutputWithEnv(ctx context.Context, cwd string, args []string, options gitEnvOptions) ([]byte, int, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	newCommand := func() *exec.Cmd {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		envutil.ScrubCommandEnv(cmd)
		cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
		for key, value := range options.set {
			filtered := cmd.Env[:0]
			for _, entry := range cmd.Env {
				if !strings.HasPrefix(entry, key+"=") {
					filtered = append(filtered, entry)
				}
			}
			cmd.Env = append(filtered, key+"="+value)
		}
		for _, key := range options.remove {
			filtered := cmd.Env[:0]
			for _, entry := range cmd.Env {
				if !strings.HasPrefix(entry, key+"=") {
					filtered = append(filtered, entry)
				}
			}
			cmd.Env = filtered
		}
		return cmd
	}
	tree, err := startGitTree(newCommand(), newCommand)
	if err != nil {
		return nil, -1, err
	}
	done := make(chan error, 1)
	go func() { done <- tree.wait() }()
	timer := time.NewTimer(defaultGitTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		status := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				status = exitErr.ExitCode()
				return stdout.Bytes(), status, nil
			}
			return stdout.Bytes(), -1, err
		}
		return stdout.Bytes(), 0, nil
	case <-ctx.Done():
		tree.kill()
		<-done
		return nil, -1, ctx.Err()
	case <-timer.C:
		tree.kill()
		<-done
		return nil, -1, context.DeadlineExceeded
	}
}

func sortedStringKeys(values map[string][][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

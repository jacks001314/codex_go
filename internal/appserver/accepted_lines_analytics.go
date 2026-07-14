package appserver

import (
	"context"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitAcceptedLineFingerprintsAnalyticsEvent(ctx context.Context, threadID string, turnID string, runConfig *appTurnRunConfig, completedAt time.Time) {
	if r == nil || r.services.Analytics == nil || runConfig == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.AcceptedLineFingerprintsEventSink)
	if !ok {
		return
	}
	diff := r.activeUnifiedDiffSnapshot(threadID, turnID)
	if strings.TrimSpace(diff) == "" {
		return
	}
	summary := telemetry.AcceptedLineFingerprintsFromUnifiedDiff(diff)
	if summary.AcceptedAddedLines == 0 && summary.AcceptedDeletedLines == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	productSurface := "codex"
	modelSlug := strings.TrimSpace(runConfig.Model)
	var modelSlugPtr *string
	if modelSlug != "" {
		modelSlugPtr = &modelSlug
	}
	completedAtSeconds := completedAt.UTC().Unix()
	if completedAtSeconds < 0 {
		completedAtSeconds = 0
	}
	var repoHash *string
	if record := r.threadRecordForAnalytics(threadID); record != nil {
		repoHash = acceptedLineRepoHashForCWD(ctx, firstNonEmpty(record.Metadata.CWD, r.services.DefaultCWD))
	}
	requests := telemetry.AcceptedLineFingerprintEventRequests(&telemetry.AcceptedLineFingerprintEventInput{
		TurnID:               turnID,
		ThreadID:             threadID,
		ProductSurface:       &productSurface,
		ModelSlug:            modelSlugPtr,
		CompletedAt:          uint64(completedAtSeconds),
		RepoHash:             repoHash,
		AcceptedAddedLines:   summary.AcceptedAddedLines,
		AcceptedDeletedLines: summary.AcceptedDeletedLines,
		LineFingerprints:     summary.LineFingerprints,
	})
	for i := range requests {
		sink.TrackCodexAcceptedLineFingerprintsEvent(ctx, requests[i])
	}
}

func acceptedLineRepoHashForCWD(ctx context.Context, cwd string) *string {
	remoteURL := acceptedLineGitRemoteURLForCWD(ctx, cwd)
	if strings.TrimSpace(remoteURL) == "" {
		return nil
	}
	return acceptedLineRepoHashFromRemoteURL(remoteURL)
}

func acceptedLineRepoHashFromRemoteURL(remoteURL string) *string {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return nil
	}
	canonical := canonicalizeAcceptedLineGitRemoteURL(remoteURL)
	if canonical == "" {
		canonical = remoteURL
	}
	hash := telemetry.FingerprintHash("repo", canonical)
	return &hash
}

func acceptedLineGitRemoteURLForCWD(ctx context.Context, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(timeoutCtx, "git", "remote", "-v")
	cmd.Dir = cwd
	output, err := runCommandOutput(cmd)
	if err != nil {
		return ""
	}
	remotes := acceptedLineParseGitRemoteURLs(string(output))
	if len(remotes) == 0 {
		return ""
	}
	if origin := strings.TrimSpace(remotes["origin"]); origin != "" {
		return origin
	}
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.TrimSpace(remotes[names[0]])
}

func acceptedLineParseGitRemoteURLs(output string) map[string]string {
	remotes := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, " (fetch)") {
			continue
		}
		line = strings.TrimSuffix(line, " (fetch)")
		name, remoteURL, ok := strings.Cut(line, "\t")
		if !ok {
			name, remoteURL, ok = strings.Cut(line, " ")
		}
		name = strings.TrimSpace(name)
		remoteURL = strings.TrimSpace(remoteURL)
		if ok && name != "" && remoteURL != "" {
			remotes[name] = remoteURL
		}
	}
	return remotes
}

func canonicalizeAcceptedLineGitRemoteURL(remoteURL string) string {
	remoteURL = trimAcceptedLineGitSuffix(strings.TrimRight(strings.TrimSpace(remoteURL), "/"))
	if remoteURL == "" {
		return ""
	}
	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil || parsed.Scheme == "" {
			return ""
		}
		defaultPort := acceptedLineGitDefaultPort(parsed.Scheme)
		if defaultPort == "" || parsed.Host == "" {
			return ""
		}
		return canonicalizeAcceptedLineGitRemoteHostPath(parsed.Host, parsed.Path, defaultPort)
	}
	if hostPart, pathPart, ok := acceptedLineParseSCPLikeRemote(remoteURL); ok {
		return canonicalizeAcceptedLineGitRemoteHostPath(hostPart, pathPart, "")
	}
	hostPart, pathPart, ok := strings.Cut(remoteURL, "/")
	if !ok {
		return ""
	}
	return canonicalizeAcceptedLineGitRemoteHostPath(hostPart, pathPart, "")
}

func acceptedLineGitDefaultPort(scheme string) string {
	switch scheme {
	case "git":
		return "9418"
	case "http":
		return "80"
	case "https":
		return "443"
	case "ssh":
		return "22"
	default:
		return ""
	}
}

func acceptedLineParseSCPLikeRemote(remote string) (string, string, bool) {
	if slash := strings.Index(remote, "/"); slash >= 0 {
		if colon := strings.Index(remote, ":"); colon < 0 || slash < colon {
			return "", "", false
		}
	}
	hostPart, pathPart, ok := strings.Cut(remote, ":")
	if !ok || strings.TrimSpace(hostPart) == "" || strings.TrimSpace(pathPart) == "" {
		return "", "", false
	}
	return hostPart, pathPart, true
}

func canonicalizeAcceptedLineGitRemoteHostPath(hostPart string, pathPart string, defaultPort string) string {
	if _, after, ok := strings.Cut(strings.TrimSpace(hostPart), "@"); ok {
		hostPart = after
	}
	host := strings.ToLower(strings.TrimRight(strings.TrimSpace(hostPart), "/"))
	if defaultPort != "" {
		if hostWithoutPort, port, ok := strings.Cut(strings.ToLower(host), ":"); ok && port == defaultPort {
			host = hostWithoutPort
		}
	}
	if host == "" {
		return ""
	}
	pathPart = trimAcceptedLineGitSuffix(strings.Trim(strings.TrimSpace(pathPart), "/"))
	components := make([]string, 0, 2)
	for _, component := range strings.Split(pathPart, "/") {
		component = strings.TrimSpace(component)
		if component != "" {
			components = append(components, component)
		}
	}
	if len(components) < 2 || components[0] == "." || components[0] == ".." || components[1] == "." || components[1] == ".." {
		return ""
	}
	path := strings.Join(components, "/")
	if host == "github.com" {
		path = strings.ToLower(path)
	}
	return host + "/" + path
}

func trimAcceptedLineGitSuffix(value string) string {
	return strings.TrimSuffix(value, ".git")
}

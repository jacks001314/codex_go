package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

// gitCredentialRequestLimit mirrors Rust's 16 KiB + 1 read cap in
// read_request: a larger first record fails instead of being truncated.
const gitCredentialRequestLimit = 16*1024 + 1

const gitCredentialSitesPathPrefix = "backend-api/codex/sites/git/"

var (
	errGitCredentialConfigUnavailable = errors.New("Codex authentication configuration is unavailable")
	errGitCredentialNeedsFileLogin    = errors.New("Codex Git authentication requires a file-backed ChatGPT login for this origin")
	errGitCredentialAuthUnavailable   = errors.New("Codex authentication is unavailable")
	errGitCredentialProtectedLogin    = errors.New("Git cannot verify Codex workspace policy using a protected login")
	errGitCredentialPolicyRejected    = errors.New("Codex workspace policy does not permit this Git login")
	errGitCredentialLoginChanged      = errors.New("Codex login changed or expired while checking Git access")
)

type gitCredentialOutcome int

const (
	gitCredentialIgnore gitCredentialOutcome = iota
	gitCredentialQuit
	gitCredentialToken
)

// runGitCredential implements Rust #60746 (cli/src/git_credential.rs): supply
// the existing Codex login to Git's credential-helper protocol. Git falls back
// to prompting after helper errors, so every failure ends with an explicit
// quit=true record.
func runGitCredential(ctx context.Context, opts cli.GitCredentialOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if opts.Operation == "capability" {
		_, err := fmt.Fprint(stdout, "version 0\ncapability authtype\n")
		return err
	}
	request, readErr := readGitCredentialRequest(stdin)
	outcome := gitCredentialIgnore
	var token string
	var err error
	if readErr == nil && opts.Operation == "get" {
		token, outcome, err = resolveGitCredential(ctx, opts.ConfigOverrides, request)
	} else if readErr != nil {
		err = readErr
	}
	if err != nil {
		if stderr != nil {
			fmt.Fprintln(stderr, err)
		}
		printGitCredentialQuit(stdout)
		return nil
	}
	switch outcome {
	case gitCredentialQuit:
		printGitCredentialQuit(stdout)
	case gitCredentialToken:
		if _, err := fmt.Fprintf(stdout, "capability[]=authtype\nauthtype=Bearer\ncredential=%s\nephemeral=1\n\n", token); err != nil {
			return err
		}
	}
	return nil
}

func printGitCredentialQuit(stdout io.Writer) {
	if stdout != nil {
		fmt.Fprint(stdout, "quit=true\n\n")
	}
}

// readGitCredentialRequest consumes exactly one credential record, mirroring
// Rust's read_request loop: lines up to the first empty one, capped at 16 KiB.
func readGitCredentialRequest(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", nil
	}
	limited := io.LimitReader(stdin, gitCredentialRequestLimit)
	buf := make([]byte, 0, 512)
	chunk := make([]byte, 1)
	lineStart := 0
	for {
		n, err := limited.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if len(buf) >= gitCredentialRequestLimit {
				return "", errors.New("Git credential request is too large")
			}
			if chunk[0] == '\n' {
				line := string(buf[lineStart:])
				lineStart = len(buf)
				if line == "\n" || line == "\r\n" {
					return string(buf), nil
				}
			}
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return string(buf), nil
			}
			return "", err
		}
	}
}

func gitCredentialFields(request string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(request, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSuffix(key, "\r")
		value = strings.TrimSuffix(value, "\r")
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return fields
}

func resolveGitCredential(ctx context.Context, overrides []string, request string) (string, gitCredentialOutcome, error) {
	fields := gitCredentialFields(request)
	if fields["capability[]"] != "authtype" || fields["protocol"] != "https" {
		return "", gitCredentialIgnore, nil
	}
	host, path := fields["host"], fields["path"]
	if host == "" || path == "" {
		return "", gitCredentialIgnore, nil
	}
	if !strings.HasPrefix(path, gitCredentialSitesPathPrefix) || gitCredentialPathIsUnsafe(path) {
		return "", gitCredentialIgnore, nil
	}
	destination, err := url.Parse("https://" + host + "/" + path)
	if err != nil {
		return "", gitCredentialIgnore, nil
	}
	if !gitCredentialDestinationAllowed(destination) {
		return "", gitCredentialIgnore, nil
	}
	return gitTokenForOrigin(ctx, overrides, destination)
}

// gitCredentialPathIsUnsafe rejects control characters and the separators Git
// normalizes away, plus `.`/`..` segments (Rust #60746).
func gitCredentialPathIsUnsafe(path string) bool {
	for _, r := range path {
		if unicode.IsControl(r) || r == '\\' || r == '?' || r == '#' {
			return true
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func gitCredentialDestinationAllowed(destination *url.URL) bool {
	if destination == nil {
		return false
	}
	host := strings.ToLower(destination.Hostname())
	if host != "chatgpt.com" && host != "chatgpt-staging.com" {
		return false
	}
	port := destination.Port()
	if port != "" && port != "443" {
		return false
	}
	if destination.User != nil && destination.User.String() != "" {
		return false
	}
	if !strings.HasPrefix(destination.Path, "/"+gitCredentialSitesPathPrefix) {
		return false
	}
	return destination.Scheme == "https"
}

// gitTokenForOrigin reads a file-backed ChatGPT login and refuses anything Git
// must not touch: keyring/keyring-backed logins, workload identities, other API
// origins, FedRAMP accounts, expired tokens and config-server protected logins.
func gitTokenForOrigin(ctx context.Context, overrides []string, destination *url.URL) (string, gitCredentialOutcome, error) {
	codexHome := auth.DefaultCodexHome()
	loadOpts := &config.EffectiveOptions{
		RawOverrides:        append([]string(nil), overrides...),
		IgnoreProjectConfig: true,
	}
	loaded, err := config.LoadEffectiveWithOptions(codexHome, loadOpts)
	if err != nil {
		return "", gitCredentialIgnore, errGitCredentialConfigUnavailable
	}
	matches := func(cfg *config.Config) bool {
		return cfg.CLIAuthCredentialsStoreMode() == "file" && gitBaseOriginMatches(cfg.ChatGPTBaseURL(), destination)
	}
	if !matches(loaded) || auth.IsWorkloadIdentitySelected() {
		return "", gitCredentialIgnore, errGitCredentialNeedsFileLogin
	}
	snapshot, err := auth.NewStoreWithOptions(codexHome, authStoreOptionsFromLoadedConfig(loaded)).Load()
	if err != nil {
		return "", gitCredentialIgnore, errGitCredentialAuthUnavailable
	}
	if snapshot == nil || snapshot.AuthMode != "chatgpt" || auth.IsFedrampAccount(snapshot) {
		return "", gitCredentialQuit, nil
	}
	token := auth.ChatGPTAccessTokenFromAuth(snapshot)
	if token == "" || !gitCredentialTokenPrintable(token) {
		return "", gitCredentialQuit, nil
	}
	expiry, ok := auth.JWTAccessTokenExpiration(token)
	if !ok || !expiry.After(time.Now()) {
		return "", gitCredentialQuit, nil
	}
	if loaded.Requirements != nil && loaded.Requirements.ConfigServer != nil && loaded.Requirements.ConfigServer.OAuth != nil {
		return "", gitCredentialIgnore, errGitCredentialProtectedLogin
	}
	if loaded.RequiresRemoteConfig() {
		bundle, err := cloudConfigBundleLoaderForRun(ctx, codexHome, loaded, snapshot)
		if err != nil {
			return "", gitCredentialIgnore, errGitCredentialConfigUnavailable
		}
		managedOpts := &config.EffectiveOptions{
			RawOverrides:         append([]string(nil), overrides...),
			IgnoreProjectConfig:  true,
			IncludeManagedConfig: true,
			CloudConfigBundle:    bundle,
		}
		effective, err := loadEffectiveWithAppliedConfigServer(codexHome, managedOpts, bundle)
		if err != nil {
			return "", gitCredentialIgnore, errGitCredentialConfigUnavailable
		}
		if !matches(effective) || !effective.IsLoginMethodAllowed(config.ForcedLoginMethodChatGPT) {
			return "", gitCredentialIgnore, errGitCredentialPolicyRejected
		}
	}
	if expiry, ok := auth.JWTAccessTokenExpiration(token); ok && !expiry.After(time.Now()) {
		return "", gitCredentialIgnore, errGitCredentialLoginChanged
	}
	return token, gitCredentialToken, nil
}

func gitBaseOriginMatches(baseURL string, destination *url.URL) bool {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base == nil || destination == nil {
		return false
	}
	return gitOriginKey(base) == gitOriginKey(destination)
}

func gitOriginKey(target *url.URL) string {
	scheme := strings.ToLower(target.Scheme)
	host := strings.ToLower(target.Hostname())
	port := target.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		}
	}
	return scheme + "://" + host + ":" + port
}

func gitCredentialTokenPrintable(token string) bool {
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7e {
			return false
		}
	}
	return true
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/mcp"
)

// Rust #43708 for the embedded (in-process) TUI. The remote client reaches the
// user-verification provider through the app-server's `userVerification/verify`
// RPC; the local TUI owns an in-process app-server runtime instead, so it issues
// the same RPC against that runtime. The attempt ledger in
// UserVerificationRequests is shared by both paths, so a local request also
// refuses to sign twice and drops proofs that finish after the prompt was
// resolved.
const interactiveUserVerificationConnectionID = "local-tui-user-verification"

// errUserVerificationProviderUnavailable reports a build whose app-server has no
// native user-verification backend (Rust's `providerUnavailable`).
var errUserVerificationProviderUnavailable = errors.New("user verification provider unavailable")

// interactiveUserVerificationProvider signs one accepted user-verification
// challenge for the local TUI.
type interactiveUserVerificationProvider interface {
	// Available reports whether this build can sign at all. An unavailable
	// provider must not publish a prompt the TUI could never satisfy.
	Available() bool
	// Verify authenticates the user and signs the validated challenge.
	Verify(ctx context.Context, request *mcp.MCPElicitationRequest) (appserver.UserVerificationProof, error)
}

// interactiveAppServerUserVerificationProvider resolves the proof through the
// in-process app-server runtime, which installs the platform provider.
type interactiveAppServerUserVerificationProvider struct {
	codexHome string

	mu           sync.Mutex
	availability *bool
}

// interactiveLocalUserVerificationProvider builds the embedded TUI's signer.
func interactiveLocalUserVerificationProvider(codexHome string) interactiveUserVerificationProvider {
	return &interactiveAppServerUserVerificationProvider{codexHome: strings.TrimSpace(codexHome)}
}

// Available probes `userVerification/status` once per session: a PendingVerification
// prompt is only worth publishing when the runtime reports a usable provider.
func (p *interactiveAppServerUserVerificationProvider) Available() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.availability != nil {
		return *p.availability
	}
	available := p.probeAvailability()
	p.availability = &available
	return available
}

func (p *interactiveAppServerUserVerificationProvider) probeAvailability() bool {
	codexHome := p.codexHome
	if codexHome == "" {
		codexHome = auth.DefaultCodexHome()
	}
	router := appserver.NewDefaultRuntimeRouter(newSessionStore(), codexHome)
	defer router.Close()
	if err := initializeLocalTUIConnection(router.Handle, interactiveUserVerificationConnectionID); err != nil {
		return false
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(2),
		Method:       appserver.MethodUserVerificationStatus,
		ConnectionID: interactiveUserVerificationConnectionID,
	})
	if response == nil || response.Error != nil {
		return false
	}
	status, ok := response.Result.(*appserver.UserVerificationStatusResponse)
	if !ok || status == nil {
		return false
	}
	return status.UnavailableReason == nil
}

// Verify issues the same request the remote TUI sends (app/remote_tui.go
// verifyUserVerification), so both paths share the provider contract.
func (p *interactiveAppServerUserVerificationProvider) Verify(ctx context.Context, request *mcp.MCPElicitationRequest) (appserver.UserVerificationProof, error) {
	if p == nil || request == nil {
		return appserver.UserVerificationProof{}, errUserVerificationProviderUnavailable
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return appserver.UserVerificationProof{}, err
		}
	}
	codexHome := p.codexHome
	if codexHome == "" {
		codexHome = auth.DefaultCodexHome()
	}
	params, err := json.Marshal(appserver.UserVerificationVerifyParams{
		Challenge:   strings.TrimSpace(request.Challenge),
		Title:       strings.TrimSpace(request.Title),
		Description: strings.TrimSpace(request.Description),
	})
	if err != nil {
		return appserver.UserVerificationProof{}, err
	}
	router := appserver.NewDefaultRuntimeRouter(newSessionStore(), codexHome)
	defer router.Close()
	if err := initializeLocalTUIConnection(router.Handle, interactiveUserVerificationConnectionID); err != nil {
		return appserver.UserVerificationProof{}, err
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(3),
		Method:       appserver.MethodUserVerificationVerify,
		Params:       params,
		ConnectionID: interactiveUserVerificationConnectionID,
	})
	if response == nil {
		return appserver.UserVerificationProof{}, errors.New("userVerification/verify returned no response")
	}
	if response.Error != nil {
		if userVerificationUnavailableResponse(response.Error) {
			return appserver.UserVerificationProof{}, errUserVerificationProviderUnavailable
		}
		return appserver.UserVerificationProof{}, errors.New(strings.TrimSpace(response.Error.Message))
	}
	result, ok := response.Result.(*appserver.UserVerificationVerifyResponse)
	if !ok || result == nil {
		return appserver.UserVerificationProof{}, errors.New("userVerification/verify returned an invalid response")
	}
	return result.Proof, nil
}

// userVerificationUnavailableResponse recognizes the app-server's
// `providerUnavailable` failure without matching on display text.
func userVerificationUnavailableResponse(err *appserver.ResponseError) bool {
	if err == nil || err.Data == nil {
		return false
	}
	typeName, _ := err.Data["type"].(string)
	reason, _ := err.Data["reason"].(string)
	return typeName == "unavailable" ||
		reason == string(appserver.UserVerificationUnavailableProviderUnavailable)
}

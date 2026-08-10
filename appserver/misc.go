package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"codex_go/envutil"
	"codex_go/filesearch"
)

var ErrInvalidMiscRequest = errors.New("invalid misc request")

type AuthStatusParams struct {
	IncludeToken *bool `json:"includeToken,omitempty"`
	RefreshToken *bool `json:"refreshToken,omitempty"`
}

func (p *AuthStatusParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		IncludeToken *bool `json:"includeToken"`
		RefreshToken *bool `json:"refreshToken"`
	}{
		IncludeToken: p.IncludeToken,
		RefreshToken: p.RefreshToken,
	})
}

type AuthStatusResponse struct {
	AuthMethod         *string `json:"authMethod"`
	AuthToken          *string `json:"authToken"`
	RequiresOpenAIAuth *bool   `json:"requiresOpenaiAuth"`
	Authenticated      bool    `json:"authenticated"`
	Mode               string  `json:"mode,omitempty"`
	AccountID          string  `json:"accountId,omitempty"`
}

func (r *AuthStatusResponse) MarshalJSON() ([]byte, error) {
	r.normalizeLegacy()
	return json.Marshal(struct {
		AuthMethod         *string `json:"authMethod"`
		AuthToken          *string `json:"authToken"`
		RequiresOpenAIAuth *bool   `json:"requiresOpenaiAuth"`
	}{
		AuthMethod:         r.AuthMethod,
		AuthToken:          r.AuthToken,
		RequiresOpenAIAuth: r.RequiresOpenAIAuth,
	})
}

type ConversationSummaryParams struct {
	ConversationID string `json:"conversationId,omitempty"`
	RolloutPath    string `json:"rolloutPath,omitempty"`
	ThreadID       string `json:"threadId,omitempty"`
}

func (p ConversationSummaryParams) MarshalJSON() ([]byte, error) {
	if rolloutPath := strings.TrimSpace(p.RolloutPath); rolloutPath != "" {
		return json.Marshal(struct {
			RolloutPath string `json:"rolloutPath"`
		}{RolloutPath: rolloutPath})
	}
	return json.Marshal(struct {
		ConversationID string `json:"conversationId"`
	}{ConversationID: p.LookupConversationID()})
}

func (p *ConversationSummaryParams) LookupConversationID() string {
	if p == nil {
		return ""
	}
	if conversationID := strings.TrimSpace(p.ConversationID); conversationID != "" {
		return conversationID
	}
	return strings.TrimSpace(p.ThreadID)
}

type ConversationSummaryResponse struct {
	Summary     string               `json:"summary"`
	SummaryData *ConversationSummary `json:"-"`
}

func (r ConversationSummaryResponse) MarshalJSON() ([]byte, error) {
	summary := r.SummaryData
	if summary == nil {
		summary = conversationSummaryFromText(r.Summary, "")
	}
	return json.Marshal(struct {
		Summary *ConversationSummary `json:"summary"`
	}{Summary: summary})
}

type GitDiffToRemoteParams struct {
	CWD    string `json:"cwd,omitempty"`
	Remote string `json:"remote,omitempty"`
}

func (p GitDiffToRemoteParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		CWD string `json:"cwd"`
	}{CWD: p.CWD})
}

type GitDiffToRemoteResponse struct {
	SHA  string `json:"sha"`
	Diff string `json:"diff"`
}

type FuzzyFileSearchParams struct {
	Query             string   `json:"query"`
	CWD               string   `json:"cwd,omitempty"`
	Roots             []string `json:"roots,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	Exclude           []string `json:"exclude,omitempty"`
	CancellationToken *string  `json:"cancellationToken,omitempty"`
}

func (p *FuzzyFileSearchParams) MarshalJSON() ([]byte, error) {
	roots := append([]string(nil), p.Roots...)
	if roots == nil {
		roots = []string{}
	}
	return json.Marshal(struct {
		Query             string   `json:"query"`
		Roots             []string `json:"roots"`
		CancellationToken *string  `json:"cancellationToken"`
	}{
		Query:             p.Query,
		Roots:             roots,
		CancellationToken: cloneStringPtrAppserver(p.CancellationToken),
	})
}

const fuzzyFileSearchMatchLimit = 50

type FuzzyFileSearchResponse struct {
	Files []filesearch.FileMatch `json:"files"`
}

func (r *FuzzyFileSearchResponse) MarshalJSON() ([]byte, error) {
	files := append([]filesearch.FileMatch(nil), r.Files...)
	if files == nil {
		files = []filesearch.FileMatch{}
	}
	return json.Marshal(struct {
		Files []filesearch.FileMatch `json:"files"`
	}{Files: files})
}

type FuzzyFileSearchSessionStartParams struct {
	SessionID string   `json:"sessionId"`
	Roots     []string `json:"roots"`
}

func (p *FuzzyFileSearchSessionStartParams) MarshalJSON() ([]byte, error) {
	roots := append([]string(nil), p.Roots...)
	if roots == nil {
		roots = []string{}
	}
	return json.Marshal(struct {
		SessionID string   `json:"sessionId"`
		Roots     []string `json:"roots"`
	}{
		SessionID: p.SessionID,
		Roots:     roots,
	})
}

type FuzzyFileSearchSessionStartResponse struct{}

type FuzzyFileSearchSessionUpdateParams struct {
	SessionID string `json:"sessionId"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
}

func (p *FuzzyFileSearchSessionUpdateParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		SessionID string `json:"sessionId"`
		Query     string `json:"query"`
	}{
		SessionID: p.SessionID,
		Query:     p.Query,
	})
}

type FuzzyFileSearchSessionUpdateResponse struct {
	Files     []filesearch.FileMatch `json:"-"`
	Notify    bool                   `json:"-"`
	SessionID string                 `json:"-"`
	Query     string                 `json:"-"`
}

func (r *FuzzyFileSearchSessionUpdateResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{}{})
}

type FuzzyFileSearchSessionStopParams struct {
	SessionID string `json:"sessionId"`
}

type FuzzyFileSearchSessionStopResponse struct{}

type fuzzySearchCancellation struct {
	cancel context.CancelFunc
}

type fuzzySearchSessionState struct {
	roots       []string
	latestQuery string
	generation  uint64
	cancel      context.CancelFunc
}

type MiscService struct {
	mu                   sync.Mutex
	auth                 AuthStatusResponse
	authSet              bool
	pendingFuzzySearches map[string]*fuzzySearchCancellation
	searchSessions       map[string]*fuzzySearchSessionState
}

func NewMiscService() *MiscService {
	return &MiscService{
		pendingFuzzySearches: map[string]*fuzzySearchCancellation{},
		searchSessions:       map[string]*fuzzySearchSessionState{},
	}
}

func (r *AuthStatusResponse) normalizeLegacy() {
	if r == nil {
		return
	}
	if r.AuthMethod == nil && strings.TrimSpace(r.Mode) != "" {
		mode := strings.TrimSpace(r.Mode)
		r.AuthMethod = &mode
	}
	if strings.TrimSpace(r.Mode) == "" && r.AuthMethod != nil {
		r.Mode = *r.AuthMethod
	}
	if r.AuthMethod != nil {
		r.Authenticated = true
	}
	if r.RequiresOpenAIAuth == nil {
		value := true
		r.RequiresOpenAIAuth = &value
	}
}

func (s *MiscService) SetAuthStatus(status AuthStatusResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status.normalizeLegacy()
	s.auth = status
	s.authSet = true
}

func (s *MiscService) HasAuthStatus() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authSet
}

func (s *MiscService) AuthStatus(params *AuthStatusParams) *AuthStatusResponse {
	if params == nil {
		params = &AuthStatusParams{}
	}
	s.mu.Lock()
	status := s.auth
	s.mu.Unlock()
	status.normalizeLegacy()
	if params.IncludeToken == nil || !*params.IncludeToken {
		status.AuthToken = nil
	}
	return &status
}

func (s *MiscService) ConversationSummary(params *ConversationSummaryParams) (*ConversationSummaryResponse, error) {
	if params == nil {
		params = &ConversationSummaryParams{}
	}
	conversationID := params.LookupConversationID()
	if conversationID == "" && strings.TrimSpace(params.RolloutPath) == "" {
		return nil, fmt.Errorf("%w: conversationId or rolloutPath is required", ErrInvalidMiscRequest)
	}
	return &ConversationSummaryResponse{SummaryData: conversationSummaryFromText("", conversationID)}, nil
}

func (s *MiscService) GitDiffToRemote(params *GitDiffToRemoteParams) (*GitDiffToRemoteResponse, error) {
	if params == nil {
		params = &GitDiffToRemoteParams{}
	}
	cwd := strings.TrimSpace(params.CWD)
	if cwd == "" {
		return nil, fmt.Errorf("%w: cwd is required", ErrInvalidMiscRequest)
	}
	if info, err := os.Stat(cwd); err != nil {
		return nil, fmt.Errorf("%w: invalid cwd: %w", ErrInvalidMiscRequest, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%w: cwd is not a directory", ErrInvalidMiscRequest)
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	upstream, _ := gitOutput(cwd, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	candidates := gitRemoteCandidates(cwd, remote, upstream)
	for _, candidate := range candidates {
		sha, err := gitOutput(cwd, "merge-base", "HEAD", candidate)
		if err != nil || strings.TrimSpace(sha) == "" {
			continue
		}
		sha = strings.TrimSpace(sha)
		diff, err := gitOutput(cwd, "diff", "--no-ext-diff", "--binary", sha+"...HEAD")
		if err != nil {
			return nil, fmt.Errorf("%w: failed to compute git diff to remote for cwd %q: %w", ErrInvalidMiscRequest, cwd, err)
		}
		return &GitDiffToRemoteResponse{SHA: sha, Diff: diff}, nil
	}
	return nil, fmt.Errorf("%w: failed to compute git diff to remote for cwd %q", ErrInvalidMiscRequest, cwd)
}

func (s *MiscService) FuzzyFileSearch(ctx context.Context, params *FuzzyFileSearchParams) (*FuzzyFileSearchResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidMiscRequest)
	}
	searchCtx, finish, tokenScoped := s.beginFuzzyFileSearch(ctx, params.CancellationToken)
	defer finish()
	if params.Query == "" {
		return &FuzzyFileSearchResponse{Files: []filesearch.FileMatch{}}, nil
	}
	roots := append([]string(nil), params.Roots...)
	if len(roots) == 0 && strings.TrimSpace(params.CWD) != "" {
		roots = append(roots, params.CWD)
	}
	if len(roots) == 0 {
		return &FuzzyFileSearchResponse{Files: []filesearch.FileMatch{}}, nil
	}
	results, err := filesearch.Run(searchCtx, params.Query, roots, filesearch.Options{Limit: fuzzyFileSearchMatchLimit, ComputeIndices: true, IncludeHidden: true})
	if err != nil {
		if tokenScoped && errors.Is(err, context.Canceled) {
			return &FuzzyFileSearchResponse{Files: []filesearch.FileMatch{}}, nil
		}
		return nil, err
	}
	return &FuzzyFileSearchResponse{Files: results.Matches}, nil
}

func (s *MiscService) beginFuzzyFileSearch(ctx context.Context, token *string) (context.Context, func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if token == nil {
		return ctx, func() {}, false
	}
	tokenValue := *token
	searchCtx, cancel := context.WithCancel(ctx)
	state := &fuzzySearchCancellation{cancel: cancel}
	s.mu.Lock()
	if s.pendingFuzzySearches == nil {
		s.pendingFuzzySearches = map[string]*fuzzySearchCancellation{}
	}
	if existing := s.pendingFuzzySearches[tokenValue]; existing != nil && existing.cancel != nil {
		existing.cancel()
	}
	s.pendingFuzzySearches[tokenValue] = state
	s.mu.Unlock()
	return searchCtx, func() {
		s.mu.Lock()
		if s.pendingFuzzySearches[tokenValue] == state {
			delete(s.pendingFuzzySearches, tokenValue)
		}
		s.mu.Unlock()
		cancel()
	}, true
}

func (s *MiscService) FuzzyFileSearchSessionStart(params *FuzzyFileSearchSessionStartParams) (*FuzzyFileSearchSessionStartResponse, error) {
	if params == nil {
		params = &FuzzyFileSearchSessionStartParams{}
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId must not be empty", ErrInvalidMiscRequest)
	}
	roots := append([]string(nil), params.Roots...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.searchSessions == nil {
		s.searchSessions = map[string]*fuzzySearchSessionState{}
	}
	if existing := s.searchSessions[sessionID]; existing != nil && existing.cancel != nil {
		existing.cancel()
	}
	s.searchSessions[sessionID] = &fuzzySearchSessionState{roots: roots}
	return &FuzzyFileSearchSessionStartResponse{}, nil
}

func (s *MiscService) FuzzyFileSearchSessionUpdate(ctx context.Context, params *FuzzyFileSearchSessionUpdateParams) (*FuzzyFileSearchSessionUpdateResponse, error) {
	if params == nil {
		params = &FuzzyFileSearchSessionUpdateParams{}
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId must not be empty", ErrInvalidMiscRequest)
	}
	s.mu.Lock()
	session, ok := s.searchSessions[sessionID]
	if ok {
		if session.cancel != nil {
			session.cancel()
		}
		session.generation++
		session.latestQuery = params.Query
	}
	generation := uint64(0)
	roots := []string(nil)
	var cancel context.CancelFunc
	searchCtx := ctx
	if searchCtx == nil {
		searchCtx = context.Background()
	}
	if ok {
		generation = session.generation
		roots = append([]string(nil), session.roots...)
		searchCtx, cancel = context.WithCancel(searchCtx)
		session.cancel = cancel
	}
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: fuzzy file search session not found: %s", ErrInvalidMiscRequest, sessionID)
	}
	if params.Query == "" {
		if cancel != nil {
			cancel()
		}
		notify := s.finishFuzzyFileSearchSessionUpdate(sessionID, session, generation, cancel)
		return &FuzzyFileSearchSessionUpdateResponse{Files: []filesearch.FileMatch{}, Notify: notify, SessionID: sessionID, Query: params.Query}, nil
	}
	if len(roots) == 0 {
		if cancel != nil {
			cancel()
		}
		notify := s.finishFuzzyFileSearchSessionUpdate(sessionID, session, generation, cancel)
		return &FuzzyFileSearchSessionUpdateResponse{Files: []filesearch.FileMatch{}, Notify: notify, SessionID: sessionID, Query: params.Query}, nil
	}
	results, err := filesearch.Run(searchCtx, params.Query, roots, filesearch.Options{Limit: fuzzyFileSearchMatchLimit, ComputeIndices: true, IncludeHidden: true})
	notify := s.finishFuzzyFileSearchSessionUpdate(sessionID, session, generation, cancel)
	if err != nil {
		if errors.Is(err, context.Canceled) && !notify {
			return &FuzzyFileSearchSessionUpdateResponse{Files: []filesearch.FileMatch{}, Notify: false, SessionID: sessionID, Query: params.Query}, nil
		}
		return nil, err
	}
	return &FuzzyFileSearchSessionUpdateResponse{Files: results.Matches, Notify: notify, SessionID: sessionID, Query: params.Query}, nil
}

func (s *MiscService) finishFuzzyFileSearchSessionUpdate(sessionID string, session *fuzzySearchSessionState, generation uint64, _ context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.searchSessions[sessionID]
	if current != session || current == nil || current.generation != generation {
		return false
	}
	current.cancel = nil
	return true
}

func (s *MiscService) FuzzyFileSearchSessionStop(params *FuzzyFileSearchSessionStopParams) (*FuzzyFileSearchSessionStopResponse, error) {
	if params == nil {
		params = &FuzzyFileSearchSessionStopParams{}
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId must not be empty", ErrInvalidMiscRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.searchSessions[sessionID]; session != nil && session.cancel != nil {
		session.cancel()
	}
	delete(s.searchSessions, sessionID)
	return &FuzzyFileSearchSessionStopResponse{}, nil
}

func gitRemoteCandidates(cwd string, remote string, upstream string) []string {
	seen := map[string]bool{}
	add := func(out *[]string, value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		*out = append(*out, value)
		seen[value] = true
	}
	var candidates []string
	add(&candidates, upstream)
	if head, err := gitOutput(cwd, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		add(&candidates, head)
	}
	add(&candidates, remote+"/main")
	add(&candidates, remote+"/master")
	return candidates
}

func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	envutil.ScrubCommandEnv(cmd)
	stdout, stderr, err := runCommandCaptured(cmd)
	if err != nil {
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = strings.TrimSpace(stdout)
		}
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), message)
	}
	return strings.TrimRight(stdout, "\n"), nil
}

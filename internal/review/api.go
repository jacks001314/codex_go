package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidRequest = errors.New("invalid review request")

type APITarget struct {
	Type         string  `json:"type,omitempty"`
	Base         string  `json:"base,omitempty"`
	Branch       string  `json:"branch,omitempty"`
	Head         string  `json:"head,omitempty"`
	SHA          string  `json:"sha,omitempty"`
	Title        *string `json:"title,omitempty"`
	Commit       string  `json:"commit,omitempty"`
	Prompt       string  `json:"prompt,omitempty"`
	Instructions string  `json:"instructions,omitempty"`
	ThreadID     string  `json:"threadId,omitempty"`
}

func (t *APITarget) MarshalJSON() ([]byte, error) {
	switch strings.TrimSpace(t.Type) {
	case "base", "baseBranch":
		branch := strings.TrimSpace(t.Branch)
		if branch == "" {
			branch = strings.TrimSpace(t.Base)
		}
		return json.Marshal(struct {
			Type   string `json:"type"`
			Branch string `json:"branch"`
		}{Type: "baseBranch", Branch: branch})
	case "commit":
		sha := strings.TrimSpace(t.SHA)
		if sha == "" {
			sha = strings.TrimSpace(t.Commit)
		}
		return json.Marshal(struct {
			Type  string  `json:"type"`
			SHA   string  `json:"sha"`
			Title *string `json:"title"`
		}{Type: "commit", SHA: sha, Title: t.Title})
	case "custom":
		instructions := strings.TrimSpace(t.Instructions)
		if instructions == "" {
			instructions = strings.TrimSpace(t.Prompt)
		}
		return json.Marshal(struct {
			Type         string `json:"type"`
			Instructions string `json:"instructions"`
		}{Type: "custom", Instructions: instructions})
	default:
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: "uncommittedChanges"})
	}
}

type StartParams struct {
	ThreadID string    `json:"threadId"`
	Target   APITarget `json:"target"`
	Delivery *string   `json:"delivery,omitempty"`
}

func (p *StartParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID string    `json:"threadId"`
		Target   APITarget `json:"target"`
		Delivery *string   `json:"delivery,omitempty"`
	}{
		ThreadID: p.ThreadID,
		Target:   p.Target,
		Delivery: p.Delivery,
	})
}

func (p *StartParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if err := p.Target.Validate(); err != nil {
		return err
	}
	if p.Delivery != nil {
		switch strings.TrimSpace(*p.Delivery) {
		case "", "inline", "detached":
		default:
			return fmt.Errorf("%w: unsupported review delivery %q", ErrInvalidRequest, *p.Delivery)
		}
	}
	return nil
}

func (t *APITarget) Validate() error {
	if t == nil {
		return nil
	}
	switch strings.TrimSpace(t.Type) {
	case "", "diff", "uncommitted", "uncommittedChanges":
		return nil
	case "base", "baseBranch":
		if strings.TrimSpace(t.Base) == "" && strings.TrimSpace(t.Branch) == "" {
			return fmt.Errorf("%w: review target branch is required", ErrInvalidRequest)
		}
	case "commit":
		if strings.TrimSpace(t.Commit) == "" && strings.TrimSpace(t.SHA) == "" {
			return fmt.Errorf("%w: review target sha is required", ErrInvalidRequest)
		}
	case "custom":
		if strings.TrimSpace(t.Prompt) == "" && strings.TrimSpace(t.Instructions) == "" {
			return fmt.Errorf("%w: custom review instructions are required", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported review target type %q", ErrInvalidRequest, t.Type)
	}
	return nil
}

func (t *APITarget) ToTarget() Target {
	if t == nil {
		return Target{Kind: "uncommitted"}
	}
	switch strings.TrimSpace(t.Type) {
	case "base", "baseBranch":
		base := strings.TrimSpace(t.Base)
		if base == "" {
			base = strings.TrimSpace(t.Branch)
		}
		return Target{Kind: "base", Base: base}
	case "commit":
		commit := strings.TrimSpace(t.Commit)
		if commit == "" {
			commit = strings.TrimSpace(t.SHA)
		}
		return Target{Kind: "commit", Commit: commit}
	case "custom":
		prompt := strings.TrimSpace(t.Prompt)
		if prompt == "" {
			prompt = strings.TrimSpace(t.Instructions)
		}
		return Target{Kind: "custom", Instructions: prompt}
	default:
		return Target{Kind: "uncommitted"}
	}
}

const (
	TurnStatusInProgress  = "inProgress"
	TurnStatusCompleted   = "completed"
	TurnStatusInterrupted = "interrupted"
	TurnStatusFailed      = "failed"
)

type Turn struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"`
}

func (t *Turn) MarshalJSON() ([]byte, error) {
	status := normalizeTurnStatus(t.Status)
	return json.Marshal(struct {
		ID          string           `json:"id"`
		Items       []map[string]any `json:"items"`
		ItemsView   string           `json:"itemsView"`
		Status      string           `json:"status"`
		Error       any              `json:"error"`
		StartedAt   *int64           `json:"startedAt"`
		CompletedAt *int64           `json:"completedAt"`
		DurationMS  *int64           `json:"durationMs"`
	}{
		ID:        t.ID,
		Items:     []map[string]any{},
		ItemsView: "notLoaded",
		Status:    status,
		Error:     nil,
		StartedAt: nil,
	})
}

type StartResponse struct {
	Turn           Turn   `json:"turn"`
	ReviewThreadID string `json:"reviewThreadId"`
}

type Service struct {
	now func() time.Time
}

func NewService() *Service {
	return &Service{now: time.Now}
}

func (s *Service) SetClock(clock func() time.Time) {
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *Service) Start(params *StartParams) (*StartResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	reviewThreadID := params.ThreadID
	if params.Delivery != nil && strings.TrimSpace(*params.Delivery) == "detached" {
		reviewThreadID = "review-" + params.ThreadID
	}
	return &StartResponse{Turn: Turn{
		ID:        "review-" + params.ThreadID,
		Status:    TurnStatusInProgress,
		StartedAt: s.now().UTC().Unix(),
	}, ReviewThreadID: reviewThreadID}, nil
}

func normalizeTurnStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "", "running":
		return TurnStatusInProgress
	case TurnStatusInProgress, TurnStatusCompleted, TurnStatusInterrupted, TurnStatusFailed:
		return status
	default:
		return status
	}
}

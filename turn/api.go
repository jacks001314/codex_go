package turn

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidTurnRequest   = errors.New("invalid turn request")
	ErrNoActiveTurnToSteer  = errors.New("no active turn to steer")
	ErrExpectedTurnMismatch = errors.New("expected active turn mismatch")
	ErrEmptyTurnSteerInput  = errors.New("empty turn steer input")
)

const (
	MaxUserInputTextChars = 1 << 20
	InputTooLargeCode     = "input_too_large"
)

type InputTooLargeError struct {
	MaxChars    int
	ActualChars int
}

func (e *InputTooLargeError) Error() string {
	maxChars := MaxUserInputTextChars
	if e != nil && e.MaxChars > 0 {
		maxChars = e.MaxChars
	}
	return fmt.Sprintf("Input exceeds the maximum length of %d characters.", maxChars)
}

func (e *InputTooLargeError) Unwrap() error {
	return ErrInvalidTurnRequest
}

func (e *InputTooLargeError) JSONRPCErrorData() map[string]any {
	maxChars := MaxUserInputTextChars
	actualChars := 0
	if e != nil {
		if e.MaxChars > 0 {
			maxChars = e.MaxChars
		}
		actualChars = e.ActualChars
	}
	return map[string]any{
		"input_error_code": InputTooLargeCode,
		"max_chars":        maxChars,
		"actual_chars":     actualChars,
	}
}

type TurnUserInput struct {
	Type         string        `json:"type,omitempty"`
	Text         string        `json:"text,omitempty"`
	TextElements []TextElement `json:"text_elements,omitempty"`
	Path         string        `json:"path,omitempty"`
	URL          string        `json:"url,omitempty"`
	Name         string        `json:"name,omitempty"`
	Detail       *string       `json:"detail,omitempty"`
	MimeType     string        `json:"mimeType,omitempty"`
}

type ByteRange struct {
	Start uint `json:"start"`
	End   uint `json:"end"`
}

type TextElement struct {
	ByteRange   ByteRange `json:"byteRange"`
	Placeholder *string   `json:"placeholder,omitempty"`
}

func (e TextElement) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ByteRange   ByteRange `json:"byteRange"`
		Placeholder *string   `json:"placeholder"`
	}{
		ByteRange:   e.ByteRange,
		Placeholder: cloneStringPtr(e.Placeholder),
	})
}

func (i *TurnUserInput) MarshalJSON() ([]byte, error) {
	inputType := strings.TrimSpace(i.Type)
	if inputType == "" {
		switch {
		case strings.TrimSpace(i.Text) != "":
			inputType = "text"
		case strings.TrimSpace(i.URL) != "":
			inputType = "image"
		case strings.TrimSpace(i.Path) != "":
			inputType = "localImage"
		}
	}
	switch inputType {
	case "text":
		textElements := append([]TextElement(nil), i.TextElements...)
		if textElements == nil {
			textElements = []TextElement{}
		}
		return json.Marshal(struct {
			Type         string        `json:"type"`
			Text         string        `json:"text"`
			TextElements []TextElement `json:"text_elements"`
		}{
			Type:         "text",
			Text:         i.Text,
			TextElements: textElements,
		})
	case "image":
		return json.Marshal(struct {
			Type   string  `json:"type"`
			Detail *string `json:"detail,omitempty"`
			URL    string  `json:"url"`
		}{
			Type:   "image",
			Detail: cloneStringPtr(i.Detail),
			URL:    i.URL,
		})
	case "localImage":
		return json.Marshal(struct {
			Type   string  `json:"type"`
			Detail *string `json:"detail,omitempty"`
			Path   string  `json:"path"`
		}{
			Type:   "localImage",
			Detail: cloneStringPtr(i.Detail),
			Path:   i.Path,
		})
	case "skill", "mention":
		return json.Marshal(struct {
			Type string `json:"type"`
			Name string `json:"name"`
			Path string `json:"path"`
		}{
			Type: inputType,
			Name: i.Name,
			Path: i.Path,
		})
	default:
		return json.Marshal(struct {
			Type         string        `json:"type,omitempty"`
			Text         string        `json:"text,omitempty"`
			TextElements []TextElement `json:"text_elements,omitempty"`
			Path         string        `json:"path,omitempty"`
			URL          string        `json:"url,omitempty"`
			Name         string        `json:"name,omitempty"`
			Detail       *string       `json:"detail,omitempty"`
			MimeType     string        `json:"mimeType,omitempty"`
		}{
			Type:         inputType,
			Text:         i.Text,
			TextElements: append([]TextElement(nil), i.TextElements...),
			Path:         i.Path,
			URL:          i.URL,
			Name:         i.Name,
			Detail:       cloneStringPtr(i.Detail),
			MimeType:     i.MimeType,
		})
	}
}

type TurnStartParams struct {
	ThreadID              string            `json:"threadId"`
	Input                 []TurnUserInput   `json:"input,omitempty"`
	Prompt                string            `json:"prompt,omitempty"`
	ClientUserMessageID   string            `json:"clientUserMessageId,omitempty"`
	ResponsesAPIMetadata  map[string]string `json:"responsesapiClientMetadata,omitempty"`
	CWD                   string            `json:"cwd,omitempty"`
	Model                 string            `json:"model,omitempty"`
	Originator            string            `json:"originator,omitempty"`
	ApprovalPolicy        any               `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer     *string           `json:"approvalsReviewer,omitempty"`
	SandboxPolicy         any               `json:"sandboxPolicy,omitempty"`
	Permissions           *string           `json:"permissions,omitempty"`
	RuntimeWorkspaceRoots []string          `json:"runtimeWorkspaceRoots,omitempty"`
	Environments          []map[string]any  `json:"environments,omitempty"`
	ServiceTier           *string           `json:"serviceTier,omitempty"`
	ServiceTierSet        bool              `json:"-"`
	Effort                *string           `json:"effort,omitempty"`
	Summary               *string           `json:"summary,omitempty"`
	OutputSchema          any               `json:"outputSchema,omitempty"`
	CollaborationMode     map[string]any    `json:"collaborationMode,omitempty"`
	// Deprecated: accepted for old app-server clients, but ignored by runtime.
	MultiAgentMode        *string                           `json:"multiAgentMode,omitempty"`
	Personality           *string                           `json:"personality,omitempty"`
	PersonalitySet        bool                              `json:"-"`
	Config                map[string]any                    `json:"config,omitempty"`
	BaseInstructions      *string                           `json:"baseInstructions,omitempty"`
	DeveloperInstructions *string                           `json:"developerInstructions,omitempty"`
	AdditionalContext     map[string]AdditionalContextEntry `json:"additionalContext,omitempty"`
	DynamicTools          []DynamicToolSpec                 `json:"dynamicTools,omitempty"`
	ExperimentalRawEvents bool                              `json:"-"`
}

func (p *TurnStartParams) CloneDynamicTools() []DynamicToolSpec {
	if p == nil || len(p.DynamicTools) == 0 {
		return nil
	}
	out := make([]DynamicToolSpec, len(p.DynamicTools))
	copy(out, p.DynamicTools)
	return out
}

func (p *TurnStartParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID            string          `json:"threadId"`
		ClientUserMessageID string          `json:"clientUserMessageId,omitempty"`
		Input               []TurnUserInput `json:"input"`
		CWD                 string          `json:"cwd,omitempty"`
		ApprovalPolicy      any             `json:"approvalPolicy,omitempty"`
		ApprovalsReviewer   *string         `json:"approvalsReviewer,omitempty"`
		SandboxPolicy       any             `json:"sandboxPolicy,omitempty"`
		Model               string          `json:"model,omitempty"`
		ServiceTier         *string         `json:"serviceTier,omitempty"`
		Effort              *string         `json:"effort,omitempty"`
		Summary             *string         `json:"summary,omitempty"`
		Personality         *string         `json:"personality,omitempty"`
		OutputSchema        any             `json:"outputSchema,omitempty"`
	}{
		ThreadID:            p.ThreadID,
		ClientUserMessageID: p.ClientUserMessageID,
		Input:               userInputsForJSONWithPrompt(p.Input, p.Prompt),
		CWD:                 p.CWD,
		ApprovalPolicy:      p.ApprovalPolicy,
		ApprovalsReviewer:   p.ApprovalsReviewer,
		SandboxPolicy:       p.SandboxPolicy,
		Model:               p.Model,
		ServiceTier:         p.ServiceTier,
		Effort:              p.Effort,
		Summary:             p.Summary,
		Personality:         p.Personality,
		OutputSchema:        p.OutputSchema,
	})
}

func (p *TurnStartParams) UnmarshalJSON(data []byte) error {
	type turnStartParamsAlias TurnStartParams
	var decoded turnStartParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if serviceTierRaw, ok := raw["serviceTier"]; ok {
		decoded.ServiceTierSet = true
		if strings.TrimSpace(string(serviceTierRaw)) == "null" {
			decoded.ServiceTier = nil
		} else {
			var serviceTier string
			if err := json.Unmarshal(serviceTierRaw, &serviceTier); err != nil {
				return err
			}
			decoded.ServiceTier = &serviceTier
		}
	}
	if _, ok := raw["personality"]; ok {
		decoded.PersonalitySet = true
	}
	*p = TurnStartParams(decoded)
	return nil
}

func (p *TurnStartParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidTurnRequest)
	}
	if err := ValidateDynamicTools(p.DynamicTools); err != nil {
		return err
	}
	if err := validateRuntimeWorkspaceRoots(p.RuntimeWorkspaceRoots); err != nil {
		return err
	}
	if err := validateUserInputTextLimit(p.Prompt, p.Input); err != nil {
		return err
	}
	return nil
}

func validateRuntimeWorkspaceRoots(roots []string) error {
	for _, root := range roots {
		if !isAbsoluteRuntimeWorkspaceRoot(root) {
			return fmt.Errorf("%w: runtimeWorkspaceRoots must contain absolute paths: %s", ErrInvalidTurnRequest, strings.TrimSpace(root))
		}
	}
	return nil
}

func isAbsoluteRuntimeWorkspaceRoot(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) {
		return true
	}
	return len(value) >= 3 && asciiLetter(value[0]) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func asciiLetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

type TurnSteerParams struct {
	ThreadID             string                            `json:"threadId"`
	ExpectedTurnID       string                            `json:"expectedTurnId"`
	Input                []TurnUserInput                   `json:"input,omitempty"`
	Prompt               string                            `json:"prompt,omitempty"`
	ClientUserMessageID  string                            `json:"clientUserMessageId,omitempty"`
	ResponsesAPIMetadata map[string]string                 `json:"responsesapiClientMetadata,omitempty"`
	AdditionalContext    map[string]AdditionalContextEntry `json:"additionalContext,omitempty"`
}

func (p *TurnSteerParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID            string          `json:"threadId"`
		ClientUserMessageID string          `json:"clientUserMessageId,omitempty"`
		Input               []TurnUserInput `json:"input"`
		ExpectedTurnID      string          `json:"expectedTurnId"`
	}{
		ThreadID:            p.ThreadID,
		ClientUserMessageID: p.ClientUserMessageID,
		Input:               userInputsForJSONWithPrompt(p.Input, p.Prompt),
		ExpectedTurnID:      p.ExpectedTurnID,
	})
}

func (p *TurnSteerParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidTurnRequest)
	}
	if strings.TrimSpace(p.ExpectedTurnID) == "" {
		return fmt.Errorf("%w: expectedTurnId must not be empty", ErrInvalidTurnRequest)
	}
	if err := validateUserInputTextLimit(p.Prompt, p.Input); err != nil {
		return err
	}
	return nil
}

type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

func (p *TurnInterruptParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" || strings.TrimSpace(p.TurnID) == "" {
		return fmt.Errorf("%w: threadId and turnId are required", ErrInvalidTurnRequest)
	}
	return nil
}

type TurnCompleteParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Status   string `json:"status,omitempty"`
}

func (p *TurnCompleteParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" || strings.TrimSpace(p.TurnID) == "" {
		return fmt.Errorf("%w: threadId and turnId are required", ErrInvalidTurnRequest)
	}
	return nil
}

type TurnStartResponse struct {
	Turn TurnRecord `json:"turn"`
}

type TurnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type TurnInterruptResponse struct{}

type TurnRecord struct {
	ID          string          `json:"id"`
	ThreadID    string          `json:"threadId"`
	Status      string          `json:"status"`
	Prompt      string          `json:"prompt,omitempty"`
	StartedAt   int64           `json:"startedAt"`
	CompletedAt *int64          `json:"completedAt"`
	Inputs      []TurnUserInput `json:"input,omitempty"`
}

func (r *TurnRecord) MarshalJSON() ([]byte, error) {
	status := normalizeTurnStatus(r.Status)
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
		ID:          r.ID,
		Items:       []map[string]any{},
		ItemsView:   "notLoaded",
		Status:      status,
		Error:       nil,
		StartedAt:   nil,
		CompletedAt: r.CompletedAt,
	})
}

const (
	TurnStatusInProgress  = "inProgress"
	TurnStatusCompleted   = "completed"
	TurnStatusInterrupted = "interrupted"
	TurnStatusFailed      = "failed"
)

type AdditionalContextKind string

const (
	AdditionalContextUntrusted   AdditionalContextKind = "untrusted"
	AdditionalContextApplication AdditionalContextKind = "application"
)

type AdditionalContextEntry struct {
	Value string                `json:"value"`
	Kind  AdditionalContextKind `json:"kind"`
}

type TurnService struct {
	mu     sync.Mutex
	nextID int
	active map[string]TurnRecord
	now    func() time.Time
}

func NewTurnService() *TurnService {
	return &TurnService{active: map[string]TurnRecord{}, now: time.Now}
}

func (s *TurnService) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *TurnService) Start(params *TurnStartParams) (*TurnStartResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	turn := TurnRecord{
		ID:        fmt.Sprintf("turn-%d", s.nextID),
		ThreadID:  params.ThreadID,
		Status:    TurnStatusInProgress,
		Prompt:    firstNonEmpty(params.Prompt, textFromInputs(params.Input)),
		StartedAt: s.now().UTC().Unix(),
		Inputs:    append([]TurnUserInput(nil), params.Input...),
	}
	s.active[params.ThreadID] = turn
	return &TurnStartResponse{Turn: turn}, nil
}

func (s *TurnService) Steer(params *TurnSteerParams) (*TurnSteerResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if len(params.Input) == 0 && strings.TrimSpace(params.Prompt) == "" {
		return nil, ErrEmptyTurnSteerInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.active[params.ThreadID]
	if !ok {
		return nil, ErrNoActiveTurnToSteer
	}
	if turn.ID != params.ExpectedTurnID {
		return nil, fmt.Errorf("%w: expected active turn id `%s` but found `%s`", ErrExpectedTurnMismatch, params.ExpectedTurnID, turn.ID)
	}
	return &TurnSteerResponse{TurnID: turn.ID}, nil
}

func (s *TurnService) Interrupt(params *TurnInterruptParams) (*TurnInterruptResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.active[params.ThreadID]
	if !ok || turn.ID != params.TurnID {
		return nil, fmt.Errorf("turn %s is not active", params.TurnID)
	}
	delete(s.active, params.ThreadID)
	return &TurnInterruptResponse{}, nil
}

func (s *TurnService) Complete(params *TurnCompleteParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.active[params.ThreadID]
	if !ok {
		return nil
	}
	if turn.ID != params.TurnID {
		return fmt.Errorf("active turn mismatch: expected %s, got %s", params.TurnID, turn.ID)
	}
	delete(s.active, params.ThreadID)
	return nil
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

func userInputsForJSON(values []TurnUserInput) []TurnUserInput {
	out := append([]TurnUserInput(nil), values...)
	if out == nil {
		return []TurnUserInput{}
	}
	return out
}

func userInputsForJSONWithPrompt(values []TurnUserInput, prompt string) []TurnUserInput {
	out := userInputsForJSON(values)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return out
	}
	input := TurnUserInput{Type: "text", Text: prompt}
	return append([]TurnUserInput{input}, out...)
}

func validateUserInputTextLimit(prompt string, inputs []TurnUserInput) error {
	actualChars := userInputTextChars(prompt, inputs)
	if actualChars > MaxUserInputTextChars {
		return &InputTooLargeError{
			MaxChars:    MaxUserInputTextChars,
			ActualChars: actualChars,
		}
	}
	return nil
}

func userInputTextChars(prompt string, inputs []TurnUserInput) int {
	total := utf8.RuneCountInString(strings.TrimSpace(prompt))
	for _, input := range inputs {
		if !turnUserInputCountsAsText(input) {
			continue
		}
		total += utf8.RuneCountInString(input.Text)
	}
	return total
}

func turnUserInputCountsAsText(input TurnUserInput) bool {
	inputType := strings.TrimSpace(input.Type)
	return inputType == "" || inputType == "text"
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func textFromInputs(inputs []TurnUserInput) string {
	parts := []string{}
	for _, input := range inputs {
		if strings.TrimSpace(input.Text) != "" {
			parts = append(parts, strings.TrimSpace(input.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

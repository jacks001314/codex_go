package tui

import (
	"fmt"
	"strings"
)

// Rust parity: codex-rs/tui/src/bottom_pane/request_user_input/.

type RequestUserInputChoice struct {
	Label       string
	Description string
}

type RequestUserInputQuestion struct {
	Header   string
	ID       string
	Question string
	Options  []RequestUserInputChoice
}

type RequestUserInputState struct {
	Questions         []RequestUserInputQuestion
	AutoResolutionMS  *int
	Current           int
	Answers           map[string]string
	AnswerLists       map[string][]string
	Committed         map[string]bool
	Draft             string
	NotesVisible      bool
	ConfirmUnanswered bool
}

func NewRequestUserInputState(questions []RequestUserInputQuestion, autoResolutionMS *int) (*RequestUserInputState, error) {
	state := &RequestUserInputState{
		Questions:        normalizeRequestUserInputQuestions(questions),
		AutoResolutionMS: cloneIntPtr(autoResolutionMS),
		Answers:          map[string]string{},
		AnswerLists:      map[string][]string{},
		Committed:        map[string]bool{},
	}
	if len(state.Questions) == 0 || len(state.Questions) > 3 {
		return nil, fmt.Errorf("request_user_input requires one to three questions")
	}
	for _, question := range state.Questions {
		if question.ID == "" || question.Question == "" {
			return nil, fmt.Errorf("question id and question are required")
		}
	}
	if state.AutoResolutionMS != nil && (*state.AutoResolutionMS < 60000 || *state.AutoResolutionMS > 240000) {
		return nil, fmt.Errorf("autoResolutionMs must be between 60000 and 240000")
	}
	return state, nil
}

func (s *RequestUserInputState) CurrentQuestion() (RequestUserInputQuestion, bool) {
	if s == nil || s.Current < 0 || s.Current >= len(s.Questions) {
		return RequestUserInputQuestion{}, false
	}
	return s.Questions[s.Current], true
}

func (s *RequestUserInputState) HasOptions() bool {
	question, ok := s.CurrentQuestion()
	return ok && len(question.Options) > 0
}

func (s *RequestUserInputState) AppendDraft(value string) {
	if s == nil {
		return
	}
	s.Draft += value
}

func (s *RequestUserInputState) BackspaceDraft() {
	if s == nil || s.Draft == "" {
		return
	}
	runes := []rune(s.Draft)
	if len(runes) == 0 {
		return
	}
	s.Draft = string(runes[:len(runes)-1])
}

func (s *RequestUserInputState) CommitAnswer(answer string) bool {
	return s.commitAnswerList([]string{strings.TrimSpace(answer)}, strings.TrimSpace(answer) != "")
}

func (s *RequestUserInputState) CommitFreeformAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	answers := []string{}
	if answer != "" {
		answers = append(answers, answer)
	}
	return s.commitAnswerList(answers, answer != "")
}

func (s *RequestUserInputState) CommitOptionAnswer(label string, notes string) bool {
	label = strings.TrimSpace(label)
	notes = strings.TrimSpace(notes)
	answers := []string{}
	if label != "" {
		answers = append(answers, label)
	}
	if notes != "" {
		answers = append(answers, "user_note: "+notes)
	}
	return s.commitAnswerList(answers, label != "")
}

func (s *RequestUserInputState) commitAnswerList(answers []string, committed bool) bool {
	if s == nil || s.Current < 0 || s.Current >= len(s.Questions) {
		return true
	}
	question := s.Questions[s.Current]
	if s.Answers == nil {
		s.Answers = map[string]string{}
	}
	if s.AnswerLists == nil {
		s.AnswerLists = map[string][]string{}
	}
	if s.Committed == nil {
		s.Committed = map[string]bool{}
	}
	normalized := normalizeAnswerList(answers)
	s.AnswerLists[question.ID] = normalized
	s.Answers[question.ID] = firstAnswer(normalized)
	s.Committed[question.ID] = committed && len(normalized) > 0
	s.Draft = ""
	s.NotesVisible = false
	s.ConfirmUnanswered = false
	if s.Current+1 >= len(s.Questions) {
		return true
	}
	s.Current++
	return false
}

func (s *RequestUserInputState) ResponseAnswers() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(s.Questions))
	for _, question := range s.Questions {
		if values, ok := s.AnswerLists[question.ID]; ok {
			out[question.ID] = firstAnswer(values)
			continue
		}
		if value, ok := s.Answers[question.ID]; ok {
			out[question.ID] = value
			continue
		}
		out[question.ID] = ""
	}
	if len(out) == 0 && len(s.Answers) > 0 {
		for key, value := range s.Answers {
			out[key] = value
		}
	}
	return out
}

func (s *RequestUserInputState) ResponseAnswerLists() map[string][]string {
	if s == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(s.Questions))
	for _, question := range s.Questions {
		values := s.AnswerLists[question.ID]
		out[question.ID] = append([]string(nil), values...)
	}
	return out
}

func (s *RequestUserInputState) UnansweredCount() int {
	if s == nil {
		return 0
	}
	count := 0
	for _, question := range s.Questions {
		if !s.Committed[question.ID] {
			count++
		}
	}
	return count
}

func (s *RequestUserInputState) FirstUnansweredIndex() int {
	if s == nil {
		return -1
	}
	for i, question := range s.Questions {
		if !s.Committed[question.ID] {
			return i
		}
	}
	return -1
}

func (s *RequestUserInputState) OpenUnansweredConfirmation() {
	if s == nil {
		return
	}
	s.ConfirmUnanswered = true
}

func (s *RequestUserInputState) CloseUnansweredConfirmation() {
	if s == nil {
		return
	}
	s.ConfirmUnanswered = false
}

func (s *RequestUserInputState) JumpToFirstUnanswered() bool {
	if s == nil {
		return false
	}
	index := s.FirstUnansweredIndex()
	if index < 0 {
		return false
	}
	s.Current = index
	s.Draft = ""
	s.NotesVisible = false
	s.ConfirmUnanswered = false
	return true
}

func (s *RequestUserInputState) BeginNotes() {
	if s == nil || !s.HasOptions() {
		return
	}
	s.NotesVisible = true
}

func (s *RequestUserInputState) ClearNotes() {
	if s == nil {
		return
	}
	s.Draft = ""
	s.NotesVisible = false
}

func (s *RequestUserInputState) RenderBody(width int) string {
	if s != nil && s.ConfirmUnanswered {
		count := s.UnansweredCount()
		suffix := "questions"
		if count == 1 {
			suffix = "question"
		}
		return fmt.Sprintf("Submit with unanswered questions?\n%d unanswered %s", count, suffix)
	}
	question, ok := s.CurrentQuestion()
	if !ok {
		return ""
	}
	lines := []string{}
	if question.Header != "" {
		lines = append(lines, question.Header)
	}
	progress := fmt.Sprintf("Question %d/%d", s.Current+1, len(s.Questions))
	if unanswered := s.UnansweredCount(); unanswered > 0 {
		progress += fmt.Sprintf(" (%d unanswered)", unanswered)
	}
	if s.AutoResolutionMS != nil {
		progress += " · auto-resolves in 1m 00s"
	}
	lines = append(lines, progress)
	lines = append(lines, question.Question)
	if len(question.Options) == 0 {
		answer := s.Draft
		if answer == "" {
			answer = "Type your answer (optional)"
		}
		lines = append(lines, "Answer: "+answer)
	} else if s.NotesVisible {
		notes := s.Draft
		if notes == "" {
			notes = "Add notes"
		}
		lines = append(lines, "Notes: "+notes)
	}
	body := strings.Join(lines, "\n")
	if width > 0 {
		body = strings.Join(WrapLines(strings.Split(body, "\n"), WrapOptions{Width: width, BreakWords: true}), "\n")
	}
	return body
}

func normalizeRequestUserInputQuestions(questions []RequestUserInputQuestion) []RequestUserInputQuestion {
	out := make([]RequestUserInputQuestion, 0, len(questions))
	for _, question := range questions {
		header := strings.TrimSpace(question.Header)
		if len(header) > 12 {
			header = header[:12]
		}
		options := make([]RequestUserInputChoice, 0, len(question.Options))
		for _, option := range question.Options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				continue
			}
			options = append(options, RequestUserInputChoice{
				Label:       label,
				Description: strings.TrimSpace(option.Description),
			})
			if len(options) == 3 {
				break
			}
		}
		out = append(out, RequestUserInputQuestion{
			Header:   header,
			ID:       strings.TrimSpace(question.ID),
			Question: strings.TrimSpace(question.Question),
			Options:  options,
		})
	}
	return out
}

func normalizeAnswerList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstAnswer(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

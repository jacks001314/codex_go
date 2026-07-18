package historycell

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/request_user_input.rs.

type RequestUserInputQuestion struct {
	ID       string
	Question string
	Options  []string
	IsSecret bool
}

type RequestUserInputAnswer struct {
	Answers []string
}

type RequestUserInputResultCell struct {
	Questions   []RequestUserInputQuestion
	Answers     map[string]RequestUserInputAnswer
	Interrupted bool
}

func NewRequestUserInputResult(questions []RequestUserInputQuestion, answers map[string]RequestUserInputAnswer, interrupted bool) RequestUserInputResultCell {
	clonedQuestions := make([]RequestUserInputQuestion, len(questions))
	for i := range questions {
		clonedQuestions[i] = questions[i]
		clonedQuestions[i].Options = append([]string(nil), questions[i].Options...)
	}
	clonedAnswers := make(map[string]RequestUserInputAnswer, len(answers))
	for id, answer := range answers {
		clonedAnswers[id] = RequestUserInputAnswer{Answers: append([]string(nil), answer.Answers...)}
	}
	return RequestUserInputResultCell{
		Questions:   clonedQuestions,
		Answers:     clonedAnswers,
		Interrupted: interrupted,
	}
}

func (c RequestUserInputResultCell) DisplayLines(width int) []string {
	width = max(width, 1)
	total := len(c.Questions)
	answered := c.answeredCount()
	header := "\u2022 Questions " + tui.FormatInt(int64(answered)) + "/" + tui.FormatInt(int64(total)) + " answered"
	if c.Interrupted {
		header += " (interrupted)"
	}
	lines := []string{header}
	for _, question := range c.Questions {
		answer, ok := c.Answers[question.ID]
		answerMissing := !ok || len(answer.Answers) == 0
		questionLines := tui.AdaptiveWrapLine(question.Question, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "  \u2022 ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})
		if len(questionLines) == 0 {
			questionLines = []string{"  \u2022 "}
		}
		if answerMissing {
			questionLines[len(questionLines)-1] += " (unanswered)"
		}
		lines = append(lines, questionLines...)
		if answerMissing {
			continue
		}
		if question.IsSecret {
			lines = append(lines, tui.AdaptiveWrapLine("\u2022\u2022\u2022\u2022\u2022\u2022", tui.WrapOptions{
				Width:            width,
				InitialIndent:    "    answer: ",
				SubsequentIndent: "            ",
				BreakWords:       true,
			})...)
			continue
		}
		options, note := splitRequestUserInputAnswer(answer)
		for _, option := range options {
			lines = append(lines, tui.AdaptiveWrapLine(option, tui.WrapOptions{
				Width:            width,
				InitialIndent:    "    answer: ",
				SubsequentIndent: "            ",
				BreakWords:       true,
			})...)
		}
		if note != "" {
			initial := "    answer: "
			subsequent := "            "
			if len(question.Options) > 0 {
				initial = "    note: "
				subsequent = "          "
			}
			lines = append(lines, tui.AdaptiveWrapLine(note, tui.WrapOptions{
				Width:            width,
				InitialIndent:    initial,
				SubsequentIndent: subsequent,
				BreakWords:       true,
			})...)
		}
	}
	if c.Interrupted {
		unanswered := total - answered
		if unanswered > 0 {
			lines = append(lines, tui.AdaptiveWrapLine("interrupted with "+tui.FormatInt(int64(unanswered))+" unanswered", tui.WrapOptions{
				Width:            width,
				InitialIndent:    "  \u21b3 ",
				SubsequentIndent: "    ",
				BreakWords:       true,
			})...)
		}
	}
	return lines
}

func (c RequestUserInputResultCell) RawLines() []string {
	total := len(c.Questions)
	answered := c.answeredCount()
	lines := []string{"Questions " + tui.FormatInt(int64(answered)) + "/" + tui.FormatInt(int64(total)) + " answered"}
	if c.Interrupted {
		lines = append(lines, "(interrupted)")
	}
	for _, question := range c.Questions {
		lines = append(lines, question.Question)
		answer, ok := c.Answers[question.ID]
		if !ok || len(answer.Answers) == 0 {
			lines = append(lines, "(unanswered)")
			continue
		}
		if question.IsSecret {
			lines = append(lines, "answer: ******")
			continue
		}
		options, note := splitRequestUserInputAnswer(answer)
		for _, option := range options {
			lines = append(lines, "answer: "+option)
		}
		if note != "" {
			lines = append(lines, "note: "+note)
		}
	}
	return lines
}

func (c RequestUserInputResultCell) answeredCount() int {
	answered := 0
	for _, question := range c.Questions {
		if answer, ok := c.Answers[question.ID]; ok && len(answer.Answers) > 0 {
			answered++
		}
	}
	return answered
}

func splitRequestUserInputAnswer(answer RequestUserInputAnswer) ([]string, string) {
	options := []string{}
	note := ""
	for _, entry := range answer.Answers {
		if text, ok := strings.CutPrefix(entry, "user_note: "); ok {
			note = text
			continue
		}
		options = append(options, entry)
	}
	return options, note
}

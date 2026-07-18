package review

import (
	"encoding/json"
	"fmt"
	"strings"
)

const FallbackMessage = "Reviewer failed to output a response."
const InterruptedMessage = "Review was interrupted. Please re-run /review and wait for it to complete."

const ReviewRolloutUserMessageID = "review_rollout_user"
const ReviewRolloutAssistantMessageID = "review_rollout_assistant"

type CodeLocation struct {
	AbsoluteFilePath string
	StartLine        int
	EndLine          int
}

type Finding struct {
	Title           string
	Body            string
	ConfidenceScore float64
	Priority        int
	CodeLocation    CodeLocation
}

type OutputEvent struct {
	Findings               []Finding
	OverallCorrectness     string
	OverallExplanation     string
	OverallConfidenceScore float64
}

func FormatLocation(item *Finding) string {
	if item == nil {
		return ""
	}
	end := item.CodeLocation.EndLine
	if end == 0 {
		end = item.CodeLocation.StartLine
	}
	return fmt.Sprintf("%s:%d-%d", item.CodeLocation.AbsoluteFilePath, item.CodeLocation.StartLine, end)
}

func FormatFindingsBlock(findings []Finding, selection []bool) string {
	lines := []string{""}
	if len(findings) > 1 {
		lines = append(lines, "Full review comments:")
	} else {
		lines = append(lines, "Review comment:")
	}
	for index := range findings {
		item := &findings[index]
		lines = append(lines, "")
		prefix := "-"
		if selection != nil {
			checked := true
			if index < len(selection) {
				checked = selection[index]
			}
			if checked {
				prefix = "- [x]"
			} else {
				prefix = "- [ ]"
			}
		}
		lines = append(lines, fmt.Sprintf("%s %s — %s", prefix, item.Title, FormatLocation(item)))
		for _, line := range rustStringLines(item.Body) {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func rustStringLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	if strings.HasSuffix(text, "\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

func RenderOutputText(output *OutputEvent) string {
	if output == nil {
		return FallbackMessage
	}
	var sections []string
	if explanation := strings.TrimSpace(output.OverallExplanation); explanation != "" {
		sections = append(sections, explanation)
	}
	if len(output.Findings) > 0 {
		sections = append(sections, strings.TrimSpace(FormatFindingsBlock(output.Findings, nil)))
	}
	if len(sections) == 0 {
		return FallbackMessage
	}
	return strings.Join(sections, "\n\n")
}

func ParseOutputEvent(text string) *OutputEvent {
	if output, ok := parseOutputEventJSON(text); ok {
		return output
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end >= 0 && start < end {
		if output, ok := parseOutputEventJSON(text[start : end+1]); ok {
			return output
		}
	}
	return &OutputEvent{OverallExplanation: text}
}

func ReviewRolloutMessages(output *OutputEvent) (string, string) {
	if output == nil {
		return RenderReviewExitInterrupted(), InterruptedMessage
	}
	results := strings.TrimSpace(output.OverallExplanation)
	if len(output.Findings) > 0 {
		results += "\n" + FormatFindingsBlock(output.Findings, nil)
	}
	return RenderReviewExitSuccess(results), RenderOutputText(output)
}

func RenderReviewExitSuccess(results string) string {
	const template = "<user_action>\n  <context>User initiated a review task. Here's the full review output from reviewer model. User may select one or more comments to resolve.</context>\n  <action>review</action>\n  <results>\n  {{results}}\n  </results>\n  </user_action>\n"
	return strings.Replace(template, "{{results}}", results, 1)
}

func RenderReviewExitInterrupted() string {
	return "<user_action>\n  <context>User initiated a review task, but was interrupted. If user asks about this, tell them to re-initiate a review with `/review` and wait for it to complete.</context>\n  <action>review</action>\n  <results>\n  None.\n  </results>\n</user_action>\n"
}

type reviewOutputJSON struct {
	Findings               *[]reviewFindingJSON `json:"findings"`
	OverallCorrectness     *string              `json:"overall_correctness"`
	OverallExplanation     *string              `json:"overall_explanation"`
	OverallConfidenceScore *float64             `json:"overall_confidence_score"`
}

type reviewFindingJSON struct {
	Title           *string                 `json:"title"`
	Body            *string                 `json:"body"`
	ConfidenceScore *float64                `json:"confidence_score"`
	Priority        *int                    `json:"priority"`
	CodeLocation    *reviewCodeLocationJSON `json:"code_location"`
}

type reviewCodeLocationJSON struct {
	AbsoluteFilePath *string              `json:"absolute_file_path"`
	LineRange        *reviewLineRangeJSON `json:"line_range"`
}

type reviewLineRangeJSON struct {
	Start *int `json:"start"`
	End   *int `json:"end"`
}

func parseOutputEventJSON(text string) (*OutputEvent, bool) {
	var decoded reviewOutputJSON
	if err := json.Unmarshal([]byte(text), &decoded); err != nil || !decoded.valid() {
		return nil, false
	}
	findings := make([]Finding, 0, len(*decoded.Findings))
	for _, item := range *decoded.Findings {
		if !item.valid() {
			return nil, false
		}
		findings = append(findings, Finding{
			Title:           *item.Title,
			Body:            *item.Body,
			ConfidenceScore: *item.ConfidenceScore,
			Priority:        *item.Priority,
			CodeLocation: CodeLocation{
				AbsoluteFilePath: *item.CodeLocation.AbsoluteFilePath,
				StartLine:        *item.CodeLocation.LineRange.Start,
				EndLine:          *item.CodeLocation.LineRange.End,
			},
		})
	}
	return &OutputEvent{
		Findings:               findings,
		OverallCorrectness:     *decoded.OverallCorrectness,
		OverallExplanation:     *decoded.OverallExplanation,
		OverallConfidenceScore: *decoded.OverallConfidenceScore,
	}, true
}

func (o *reviewOutputJSON) valid() bool {
	return o != nil &&
		o.Findings != nil &&
		o.OverallCorrectness != nil &&
		o.OverallExplanation != nil &&
		o.OverallConfidenceScore != nil
}

func (f *reviewFindingJSON) valid() bool {
	return f != nil &&
		f.Title != nil &&
		f.Body != nil &&
		f.ConfidenceScore != nil &&
		f.Priority != nil &&
		f.CodeLocation != nil &&
		f.CodeLocation.AbsoluteFilePath != nil &&
		f.CodeLocation.LineRange != nil &&
		f.CodeLocation.LineRange.Start != nil &&
		f.CodeLocation.LineRange.End != nil
}

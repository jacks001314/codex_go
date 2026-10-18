package bottompane

import (
	"sort"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/skill_popup.rs.

const MentionNameTruncateLen = 28

type SkillPopupItem struct {
	Name string
	Path string
}

type MentionItem struct {
	DisplayName string
	Description *string
	InsertText  string
	SearchTerms []string
	Path        *string
	CategoryTag *string
	SortRank    int
}

type SkillPopup struct {
	Query    string
	Mentions []MentionItem
	State    ScrollState
}

func NewSkillPopup(mentions []MentionItem) *SkillPopup {
	popup := &SkillPopup{Mentions: append([]MentionItem(nil), mentions...), State: NewScrollState()}
	popup.clampSelection()
	return popup
}

func (p *SkillPopup) SetMentions(mentions []MentionItem) {
	if p == nil {
		return
	}
	p.Mentions = append([]MentionItem(nil), mentions...)
	p.clampSelection()
}

func (p *SkillPopup) SetQuery(query string) {
	if p == nil {
		return
	}
	p.Query = query
	p.clampSelection()
}

func (p *SkillPopup) CalculateRequiredHeight(width int) int {
	if p == nil {
		return 0
	}
	visible := len(p.filteredItems())
	visible = min(MaxPopupRows, max(visible, 1))
	return visible + 3
}

func (p *SkillPopup) MoveUp() {
	if p == nil {
		return
	}
	length := len(p.filteredItems())
	p.State.MoveUpWrap(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *SkillPopup) MoveDown() {
	if p == nil {
		return
	}
	length := len(p.filteredItems())
	p.State.MoveDownWrap(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *SkillPopup) SelectedMention() (MentionItem, bool) {
	if p == nil || !p.State.HasSelection {
		return MentionItem{}, false
	}
	matches := p.filteredItems()
	if p.State.SelectedIdx < 0 || p.State.SelectedIdx >= len(matches) {
		return MentionItem{}, false
	}
	return p.Mentions[matches[p.State.SelectedIdx]], true
}

func (p *SkillPopup) Rows(width int) []string {
	if p == nil {
		return nil
	}
	matches := p.filtered()
	if len(matches) == 0 {
		return []string{"  no matches", "", "", tui.TruncateWithEllipsis(SkillPopupHintLine(), width)}
	}
	p.State.ClampSelection(len(matches))
	p.State.EnsureVisible(len(matches), min(MaxPopupRows, len(matches)))
	start := p.State.ScrollTop
	end := min(start+MaxPopupRows, len(matches))
	rows := make([]string, 0, end-start+2)
	for visibleIdx := start; visibleIdx < end; visibleIdx++ {
		idx := matches[visibleIdx].Index
		selected := p.State.HasSelection && visibleIdx == p.State.SelectedIdx
		row := tui.SelectionPrefix(selected) + truncateMentionName(p.Mentions[idx].DisplayName)
		tag, desc := mentionCategoryAndDescription(p.Mentions[idx])
		tagWidth := tui.DisplayWidth(tag)
		descriptionWidth := width - tui.DisplayWidth(row) - tagWidth - 2
		if tagWidth > 0 {
			descriptionWidth--
		}
		if desc != "" && descriptionWidth >= 24 {
			row += " - " + truncateSkillDescription(desc, descriptionWidth)
		}
		if tag != "" && tui.DisplayWidth(row)+tagWidth+1 <= width {
			if padding := width - tagWidth - tui.DisplayWidth(row); padding > 0 {
				row += strings.Repeat(" ", padding)
			}
			row += tag
		}
		if selected {
			row = tui.RenderSelectedRowFilled(row, width)
		}
		rows = append(rows, row)
	}
	rows = append(rows, "", "", tui.TruncateWithEllipsis(SkillPopupHintLine(), width))
	return rows
}

func (p *SkillPopup) clampSelection() {
	if p == nil {
		return
	}
	length := len(p.filteredItems())
	p.State.ClampSelection(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
}

type skillPopupMatch struct {
	Index        int
	DisplayMatch bool
	Score        int
}

func (p *SkillPopup) filteredItems() []int {
	matches := p.filtered()
	out := make([]int, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Index)
	}
	return out
}

func (p *SkillPopup) filtered() []skillPopupMatch {
	if p == nil {
		return nil
	}
	filter := strings.TrimSpace(p.Query)
	out := []skillPopupMatch{}
	for idx, mention := range p.Mentions {
		if filter == "" {
			out = append(out, skillPopupMatch{Index: idx, DisplayMatch: true})
			continue
		}
		if score, ok := fuzzySkillScore(mention.DisplayName, filter); ok {
			out = append(out, skillPopupMatch{Index: idx, DisplayMatch: true, Score: score})
			continue
		}
		bestScore := 0
		bestOK := false
		for _, term := range mention.SearchTerms {
			if term == mention.DisplayName {
				continue
			}
			if score, ok := fuzzySkillScore(term, filter); ok && (!bestOK || score < bestScore) {
				bestScore = score
				bestOK = true
			}
		}
		if bestOK {
			out = append(out, skillPopupMatch{Index: idx, DisplayMatch: false, Score: bestScore})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a := p.Mentions[out[i].Index]
		b := p.Mentions[out[j].Index]
		if filter == "" {
			if a.SortRank != b.SortRank {
				return a.SortRank < b.SortRank
			}
		} else {
			if out[i].DisplayMatch != out[j].DisplayMatch {
				return out[i].DisplayMatch
			}
			if out[i].Score != out[j].Score {
				return out[i].Score < out[j].Score
			}
			if a.SortRank != b.SortRank {
				return a.SortRank < b.SortRank
			}
		}
		return a.DisplayName < b.DisplayName
	})
	return out
}

func mentionDescription(mention MentionItem) string {
	tag, desc := mentionCategoryAndDescription(mention)
	switch {
	case tag != "" && desc != "":
		return tag + " " + desc
	case tag != "":
		return tag
	default:
		return desc
	}
}

func mentionCategoryAndDescription(mention MentionItem) (string, string) {
	tag := ""
	if mention.CategoryTag != nil {
		tag = strings.TrimSpace(*mention.CategoryTag)
	}
	desc := ""
	if mention.Description != nil {
		desc = strings.TrimSpace(*mention.Description)
	}
	return tag, desc
}

func truncateMentionName(name string) string {
	return truncateSkillDescription(name, MentionNameTruncateLen)
}

func SkillPopupHintLine() string {
	return "enter insert \u00b7 esc close"
}

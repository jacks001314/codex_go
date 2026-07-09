package bottompane

import (
	"sort"
	"strings"

	"codex_go/internal/tui"
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
	return visible + 2
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
		return []string{"  no matches", "", SkillPopupHintLine()}
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
		if desc := mentionDescription(p.Mentions[idx]); desc != "" {
			available := max(width-tui.DisplayWidth(row)-3, 0)
			if text := truncateSkillDescription(desc, available); text != "" {
				row += " - " + text
			}
		}
		if selected {
			row = tui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	rows = append(rows, "", SkillPopupHintLine())
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
	tag := ""
	if mention.CategoryTag != nil {
		tag = strings.TrimSpace(*mention.CategoryTag)
	}
	desc := ""
	if mention.Description != nil {
		desc = strings.TrimSpace(*mention.Description)
	}
	switch {
	case tag != "" && desc != "":
		return tag + " " + desc
	case tag != "":
		return tag
	default:
		return desc
	}
}

func truncateMentionName(name string) string {
	return truncateSkillDescription(name, MentionNameTruncateLen)
}

func SkillPopupHintLine() string {
	return "Press enter to insert or esc to close"
}

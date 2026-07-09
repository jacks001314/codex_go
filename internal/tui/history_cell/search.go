package historycell

import (
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/search.rs.

type WebSearchActionKind string

const (
	WebSearchActionSearch     WebSearchActionKind = "search"
	WebSearchActionOpenPage   WebSearchActionKind = "openPage"
	WebSearchActionFindInPage WebSearchActionKind = "findInPage"
	WebSearchActionOther      WebSearchActionKind = "other"
)

type WebSearchAction struct {
	Kind    WebSearchActionKind
	Query   string
	Queries []string
	URL     string
	Pattern string
}

type WebSearchCell struct {
	CallID    string
	Query     string
	Action    *WebSearchAction
	Completed bool
}

func NewActiveWebSearchCall(callID string, query string) WebSearchCell {
	return WebSearchCell{CallID: strings.TrimSpace(callID), Query: strings.TrimSpace(query)}
}

func NewWebSearchCall(callID string, query string, action WebSearchAction) WebSearchCell {
	return WebSearchCell{
		CallID:    strings.TrimSpace(callID),
		Query:     strings.TrimSpace(query),
		Action:    cloneWebSearchAction(&action),
		Completed: true,
	}
}

func (c *WebSearchCell) Update(action WebSearchAction, query string) {
	if c == nil {
		return
	}
	c.Action = cloneWebSearchAction(&action)
	c.Query = strings.TrimSpace(query)
}

func (c *WebSearchCell) Complete() {
	if c != nil {
		c.Completed = true
	}
}

func (c WebSearchCell) DisplayLines(width int) []string {
	text := c.rawLine()
	return tui.AdaptiveWrapLine(text, tui.WrapOptions{
		Width:            max(width, 1),
		InitialIndent:    "\u2022 ",
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
}

func (c WebSearchCell) RawLines() []string {
	return []string{c.rawLine()}
}

func (c WebSearchCell) rawLine() string {
	header := "Searching the web"
	if c.Completed {
		header = "Searched the web"
	}
	detail := webSearchDetail(c.Action, c.Query)
	if detail == "" {
		return header
	}
	separator := " "
	if c.Completed {
		separator = " for "
	}
	return header + separator + detail
}

func webSearchDetail(action *WebSearchAction, query string) string {
	if action == nil {
		return strings.TrimSpace(query)
	}
	detail := ""
	switch action.Kind {
	case WebSearchActionSearch:
		detail = strings.TrimSpace(action.Query)
		if detail == "" && len(action.Queries) > 0 {
			detail = strings.TrimSpace(action.Queries[0])
			if len(action.Queries) > 1 && detail != "" {
				detail += " ..."
			}
		}
	case WebSearchActionOpenPage:
		detail = strings.TrimSpace(action.URL)
	case WebSearchActionFindInPage:
		pattern := strings.TrimSpace(action.Pattern)
		url := strings.TrimSpace(action.URL)
		switch {
		case pattern != "" && url != "":
			detail = "'" + pattern + "' in " + url
		case pattern != "":
			detail = "'" + pattern + "'"
		case url != "":
			detail = url
		}
	}
	if detail == "" {
		detail = strings.TrimSpace(query)
	}
	return detail
}

func cloneWebSearchAction(action *WebSearchAction) *WebSearchAction {
	if action == nil {
		return nil
	}
	return &WebSearchAction{
		Kind:    action.Kind,
		Query:   action.Query,
		Queries: append([]string(nil), action.Queries...),
		URL:     action.URL,
		Pattern: action.Pattern,
	}
}

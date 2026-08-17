package prompt

import "sync"

const (
	skillShadowMaxPriorRequests = 2
	skillShadowMaxRequestBytes  = 2 * 1024
	skillShadowMaxQueryBytes    = 4 * 1024
	skillShadowMaxRecentSkills  = 50
)

// ShadowTaskContext is the shadow-only relevance history for one thread (Rust
// ShadowTaskContext, #39008): up to two prior substantive requests and the most
// recently relevant skills. New and reconstructed thread runtimes start cold.
type ShadowTaskContext struct {
	mu    sync.Mutex
	state taskContextState
}

type taskContextState struct {
	priorRequests []ShadowQuery
	recentSkills  []string
	pending       *taskPendingTurn
}

type taskPendingTurn struct {
	id           string
	request      *ShadowQuery
	recentSkills []string
}

// NewShadowTaskContext returns an empty task-context (cold thread runtime).
func NewShadowTaskContext() *ShadowTaskContext {
	return &ShadowTaskContext{}
}

// BeginTurn freezes the predictions for the current turn: it commits the
// previous pending turn's request and skills, builds the augmented query from
// the current request plus up to two prior substantive requests, and snapshots
// the recently relevant skills. Explicit intent and invocations recorded for
// this turn never affect its own predictions.
func (c *ShadowTaskContext) BeginTurn(turnID string, current ShadowQuery, substantive bool) TaskContextSnapshot {
	if c == nil {
		return TaskContextSnapshot{Query: current}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.pending == nil || c.state.pending.id != turnID {
		if previous := c.state.pending; previous != nil {
			if previous.request != nil {
				c.state.priorRequests = skillShadowWithoutShadowQuery(c.state.priorRequests, previous.request.Text)
				c.state.priorRequests = append([]ShadowQuery{*previous.request}, c.state.priorRequests...)
				if len(c.state.priorRequests) > skillShadowMaxPriorRequests {
					c.state.priorRequests = c.state.priorRequests[:skillShadowMaxPriorRequests]
				}
			}
			for index := len(previous.recentSkills) - 1; index >= 0; index-- {
				skillShadowRemember(&c.state.recentSkills, previous.recentSkills[index])
			}
		}
		c.state.pending = &taskPendingTurn{id: turnID}
	}

	retained := takeShadowBytesAtCharBoundary(current.Text, skillShadowMaxRequestBytes)
	text := takeShadowBytesAtCharBoundary(current.Text, skillShadowMaxQueryBytes)
	query := ShadowQuery{
		Text:      text,
		Truncated: current.Truncated || len(text) < len(current.Text),
	}
	for _, prior := range c.state.priorRequests {
		if prior.Text == retained {
			continue
		}
		if len(query.Text) > 0 && len(query.Text) < skillShadowMaxQueryBytes {
			query.Text += "\n"
		}
		part := takeShadowBytesAtCharBoundary(prior.Text, skillShadowMaxQueryBytes-len(query.Text))
		query.Text += part
		query.Truncated = query.Truncated || prior.Truncated || len(part) < len(prior.Text)
		if len(part) < len(prior.Text) {
			break
		}
	}
	recentSkills := append([]string(nil), c.state.recentSkills...)
	if substantive && c.state.pending != nil {
		request := ShadowQuery{
			Text:      retained,
			Truncated: current.Truncated || len(retained) < len(current.Text),
		}
		c.state.pending.request = &request
	}
	return TaskContextSnapshot{Query: query, RecentSkills: recentSkills}
}

// Record records relevance evidence for future turns, never the active turn's
// predictions (Rust ShadowTaskContext::record).
func (c *ShadowTaskContext) Record(turnID string, resource string) {
	if c == nil || turnID == "" || resource == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.pending != nil && c.state.pending.id == turnID {
		skillShadowRemember(&c.state.pending.recentSkills, resource)
	}
}

func skillShadowRemember(skills *[]string, resource string) {
	out := (*skills)[:0]
	for _, previous := range *skills {
		if previous != resource {
			out = append(out, previous)
		}
	}
	*skills = append([]string{resource}, out...)
	if len(*skills) > skillShadowMaxRecentSkills {
		*skills = (*skills)[:skillShadowMaxRecentSkills]
	}
}

func skillShadowWithoutShadowQuery(values []ShadowQuery, text string) []ShadowQuery {
	out := make([]ShadowQuery, 0, len(values))
	for _, value := range values {
		if value.Text != text {
			out = append(out, value)
		}
	}
	return out
}

func takeShadowBytesAtCharBoundary(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	end := maxBytes
	for end > 0 && text[end]&0xc0 == 0x80 {
		end--
	}
	return text[:end]
}

package appserver

import (
	"sync"

	"github.com/google/uuid"
)

// planItemIdentities belongs to one turn and outlives individual sampling
// attempts. Completed plans retain their IDs for persistence; unfinished plans
// carry their identity into the next attempt.
type planItemIdentities struct {
	mu         sync.Mutex
	current    string
	completed  bool
	byResponse map[string]string
	records    []completedStreamPlan
}

func (p *planItemIdentities) begin(responseID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id := p.byResponse[responseID]; id != "" {
		return id
	}
	if p.current == "" || p.completed {
		id, err := uuid.NewV7()
		if err != nil {
			p.current = uuid.NewString()
		} else {
			p.current = id.String()
		}
		p.completed = false
	}
	if p.byResponse == nil {
		p.byResponse = map[string]string{}
	}
	p.byResponse[responseID] = p.current
	return p.current
}

func (p *planItemIdentities) complete(responseID string) string {
	id := p.begin(responseID)
	p.mu.Lock()
	if p.current == id {
		p.completed = true
	}
	p.mu.Unlock()
	return id
}

func (r *RuntimeRouter) planIdentitiesForTurn(threadID, turnID string) *planItemIdentities {
	var ids *planItemIdentities
	if r != nil && r.threads != nil {
		r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
			if active.PlanIdentities == nil {
				active.PlanIdentities = &planItemIdentities{}
			}
			ids = active.PlanIdentities
		})
	}
	if ids == nil {
		ids = &planItemIdentities{}
	}
	return ids
}

type completedStreamPlan struct{ ID, ResponseID, Text string }

func (p *planItemIdentities) recordCompletion(responseID, text string) {
	id := p.complete(responseID)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, record := range p.records {
		if record.ID == id {
			return
		}
	}
	p.records = append(p.records, completedStreamPlan{ID: id, ResponseID: responseID, Text: text})
}

func (p *planItemIdentities) completedPlans() []completedStreamPlan {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]completedStreamPlan(nil), p.records...)
}

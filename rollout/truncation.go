package rollout

import (
	"fmt"

	"codex_go/eventmap"
)

type TruncationItemKind string

const (
	TruncationItemResponse                TruncationItemKind = "response"
	TruncationItemInterAgentCommunication TruncationItemKind = "inter_agent_communication"
	TruncationItemInterAgentMetadata      TruncationItemKind = "inter_agent_metadata"
	TruncationItemThreadRolledBack        TruncationItemKind = "thread_rolled_back"
	TruncationItemTurnStarted             TruncationItemKind = "turn_started"
)

type TruncationItem struct {
	Kind        TruncationItemKind
	Response    *eventmap.ResponseItem
	TriggerTurn bool
	NumTurns    uint32
	TurnID      string
	InProgress  bool
}

func InitialHistoryHasPriorUserTurns(items []TruncationItem) bool {
	for _, item := range items {
		if rolloutItemIsUserTurnBoundary(&item) {
			return true
		}
	}
	return false
}

func UserMessagePositions(items []TruncationItem) []int {
	positions := make([]int, 0)
	for index := range items {
		item := &items[index]
		if item.Kind == TruncationItemResponse && isRealUserMessageBoundary(item.Response) {
			positions = append(positions, index)
		}
		if item.Kind == TruncationItemThreadRolledBack {
			cut := len(positions) - int(item.NumTurns)
			if cut < 0 {
				cut = 0
			}
			positions = positions[:cut]
		}
	}
	return positions
}

func ForkTurnPositions(items []TruncationItem) []int {
	rollbackPositions := make([]int, 0)
	forkPositions := make([]int, 0)
	for index := range items {
		item := &items[index]
		switch item.Kind {
		case TruncationItemResponse:
			if rolloutItemIsUserTurnBoundary(item) {
				rollbackPositions = append(rollbackPositions, index)
			}
			if isRealUserMessageBoundary(item.Response) || isTriggerTurnBoundary(item.Response) {
				forkPositions = append(forkPositions, index)
			}
		case TruncationItemInterAgentCommunication, TruncationItemInterAgentMetadata:
			rollbackPositions = append(rollbackPositions, index)
			if item.TriggerTurn {
				forkPositions = append(forkPositions, index)
			}
		case TruncationItemThreadRolledBack:
			if item.NumTurns == 0 {
				continue
			}
			start := len(rollbackPositions) - int(item.NumTurns)
			if start < 0 {
				start = 0
			}
			if len(rollbackPositions) == 0 {
				continue
			}
			rollbackStart := rollbackPositions[start]
			rollbackPositions = rollbackPositions[:start]
			filtered := forkPositions[:0]
			for _, position := range forkPositions {
				if position < rollbackStart {
					filtered = append(filtered, position)
				}
			}
			forkPositions = filtered
		}
	}
	return forkPositions
}

func TruncateBeforeNthUserMessageFromStart(items []TruncationItem, n int) []TruncationItem {
	if n < 0 {
		return cloneTruncationItems(items)
	}
	positions := UserMessagePositions(items)
	if len(positions) <= n {
		return cloneTruncationItems(items)
	}
	return cloneTruncationItems(items[:positions[n]])
}

func TruncateToLastNForkTurns(items []TruncationItem, n int) []TruncationItem {
	if n <= 0 {
		return nil
	}
	positions := ForkTurnPositions(items)
	if len(positions) == 0 {
		return nil
	}
	start := len(positions) - n
	if start < 0 {
		start = 0
	}
	return cloneTruncationItems(items[positions[start]:])
}

func TruncateAfterTurnID(items []TruncationItem, turnID string) ([]TruncationItem, error) {
	if turnID == "" {
		return nil, fmt.Errorf("turn id is required")
	}
	start := -1
	for index, item := range items {
		if item.Kind == TruncationItemTurnStarted && item.TurnID == turnID {
			if item.InProgress {
				return nil, fmt.Errorf("lastTurnId %q identifies an in-progress turn", turnID)
			}
			start = index
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("lastTurnId %q was not found in the source thread", turnID)
	}
	cut := len(items)
	for index := start + 1; index < len(items); index++ {
		if items[index].Kind == TruncationItemTurnStarted {
			cut = index
			break
		}
	}
	return cloneTruncationItems(items[:cut]), nil
}

func rolloutItemIsUserTurnBoundary(item *TruncationItem) bool {
	if item == nil {
		return false
	}
	return (item.Kind == TruncationItemResponse && isUserTurnBoundary(item.Response)) || item.Kind == TruncationItemInterAgentCommunication || item.Kind == TruncationItemInterAgentMetadata
}

func isUserTurnBoundary(item *eventmap.ResponseItem) bool {
	return isRealUserMessageBoundary(item) || isTriggerTurnBoundary(item)
}

func isRealUserMessageBoundary(item *eventmap.ResponseItem) bool {
	return item != nil && item.Kind == eventmap.ResponseMessage && item.Role == "user" && !eventmap.IsContextualUserMessageContent(item.Content)
}

func isTriggerTurnBoundary(item *eventmap.ResponseItem) bool {
	if item == nil || item.Kind != eventmap.ResponseMessage || item.Role != "assistant" {
		return false
	}
	for _, content := range item.Content {
		if content.Kind == eventmap.ContentInputText && content.Text == "<inter_agent trigger_turn=\"true\"/>" {
			return true
		}
	}
	return false
}

func cloneTruncationItems(items []TruncationItem) []TruncationItem {
	out := make([]TruncationItem, len(items))
	copy(out, items)
	return out
}

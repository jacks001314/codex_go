package tui

type AgentOption struct {
	ID    string
	Label string
}

type AgentThreadEntry struct {
	ThreadID      string
	AgentNickname string
	AgentRole     string
	AgentPath     string
	IsPrimary     bool
	IsRunning     bool
	IsClosed      bool
}

func (e AgentThreadEntry) DisplayLabel() string {
	return FormatAgentPickerItemName(e.AgentNickname, e.AgentRole, e.IsPrimary)
}

func FormatAgentPickerItemName(agentNickname string, agentRole string, isPrimary bool) string {
	if isPrimary {
		return "Main [default]"
	}
	agentNickname = trimAgentLabelPart(agentNickname)
	agentRole = trimAgentLabelPart(agentRole)
	switch {
	case agentNickname != "" && agentRole != "":
		return agentNickname + " [" + agentRole + "]"
	case agentNickname != "":
		return agentNickname
	case agentRole != "":
		return "[" + agentRole + "]"
	default:
		return "Agent"
	}
}

func trimAgentLabelPart(value string) string {
	start := 0
	end := len(value)
	for start < end {
		switch value[start] {
		case ' ', '\t', '\r', '\n':
			start++
		default:
			goto trimEnd
		}
	}
trimEnd:
	for end > start {
		switch value[end-1] {
		case ' ', '\t', '\r', '\n':
			end--
		default:
			return value[start:end]
		}
	}
	return value[start:end]
}

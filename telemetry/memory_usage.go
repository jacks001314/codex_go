package telemetry

import (
	"encoding/json"
	"strings"

	"codex_go/memories"
	"codex_go/state"
	"codex_go/tool"
)

const MemoryUsageMetricName = "codex.memories.usage"

type MemoryUsageKind string

const (
	MemoryUsageKindList      MemoryUsageKind = "list"
	MemoryUsageKindRead      MemoryUsageKind = "read"
	MemoryUsageKindSearch    MemoryUsageKind = "search"
	MemoryUsageKindWrite     MemoryUsageKind = "write"
	MemoryUsageKindAdHocNote MemoryUsageKind = "ad_hoc_note"
)

type MemoryUsageMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
}

type MemoryUsageShellCommandParams struct {
	Command string `json:"command"`
}

type MemoryUsageExecCommandParams struct {
	Cmd string `json:"cmd"`
}

func MemoryUsageKindsFromCommand(command string) []MemoryUsageKind {
	original := command
	command = strings.ToLower(command)
	if strings.TrimSpace(command) == "" {
		return nil
	}
	seen := map[MemoryUsageKind]bool{}
	add := func(kind MemoryUsageKind) {
		seen[kind] = true
	}
	for _, kind := range memories.UsageKindsFromCommand(original) {
		switch kind {
		case memories.UsageKindMemoryMD, memories.UsageKindMemorySummary, memories.UsageKindRawMemories, memories.UsageKindRolloutSummaries, memories.UsageKindSkills:
			add(MemoryUsageKindRead)
		}
	}
	switch {
	case containsAny(command, "memory search", "memories search", "memory-search", "memories-search"):
		add(MemoryUsageKindSearch)
	case containsAny(command, "memory read", "memories read", "memory-read", "memories-read"):
		add(MemoryUsageKindRead)
	case containsAny(command, "memory list", "memories list", "memory-list", "memories-list"):
		add(MemoryUsageKindList)
	case containsAny(command, "memory add", "memories add", "memory write", "memories write", "ad-hoc note", "ad_hoc_note"):
		add(MemoryUsageKindAdHocNote)
		add(MemoryUsageKindWrite)
	}
	if containsAny(command, "codex memories", "codex memory") {
		if containsAny(command, " search ", " --search", " find ") {
			add(MemoryUsageKindSearch)
		}
		if containsAny(command, " read ", " show ", " cat ") {
			add(MemoryUsageKindRead)
		}
		if containsAny(command, " list ", " ls ") {
			add(MemoryUsageKindList)
		}
		if containsAny(command, " add ", " note ", " write ") {
			add(MemoryUsageKindAdHocNote)
			add(MemoryUsageKindWrite)
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]MemoryUsageKind, 0, len(seen))
	for _, kind := range []MemoryUsageKind{MemoryUsageKindList, MemoryUsageKindRead, MemoryUsageKindSearch, MemoryUsageKindWrite, MemoryUsageKindAdHocNote} {
		if seen[kind] {
			out = append(out, kind)
		}
	}
	return out
}

func MemoryUsageShellScriptForInvocation(invocation *tool.Invocation) (string, bool) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return "", false
	}
	switch {
	case invocation.ToolName.Namespace == "" && invocation.ToolName.Name == "shell_command":
		var params MemoryUsageShellCommandParams
		if err := json.Unmarshal([]byte(defaultJSON(invocation.Payload.Arguments)), &params); err != nil || params.Command == "" {
			return "", false
		}
		return params.Command, true
	case invocation.ToolName.Namespace == "" && invocation.ToolName.Name == "exec_command":
		var params MemoryUsageExecCommandParams
		if err := json.Unmarshal([]byte(defaultJSON(invocation.Payload.Arguments)), &params); err != nil || params.Cmd == "" {
			return "", false
		}
		return params.Cmd, true
	default:
		return "", false
	}
}

func EmitMemoryUsageMetricForToolRead(invocation *tool.Invocation, success bool, sink MemoryUsageMetricSink) {
	if sink == nil {
		return
	}
	command, ok := MemoryUsageShellScriptForInvocation(invocation)
	if !ok {
		return
	}
	successTag := "false"
	if success {
		successTag = "true"
	}
	for _, kind := range MemoryUsageKindsFromCommand(command) {
		sink.Counter(MemoryUsageMetricName, 1, map[string]string{
			"kind":    string(kind),
			"tool":    MemoryUsageFlatToolName(invocation.ToolName),
			"success": successTag,
		})
	}
}

func EmitTaskMemoryUsageMetricForToolRead(invocation *tool.Invocation, success bool, metrics *state.TaskMetrics) {
	EmitMemoryUsageMetricForToolRead(invocation, success, metrics)
}

func MemoryUsageFlatToolName(name tool.ToolName) string {
	if name.Namespace == "" {
		return name.Name
	}
	return name.Namespace + "." + name.Name
}

func containsAny(value string, needles ...string) bool {
	padded := " " + value + " "
	for _, needle := range needles {
		if strings.Contains(padded, needle) || strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func defaultJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

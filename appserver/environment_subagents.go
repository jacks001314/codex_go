package appserver

import (
	"fmt"
	"sort"
	"strings"

	"codex_go/agent"
	"codex_go/session"
)

const (
	// Rust #43491: the multi-agent v2 environment roster is capped at eight
	// agents and 1,024 rendered bytes.
	maxEnvironmentSubagents     = 8
	maxEnvironmentSubagentBytes = 1024
)

// environmentSubagentChild is one direct thread-spawn child of a thread.
type environmentSubagentChild struct {
	threadID string
	path     agent.AgentPath
	nickname string
	loaded   bool
}

// environmentContextSubagentLines mirrors Rust's
// AgentControl::format_environment_context_subagents. Multi-agent v2 builds the
// roster from every registered direct child (including threads that are not
// loaded), while earlier versions keep the original loaded-children listing.
func (r *RuntimeRouter) environmentContextSubagentLines(threadID string) []string {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	children := r.openThreadSpawnChildren(threadID)
	if knownRuntimeMultiAgentVersion(record.Metadata.MultiAgentVersion) == agent.VersionV2 {
		return environmentContextSubagentLinesV2(record, children)
	}
	lines := make([]string, 0, len(children))
	for _, child := range children {
		if !child.loaded {
			continue
		}
		reference := child.threadID
		if name := agentPathName(string(child.path)); name != "" {
			reference = name
		}
		lines = append(lines, agent.FormatSubagentContextLine(reference, child.nickname))
	}
	return lines
}

// environmentContextSubagentLinesV2 renders the multi-agent v2 roster: direct
// children of the thread's agent path, loaded children first, alphabetical
// within each group, capped at eight agents and 1,024 bytes (Rust #43491).
func environmentContextSubagentLinesV2(record *session.Record, children []environmentSubagentChild) []string {
	parentPrefix := normalizeEnvironmentAgentPath(record.Metadata.AgentPath) + "/"
	loaded := map[agent.AgentPath]struct{}{}
	paths := make([]agent.AgentPath, 0, len(children))
	for _, child := range children {
		path := agent.AgentPath(strings.TrimSpace(string(child.path)))
		if path == "" || !strings.HasPrefix(string(path), parentPrefix) {
			continue
		}
		// Direct children only; grandchildren are rendered under their own parent.
		if strings.Contains(strings.TrimPrefix(string(path), parentPrefix), "/") {
			continue
		}
		paths = append(paths, path)
		if child.loaded {
			loaded[path] = struct{}{}
		}
	}
	sort.SliceStable(paths, func(i, j int) bool { return paths[i] < paths[j] })
	// Stable sorting preserves alphabetical order within the loaded/unloaded groups.
	sort.SliceStable(paths, func(i, j int) bool {
		_, iLoaded := loaded[paths[i]]
		_, jLoaded := loaded[paths[j]]
		return iLoaded && !jLoaded
	})
	lines := make([]string, 0, len(paths))
	renderedBytes := len("  <subagents>\n  </subagents>\n")
	for _, path := range paths {
		if len(lines) == maxEnvironmentSubagents {
			break
		}
		line := fmt.Sprintf(`<agent name="%s" />`, path)
		lineBytes := len("    \n") + len(line)
		if renderedBytes+lineBytes <= maxEnvironmentSubagentBytes {
			renderedBytes += lineBytes
			lines = append(lines, line)
		}
	}
	return lines
}

// openThreadSpawnChildren returns the thread's direct children with an open
// thread-spawn edge, including children whose threads are not currently loaded.
func (r *RuntimeRouter) openThreadSpawnChildren(parentThreadID string) []environmentSubagentChild {
	if r == nil || r.services.SpawnGraph == nil {
		return nil
	}
	status := agent.ThreadSpawnEdgeOpen
	childIDs, err := r.services.SpawnGraph.ListThreadSpawnChildren(parentThreadID, &status)
	if err != nil {
		return nil
	}
	children := make([]environmentSubagentChild, 0, len(childIDs))
	for _, childID := range childIDs {
		child := environmentSubagentChild{threadID: childID}
		if record, recordErr := r.threadRecord(session.ThreadID(childID), true, false); recordErr == nil && record != nil {
			child.path = agent.AgentPath(record.Metadata.AgentPath)
			child.nickname = record.Metadata.AgentNickname
		}
		child.loaded = r.threads != nil && r.threads.HasLiveThread(session.ThreadID(childID))
		children = append(children, child)
	}
	return children
}

func normalizeEnvironmentAgentPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" || path == "/" {
		return "/root"
	}
	return strings.TrimSuffix(path, "/")
}

// agentPathName returns the final segment of a canonical agent path.
func agentPathName(path string) string {
	path = strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")), "/")
	if path == "" {
		return ""
	}
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return strings.TrimSpace(path[index+1:])
	}
	return path
}

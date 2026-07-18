package app

import "sort"

type LoadedThread struct {
	ID             string
	Name           string
	Source         string
	ParentThreadID string
	AgentNickname  string
	AgentRole      string
	AgentPath      string
}

type LoadedSubagentThread struct {
	ThreadID      string
	AgentNickname string
	AgentRole     string
	AgentPath     string
}

const LoadedThreadSourceSubAgentThreadSpawn = "subagent_thread_spawn"

func FindLoadedSubagentThreadsForPrimary(threads []LoadedThread, primaryThreadID string) []LoadedSubagentThread {
	threadsByID := make(map[string]LoadedThread, len(threads))
	for _, thread := range threads {
		if thread.ID == "" {
			continue
		}
		threadsByID[thread.ID] = thread
	}

	included := map[string]struct{}{}
	pending := []string{primaryThreadID}
	for len(pending) > 0 {
		parentThreadID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for threadID, thread := range threadsByID {
			if _, ok := included[threadID]; ok {
				continue
			}
			if thread.Source != LoadedThreadSourceSubAgentThreadSpawn || thread.ParentThreadID != parentThreadID {
				continue
			}
			included[threadID] = struct{}{}
			pending = append(pending, threadID)
		}
	}

	loaded := make([]LoadedSubagentThread, 0, len(included))
	for threadID := range included {
		thread := threadsByID[threadID]
		loaded = append(loaded, LoadedSubagentThread{
			ThreadID:      threadID,
			AgentNickname: thread.AgentNickname,
			AgentRole:     thread.AgentRole,
			AgentPath:     thread.AgentPath,
		})
	}
	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].ThreadID < loaded[j].ThreadID
	})
	return loaded
}

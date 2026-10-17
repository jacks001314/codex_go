package appserver

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	maxFeedbackTreeThreads      = 8
	maxListedFeedbackOmissions  = 64
	feedbackThreadIndexFilename = "feedback-thread-index.json"
)

type feedbackThreadIndex struct {
	Threads                       []feedbackThreadIndexEntry `json:"threads"`
	RetainedFailureThreadIDs      []string                   `json:"retained_failure_thread_ids"`
	OmittedThreadIDs              []string                   `json:"omitted_thread_ids"`
	UnlistedOmittedThreadCount    int                        `json:"unlisted_omitted_thread_count"`
	ProcessDiscardedReviewRecords int                        `json:"process_discarded_review_records"`
	Notes                         string                     `json:"notes"`
}

type feedbackThreadIndexEntry struct {
	ThreadID                string  `json:"thread_id"`
	RolloutFilename         *string `json:"rollout_filename"`
	GuardianRolloutFilename *string `json:"guardian_rollout_filename"`
}

func selectFeedbackTreeThreads(reportedThreadID string, subtree []string, failures guardianReviewFailures) ([]string, feedbackThreadIndex) {
	reportedThreadID = strings.TrimSpace(reportedThreadID)
	candidates := append([]string(nil), subtree...)
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	descendants := map[string]struct{}{}
	unique := candidates[:0]
	for _, id := range candidates {
		id = strings.TrimSpace(id)
		if id == "" || id == reportedThreadID {
			continue
		}
		if _, duplicate := descendants[id]; duplicate {
			continue
		}
		descendants[id] = struct{}{}
		unique = append(unique, id)
	}
	candidates = unique

	selected := map[string]struct{}{}
	threads := []string{}
	if reportedThreadID != "" {
		selected[reportedThreadID] = struct{}{}
		threads = append(threads, reportedThreadID)
	}
	add := func(id string) {
		if len(threads) >= maxFeedbackTreeThreads {
			return
		}
		if _, ok := descendants[id]; !ok {
			return
		}
		if _, duplicate := selected[id]; duplicate {
			return
		}
		selected[id] = struct{}{}
		threads = append(threads, id)
	}
	for _, id := range failures.ThreadIDs {
		add(strings.TrimSpace(id))
	}
	for _, id := range candidates {
		add(id)
	}
	omitted := make([]string, 0, min(maxListedFeedbackOmissions, len(candidates)))
	unlisted := 0
	for _, id := range candidates {
		if _, ok := selected[id]; ok {
			continue
		}
		if len(omitted) < maxListedFeedbackOmissions {
			omitted = append(omitted, id)
		} else {
			unlisted++
		}
	}
	index := feedbackThreadIndex{
		Threads:                       make([]feedbackThreadIndexEntry, len(threads)),
		RetainedFailureThreadIDs:      append([]string(nil), failures.ThreadIDs...),
		OmittedThreadIDs:              omitted,
		UnlistedOmittedThreadCount:    unlisted,
		ProcessDiscardedReviewRecords: failures.ProcessDiscardedRecords,
		Notes: "Selection only, not an upload receipt. Null filenames mean no path was available. " +
			"Omitted threads exceeded the rollout thread limit; their retained reviews are still in auto-review-failures.jsonl. " +
			"Review evidence is process-local and bounded; missing evidence does not imply no denial. " +
			"The discarded-record count is process-wide, not specific to this tree, and resets on restart.",
	}
	for i, id := range threads {
		index.Threads[i].ThreadID = id
	}
	return threads, index
}

func (i *feedbackThreadIndex) setRolloutFilename(threadID string, path string) {
	for index := range i.Threads {
		if i.Threads[index].ThreadID != threadID {
			continue
		}
		if name := feedbackRolloutFilename(path); name != "" {
			i.Threads[index].RolloutFilename = &name
		}
		return
	}
}

func feedbackRolloutFilename(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (i feedbackThreadIndex) attachment() (FeedbackAttachment, error) {
	data, err := json.Marshal(i)
	if err != nil {
		return FeedbackAttachment{}, err
	}
	return FeedbackAttachment{
		Filename:    feedbackThreadIndexFilename,
		Buffer:      data,
		ContentType: "application/json",
	}, nil
}

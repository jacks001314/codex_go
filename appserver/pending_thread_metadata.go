package appserver

import "strings"

// pendingThreadProjects holds a thread/start project assignment until the
// thread row replaces it. Rust stages the same value in the thread store
// (#63545 ThreadStore::stage_pending_thread_metadata); Go keeps it in the
// router because the state row, once written, is authoritative.
func (r *RuntimeRouter) recordPendingThreadProject(threadID string, projectID *string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	project := ""
	if projectID != nil {
		project = strings.TrimSpace(*projectID)
	}
	r.pendingThreadProjectMu.Lock()
	defer r.pendingThreadProjectMu.Unlock()
	if project == "" {
		delete(r.pendingThreadProjects, threadID)
		return
	}
	if r.pendingThreadProjects == nil {
		r.pendingThreadProjects = map[string]string{}
	}
	r.pendingThreadProjects[threadID] = project
}

// pendingThreadProject returns the staged project for a thread that has no
// state row yet, if one exists.
func (r *RuntimeRouter) pendingThreadProject(threadID string) *string {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.pendingThreadProjectMu.Lock()
	project := r.pendingThreadProjects[threadID]
	r.pendingThreadProjectMu.Unlock()
	if project == "" {
		return nil
	}
	return &project
}

// clearPendingThreadProject drops a staged assignment once the thread row
// exists (or the thread is deleted), matching Rust's pending-metadata upsert.
func (r *RuntimeRouter) clearPendingThreadProject(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.pendingThreadProjectMu.Lock()
	delete(r.pendingThreadProjects, threadID)
	r.pendingThreadProjectMu.Unlock()
}

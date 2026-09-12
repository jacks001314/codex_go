package app

import (
	"context"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/session"
)

// Rust named_session_lookup.rs (#43315): resolve a displayed session label
// through the app server before acting on its thread id. Distinct matches are
// rejected instead of silently targeting the most recent one, and a label that
// cannot be verified as unique across server pages requires a session UUID.

// sessionCollection selects the active or archived session collection.
type sessionCollection int

const (
	sessionCollectionActive sessionCollection = iota
	sessionCollectionArchived
)

// ambiguousSessionNameError mirrors Rust AmbiguousSessionName::Multiple.
func ambiguousSessionNameError(name string, firstID string, secondID string) error {
	return fmt.Errorf("Multiple sessions match '%s' (including %s and %s); use a session UUID to disambiguate.", name, firstID, secondID)
}

// paginatedSessionNameError mirrors Rust AmbiguousSessionName::Paginated.
func paginatedSessionNameError(id string) error {
	return fmt.Errorf("Cannot verify a unique session label across server pages; matching session UUID: %s. Use it only if this is the session you want.", id)
}

// localSessionLabel is Rust display_label over a session record: the trimmed
// title, else the trimmed preview.
func localSessionLabel(record session.Record) string {
	if title := strings.TrimSpace(record.Title); title != "" {
		return title
	}
	return strings.TrimSpace(record.Preview)
}

// uniqueSessionRecordByName returns the single record whose displayed label
// matches name, rejecting distinct matches (Rust #43315).
func uniqueSessionRecordByName(name string, records []session.Record) (session.Record, bool, error) {
	var matched *session.Record
	for index := range records {
		record := records[index]
		if localSessionLabel(record) != name {
			continue
		}
		if matched != nil && matched.ID != record.ID {
			return session.Record{}, false, ambiguousSessionNameError(name, string(matched.ID), string(record.ID))
		}
		copied := record
		matched = &copied
	}
	if matched == nil {
		return session.Record{}, false, nil
	}
	return *matched, true, nil
}

// lookupRemoteSessionByName resolves an exact displayed label (trimmed name or
// fallback preview) across the requested collections, rejecting distinct
// matches and pagination-ambiguous results (Rust named_session_lookup::lookup).
// A nil thread with a nil error means no session matched.
func lookupRemoteSessionByName(ctx context.Context, client *remoteAppServerTUIClient, name string, collections []sessionCollection, opts *cli.SessionOptions) (*appserver.Thread, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var matched *appserver.Thread
	paginated := false
	for _, collection := range collections {
		archived := collection == sessionCollectionArchived
		var cursor *string
		for {
			params := remoteThreadListParams(opts, archived, 100)
			params.Cursor = cursor
			// Labels are matched locally (name or preview), not by the server's
			// search index, so duplicates cannot be hidden by a search term.
			params.SearchTerm = nil
			var response appserver.ThreadListResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadList, params, &response); err != nil {
				return nil, fmt.Errorf("failed to list sessions while resolving session label: %w", err)
			}
			if response.NextCursor != nil && strings.TrimSpace(*response.NextCursor) != "" {
				paginated = true
			}
			for index := range response.Data {
				thread := &response.Data[index]
				if remoteThreadDisplayName(thread) != name {
					continue
				}
				current, skip, err := revalidateRemoteSessionLabel(ctx, client, thread, name)
				if err != nil {
					return nil, err
				}
				if skip {
					continue
				}
				if matched != nil && matched.ID != current.ID {
					return nil, ambiguousSessionNameError(name, matched.ID, current.ID)
				}
				matched = current
			}
			if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
				break
			}
			cursor = response.NextCursor
		}
	}
	if matched != nil && paginated {
		// Older server cursors can skip equal timestamps at a page boundary, so
		// uniqueness cannot be proven.
		return nil, paginatedSessionNameError(matched.ID)
	}
	return matched, nil
}

// revalidateRemoteSessionLabel re-reads a candidate so a stale list entry or a
// renamed session is not selected (Rust lookup's thread/read step). It reports
// skip for candidates the server cannot confirm or whose label no longer
// matches; older servers that cannot read an unloaded thread keep the listed
// entry.
func revalidateRemoteSessionLabel(ctx context.Context, client *remoteAppServerTUIClient, thread *appserver.Thread, name string) (*appserver.Thread, bool, error) {
	var response appserver.ThreadReadResponse
	readErr := remoteSessionRequest(ctx, client, appserver.MethodThreadRead, appserver.ThreadReadParams{
		ThreadID:     strings.TrimSpace(thread.ID),
		IncludeTurns: false,
	}, &response)
	if readErr != nil {
		message := readErr.Error()
		if strings.Contains(message, "thread not loaded: "+strings.TrimSpace(thread.ID)) {
			// Compatibility with older servers that cannot read unloaded threads.
			return thread, false, nil
		}
		if strings.Contains(message, "thread-store internal error") {
			return nil, true, nil
		}
		return nil, false, readErr
	}
	if response.Thread == nil || strings.TrimSpace(response.Thread.ID) != strings.TrimSpace(thread.ID) || remoteThreadDisplayName(response.Thread) != name {
		return nil, true, nil
	}
	return response.Thread, false, nil
}

// lookupRemoteSessionByExactName resolves one collection and maps a miss to the
// caller's scope-specific "no session found" error.
func lookupRemoteSessionByExactName(ctx context.Context, client *remoteAppServerTUIClient, name string, collection sessionCollection, opts *cli.SessionOptions) (remoteResolvedSessionTarget, error) {
	thread, err := lookupRemoteSessionByName(ctx, client, name, []sessionCollection{collection}, opts)
	if err != nil {
		return remoteResolvedSessionTarget{}, err
	}
	if thread == nil {
		return remoteResolvedSessionTarget{}, fmt.Errorf("No %s session found matching '%s'.", remoteSessionSearchScope(collection == sessionCollectionArchived), name)
	}
	return remoteSessionTargetFromThread(thread)
}

package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex_go/internal/auth"
	"codex_go/internal/cli"
	"codex_go/internal/config"
	"codex_go/internal/session"
)

const sessionPickerPageSize = 20

func runSessionResume(opts *cli.SessionOptions, root *cli.RootOptions, stdout io.Writer) error {
	if err := loadSessionRuntimeConfig(opts, root); err != nil {
		return err
	}
	if err := ensureSessionRemoteRuntimeSupported(opts, root); err != nil {
		return err
	}
	store := newSessionStore()
	if needsSessionPicker(opts) {
		return writeSessionPicker(stdout, store, "resume", opts)
	}
	target, err := resolveSessionTarget(store, opts)
	if err != nil {
		return err
	}
	record, err := store.Read(target, true, true)
	if err != nil {
		return err
	}
	return writeSessionSummary(stdout, "resumed", record)
}

func runSessionArchive(opts *cli.SessionOptions, root *cli.RootOptions, stdout io.Writer) error {
	if err := loadSessionRuntimeConfig(opts, root); err != nil {
		return err
	}
	if err := ensureSessionRemoteRuntimeSupported(opts, root); err != nil {
		return err
	}
	store := newSessionStore()
	target, err := resolveSessionMutationTarget(store, opts, sessionArchivedFilter(false))
	if err != nil {
		return err
	}
	if err := store.Archive(target); err != nil {
		return err
	}
	name := ""
	if opts != nil && !isUUIDLike(strings.TrimSpace(opts.Target)) {
		name = strings.TrimSpace(opts.Target)
	}
	fmt.Fprint(stdout, sessionMutationSuccessMessage("Archived", target, name))
	return nil
}

func runSessionUnarchive(opts *cli.SessionOptions, root *cli.RootOptions, stdout io.Writer) error {
	if err := loadSessionRuntimeConfig(opts, root); err != nil {
		return err
	}
	if err := ensureSessionRemoteRuntimeSupported(opts, root); err != nil {
		return err
	}
	store := newSessionStore()
	target, err := resolveSessionMutationTarget(store, opts, sessionArchivedFilter(true))
	if err != nil {
		return err
	}
	record, err := store.Unarchive(target)
	if err != nil {
		return err
	}
	name := ""
	if record != nil {
		name = record.Title
	}
	fmt.Fprint(stdout, sessionMutationSuccessMessage("Unarchived", target, name))
	return nil
}

func runSessionDelete(opts *cli.SessionOptions, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := loadSessionRuntimeConfig(opts, root); err != nil {
		return err
	}
	if err := ensureSessionRemoteRuntimeSupported(opts, root); err != nil {
		return err
	}
	store := newSessionStore()
	target, err := resolveSessionMutationTarget(store, opts, nil)
	if err != nil {
		return err
	}
	if opts == nil || !opts.Force {
		sessionName, err := sessionNameForDeletePrompt(store, target)
		if err != nil {
			return err
		}
		confirmed, err := confirmSessionDelete(stdin, stderr, target, sessionName)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(stdout, "Delete cancelled.")
			return nil
		}
	}
	threadIDs, err := store.SubtreeThreadIDs(target)
	if err != nil {
		return err
	}
	for _, threadID := range session.DeleteOrderForSubtree(threadIDs) {
		if err := store.Delete(threadID); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
			return err
		}
	}
	fmt.Fprint(stdout, sessionMutationSuccessMessage("Deleted", target, ""))
	return nil
}

func sessionNameForDeletePrompt(store *session.Store, target session.ThreadID) (string, error) {
	record, err := store.Read(target, true, false)
	if err != nil {
		return "", err
	}
	if record == nil {
		return "", session.ErrThreadNotFound
	}
	return strings.TrimSpace(record.Title), nil
}

func confirmSessionDelete(stdin io.Reader, stderr io.Writer, target session.ThreadID, sessionName string) (bool, error) {
	if !isSessionTerminal(stdin) || !isSessionTerminal(stderr) {
		return false, errors.New("cannot confirm session deletion without an interactive terminal; rerun with --force and a session UUID")
	}
	if strings.TrimSpace(sessionName) != "" {
		fmt.Fprintf(stderr, "Permanently delete session '%s' (%s)?\n", strings.TrimSpace(sessionName), target)
	} else {
		fmt.Fprintf(stderr, "Permanently delete session %s?\n", target)
	}
	fmt.Fprintln(stderr, "This cannot be undone. Subagent threads will also be deleted.")
	fmt.Fprint(stderr, "Continue? [y/N]: ")
	if flusher, ok := stderr.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return false, err
		}
	}
	answer, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.TrimSpace(answer)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

func isSessionTerminal(value any) bool {
	if value == nil {
		return false
	}
	if terminal, ok := value.(interface{ IsTerminal() bool }); ok {
		return terminal.IsTerminal()
	}
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runSessionFork(opts *cli.SessionOptions, root *cli.RootOptions, stdout io.Writer) error {
	if err := loadSessionRuntimeConfig(opts, root); err != nil {
		return err
	}
	if err := ensureSessionRemoteRuntimeSupported(opts, root); err != nil {
		return err
	}
	store := newSessionStore()
	if needsSessionPicker(opts) {
		return writeSessionPicker(stdout, store, "fork", opts)
	}
	target, err := resolveSessionTarget(store, opts)
	if err != nil {
		return err
	}
	forked, err := store.Fork(target, session.ForkOptions{Mode: session.ForkAll})
	if err != nil {
		return err
	}
	return writeSessionSummary(stdout, "forked", forked)
}

func newSessionStore() *session.Store {
	return session.NewStore(filepath.Join(auth.DefaultCodexHome(), "sessions"))
}

func loadSessionRuntimeConfig(opts *cli.SessionOptions, root *cli.RootOptions) error {
	loadOpts := &config.EffectiveOptions{}
	if root != nil {
		loadOpts.RawOverrides = append(loadOpts.RawOverrides, root.ConfigOverrides...)
		loadOpts.StrictConfig = loadOpts.StrictConfig || root.StrictConfig
	}
	if opts != nil {
		loadOpts.RawOverrides = append(loadOpts.RawOverrides, opts.ConfigOverrides...)
		loadOpts.StrictConfig = loadOpts.StrictConfig || opts.StrictConfig
	}
	_, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), loadOpts)
	return err
}

func ensureSessionRemoteRuntimeSupported(opts *cli.SessionOptions, root *cli.RootOptions) error {
	remoteRoot := mergedSessionRemoteOptions(opts, root)
	if strings.TrimSpace(remoteRoot.Remote) == "" && strings.TrimSpace(remoteRoot.RemoteAuthEnv) == "" {
		return nil
	}
	if _, err := resolveInteractiveRemoteEndpoint(remoteRoot); err != nil {
		return err
	}
	return errors.New("session remote app-server mode is not implemented in the Go port yet")
}

func mergedSessionRemoteOptions(opts *cli.SessionOptions, root *cli.RootOptions) *cli.RootOptions {
	remote := ""
	remoteAuthEnv := ""
	if root != nil {
		remote = root.Remote
		remoteAuthEnv = root.RemoteAuthEnv
	}
	if opts != nil {
		if strings.TrimSpace(opts.Remote) != "" {
			remote = opts.Remote
		}
		if strings.TrimSpace(opts.RemoteAuthEnv) != "" {
			remoteAuthEnv = opts.RemoteAuthEnv
		}
	}
	return &cli.RootOptions{Remote: remote, RemoteAuthEnv: remoteAuthEnv}
}

func sessionMutationSuccessMessage(action string, sessionID session.ThreadID, sessionName string) string {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName != "" {
		return fmt.Sprintf("%s session %s (%s).\n", action, sessionName, sessionID)
	}
	return fmt.Sprintf("%s session %s.\n", action, sessionID)
}

func needsSessionPicker(opts *cli.SessionOptions) bool {
	if opts == nil {
		return true
	}
	return strings.TrimSpace(opts.Target) == "" && !opts.Last
}

func resolveSessionTarget(store *session.Store, opts *cli.SessionOptions) (session.ThreadID, error) {
	if opts != nil {
		if target := strings.TrimSpace(opts.Target); target != "" {
			if isUUIDLike(target) {
				return session.ThreadID(target), nil
			}
			return sessionIDByName(store, opts, target)
		}
		if opts.Last {
			return latestSessionID(store, opts)
		}
	}
	return "", errors.New("SESSION_ID or --last is required")
}

func resolveSessionMutationTarget(store *session.Store, opts *cli.SessionOptions, archived *bool) (session.ThreadID, error) {
	if opts != nil {
		if target := strings.TrimSpace(opts.Target); target != "" {
			if isUUIDLike(target) {
				return session.ThreadID(target), nil
			}
			return sessionIDByNameWithArchiveFilter(store, target, archived)
		}
	}
	return "", errors.New("SESSION is required")
}

func latestSessionID(store *session.Store, opts *cli.SessionOptions) (session.ThreadID, error) {
	records, err := listSessionPickerRecords(store, opts, 1)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", session.ErrThreadNotFound
	}
	return records[0].ID, nil
}

func sessionIDByName(store *session.Store, opts *cli.SessionOptions, name string) (session.ThreadID, error) {
	records, err := listSessionPickerRecords(store, opts, sessionPickerPageSize*10)
	if err != nil {
		return "", err
	}
	matches := make([]session.Record, 0, len(records))
	for i := range records {
		recordName := strings.TrimSpace(records[i].Title)
		if recordName == "" {
			recordName = strings.TrimSpace(string(records[i].ID))
		}
		if recordName == name {
			matches = append(matches, records[i])
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("No session found matching '%s'.", name)
	}
	sortSessionRecordsByRecency(matches)
	return matches[0].ID, nil
}

func sessionIDByNameWithArchiveFilter(store *session.Store, name string, archived *bool) (session.ThreadID, error) {
	if store == nil {
		return "", errors.New("session store is nil")
	}
	var records []session.Record
	if archived == nil || !*archived {
		active, err := listSessionsByArchived(store, false)
		if err != nil {
			return "", err
		}
		records = append(records, active...)
	}
	if archived == nil || *archived {
		inactive, err := listSessionsByArchived(store, true)
		if err != nil {
			return "", err
		}
		records = append(records, inactive...)
	}
	matches := make([]session.Record, 0, len(records))
	for i := range records {
		if !isInteractiveSession(&records[i]) {
			continue
		}
		recordName := strings.TrimSpace(records[i].Title)
		if recordName == "" {
			recordName = strings.TrimSpace(string(records[i].ID))
		}
		if recordName == name {
			matches = append(matches, records[i])
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("No %s session found matching '%s'.", sessionMutationSearchScope(archived), name)
	}
	sortSessionRecordsByRecency(matches)
	return matches[0].ID, nil
}

func sessionMutationSearchScope(archived *bool) string {
	if archived == nil {
		return "active or archived"
	}
	if *archived {
		return "archived"
	}
	return "active"
}

func sessionArchivedFilter(archived bool) *bool {
	value := archived
	return &value
}

func isUUIDLike(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func writeSessionSummary(stdout io.Writer, action string, record *session.Record) error {
	if stdout == nil {
		return nil
	}
	if record == nil {
		return errors.New("session record is nil")
	}
	payload := struct {
		Action    string           `json:"action"`
		ID        session.ThreadID `json:"id"`
		Title     string           `json:"title,omitempty"`
		Preview   string           `json:"preview,omitempty"`
		Archived  bool             `json:"archived"`
		ItemCount int              `json:"itemCount"`
	}{
		Action:    action,
		ID:        record.ID,
		Title:     record.Title,
		Preview:   record.Preview,
		Archived:  record.Archived,
		ItemCount: len(record.Items),
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(&payload)
}

func writeSessionPicker(stdout io.Writer, store *session.Store, action string, opts *cli.SessionOptions) error {
	if stdout == nil {
		return nil
	}
	records, err := listSessionPickerRecords(store, opts, sessionPickerPageSize)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintf(stdout, "No sessions found to %s.\n", action)
		return nil
	}
	response := &sessionPickerResponse{
		Action:   action,
		Count:    len(records),
		Command:  action,
		Sessions: sessionPickerEntries(records),
		Hint:     fmt.Sprintf("Run `codex %s SESSION_ID` with one of these IDs.", action),
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(response)
}

func listSessionPickerRecords(store *session.Store, opts *cli.SessionOptions, limit int) ([]session.Record, error) {
	if store == nil {
		return nil, errors.New("session store is nil")
	}
	if limit <= 0 {
		limit = sessionPickerPageSize
	}
	records, err := listSessionsByArchived(store, false)
	if err != nil {
		return nil, err
	}
	if opts != nil && opts.All {
		archived, err := listSessionsByArchived(store, true)
		if err != nil {
			return nil, err
		}
		records = append(records, archived...)
	}
	records = filterSessionPickerRecords(records, opts)
	sortSessionRecordsByRecency(records)
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func listSessionsByArchived(store *session.Store, archived bool) ([]session.Record, error) {
	page, err := store.List(session.ListOptions{
		SortKey:        session.SortRecencyAt,
		SortDirection:  session.SortDesc,
		Archived:       archived,
		IncludeHistory: false,
	})
	if err != nil {
		return nil, err
	}
	return append([]session.Record(nil), page.Records...), nil
}

func filterSessionPickerRecords(records []session.Record, opts *cli.SessionOptions) []session.Record {
	if len(records) == 0 {
		return nil
	}
	includeNonInteractive := opts != nil && opts.IncludeNonInteractive
	filtered := make([]session.Record, 0, len(records))
	for i := range records {
		record := &records[i]
		if !includeNonInteractive && !isInteractiveSession(record) {
			continue
		}
		filtered = append(filtered, *record)
	}
	return filtered
}

func isInteractiveSession(record *session.Record) bool {
	if record == nil {
		return false
	}
	source := normalizeSessionSource(record.Metadata.Source)
	switch source {
	case "", "cli", "vscode":
		return true
	case "exec", "appserver", "subagent", "subagentreview", "subagentcompact", "subagentthreadspawn", "subagentother", "unknown":
		return false
	default:
		return true
	}
}

func normalizeSessionSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	source = strings.ReplaceAll(source, "_", "")
	source = strings.ReplaceAll(source, "-", "")
	return source
}

func sortSessionRecordsByRecency(records []session.Record) {
	sort.SliceStable(records, func(i int, j int) bool {
		left := sessionRecordRecency(&records[i])
		right := sessionRecordRecency(&records[j])
		if left.Equal(right) {
			return records[i].ID > records[j].ID
		}
		return left.After(right)
	})
}

func sessionRecordRecency(record *session.Record) time.Time {
	if record == nil {
		return time.Time{}
	}
	if !record.RecencyAt.IsZero() {
		return record.RecencyAt
	}
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt
	}
	return record.CreatedAt
}

func sessionPickerEntries(records []session.Record) []*sessionPickerEntry {
	entries := make([]*sessionPickerEntry, 0, len(records))
	for i := range records {
		entries = append(entries, sessionPickerEntryFromRecord(&records[i]))
	}
	return entries
}

func sessionPickerEntryFromRecord(record *session.Record) *sessionPickerEntry {
	if record == nil {
		return nil
	}
	updatedAt := sessionRecordRecency(record)
	entry := &sessionPickerEntry{
		ID:            record.ID,
		Title:         record.Title,
		Preview:       record.Preview,
		Archived:      record.Archived,
		CWD:           record.Metadata.CWD,
		Model:         record.Metadata.Model,
		ModelProvider: record.Metadata.ModelProvider,
		Source:        record.Metadata.Source,
		ThreadSource:  record.Metadata.ThreadSource,
	}
	if !updatedAt.IsZero() {
		entry.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	}
	return entry
}

type sessionPickerResponse struct {
	Action   string                `json:"action"`
	Count    int                   `json:"count"`
	Command  string                `json:"command"`
	Sessions []*sessionPickerEntry `json:"sessions"`
	Hint     string                `json:"hint,omitempty"`
}

type sessionPickerEntry struct {
	ID            session.ThreadID `json:"id"`
	Title         string           `json:"title,omitempty"`
	Preview       string           `json:"preview,omitempty"`
	Archived      bool             `json:"archived"`
	UpdatedAt     string           `json:"updatedAt,omitempty"`
	CWD           string           `json:"cwd,omitempty"`
	Model         string           `json:"model,omitempty"`
	ModelProvider string           `json:"modelProvider,omitempty"`
	Source        string           `json:"source,omitempty"`
	ThreadSource  string           `json:"threadSource,omitempty"`
}

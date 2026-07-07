package doctor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/internal/rollout"
	"codex_go/internal/utils"
)

const (
	maxRolloutParityScanFiles = 10_000
	rolloutParitySampleLimit  = 5
	rolloutParitySummaryLimit = 8
)

type rolloutParityFile struct {
	Path     string
	Key      string
	Archived bool
	ThreadID string
}

type rolloutParityScan struct {
	Files          []*rolloutParityFile
	ScanErrors     []string
	MalformedNames []string
	ReachedScanCap bool
}

type rolloutParityRecord struct {
	ID            string
	RolloutPath   string
	Key           string
	Archived      bool
	Source        string
	ModelProvider string
}

func rolloutDBParityCheck(codexHome string, opts *Options) *DoctorCheck {
	scan := scanRolloutParityFiles(codexHome)
	cfg, _ := loadEffectiveConfigForDoctor(codexHome, opts)
	defaultProvider := effectiveProviderIDForDoctor(opts, cfg)
	stateDBPath := filepath.Join(sqliteHomeForDoctor(codexHome, opts), "state_5.sqlite")
	details := []string{
		"default model provider: " + defaultProvider,
		fmt.Sprintf("rollout DB active files: %d", rolloutParityFileCount(scan.Files, false)),
		fmt.Sprintf("rollout DB archived files: %d", rolloutParityFileCount(scan.Files, true)),
		fmt.Sprintf("rollout DB scan errors: %d", len(scan.ScanErrors)),
		fmt.Sprintf("rollout DB malformed file names: %d", len(scan.MalformedNames)),
		fmt.Sprintf("rollout DB scan cap reached: %t", scan.ReachedScanCap),
	}
	pushDoctorSamples(&details, "rollout DB scan error sample", scan.ScanErrors)
	pushDoctorSamples(&details, "rollout DB malformed file sample", scan.MalformedNames)

	if !isRegularFileForRolloutParity(stateDBPath) {
		details = append(details, "rollout DB rows: skipped (state DB missing)")
		return missingStateDBRolloutParityCheck(scan, details)
	}

	records, err := rolloutParityRecordsFromStateDB(stateDBPath)
	if err != nil {
		details = append(details, "rollout DB read error: "+err.Error())
		return NewCheck("state.rollout_db_parity", "threads", CheckStatusWarning, "state database thread inventory could not be read").
			DetailsList(details).
			Issue(NewIssue(CheckStatusWarning, "state DB thread rows could not be queried").
				WithMeasured(err.Error()).
				WithExpected("readable threads table"))
	}

	return rolloutParityCheckFromScanAndRecords(codexHome, scan, records, details)
}

func missingStateDBRolloutParityCheck(scan *rolloutParityScan, details []string) *DoctorCheck {
	if scan == nil {
		scan = &rolloutParityScan{}
	}
	if len(scan.Files) == 0 && len(scan.ScanErrors) == 0 && len(scan.MalformedNames) == 0 && !scan.ReachedScanCap {
		return NewCheck("state.rollout_db_parity", "threads", CheckStatusOK, "no rollout/state DB inventory to compare").
			DetailsList(details)
	}
	summary := "state DB is missing while rollout files exist"
	if len(scan.Files) == 0 {
		summary = "rollout scan was incomplete or found bad files"
	}
	check := NewCheck("state.rollout_db_parity", "threads", CheckStatusWarning, summary).DetailsList(details)
	if len(scan.Files) > 0 {
		remedy := "Start Codex with no state DB present so startup backfill can create it from rollout files."
		check.Issue(NewIssue(CheckStatusWarning, "rollout files exist but the state DB is missing").
			WithMeasured(fmt.Sprintf("%d rollout files", len(scan.Files))).
			WithExpected("state DB contains matching thread rows").
			WithRemedy(remedy)).
			Remediate(remedy)
	}
	if len(scan.ScanErrors) > 0 || len(scan.MalformedNames) > 0 || scan.ReachedScanCap {
		check.Issue(NewIssue(CheckStatusWarning, "rollout scan was incomplete or found bad files").
			WithMeasured(fmt.Sprintf("%d scan errors, %d malformed names, scan cap reached: %t", len(scan.ScanErrors), len(scan.MalformedNames), scan.ReachedScanCap)).
			WithExpected("rollout directories are fully scannable").
			WithRemedy("Check file permissions and unexpected files under CODEX_HOME sessions."))
	}
	return check
}

func rolloutParityCheckFromScanAndRecords(codexHome string, scan *rolloutParityScan, records []*rolloutParityRecord, details []string) *DoctorCheck {
	if scan == nil {
		scan = &rolloutParityScan{}
	}
	rolloutByKey := map[string]*rolloutParityFile{}
	for _, file := range scan.Files {
		if file == nil || strings.TrimSpace(file.Key) == "" {
			continue
		}
		rolloutByKey[file.Key] = file
	}
	rowsByKey := map[string][]*rolloutParityRecord{}
	for _, record := range records {
		if record == nil || strings.TrimSpace(record.Key) == "" {
			continue
		}
		rowsByKey[record.Key] = append(rowsByKey[record.Key], record)
	}

	missingActiveRows := rolloutMissingRecordPaths(scan.Files, rowsByKey, false)
	missingArchivedRows := rolloutMissingRecordPaths(scan.Files, rowsByKey, true)
	scanComplete := !scan.ReachedScanCap
	staleRows := []string{}
	archiveMismatches := []string{}
	if scanComplete {
		staleRows = staleRecordPaths(records)
		archiveMismatches = archiveMismatchPaths(codexHome, records, rolloutByKey)
	}
	duplicateRolloutThreadIDs := duplicateRolloutIDs(scan.Files)
	duplicateDBPaths := duplicateDBPathKeys(rowsByKey)

	activeRows, archivedRows := rolloutParityRecordCounts(records)
	details = append(details,
		fmt.Sprintf("rollout DB rows: %d", len(records)),
		fmt.Sprintf("rollout DB active rows: %d", activeRows),
		fmt.Sprintf("rollout DB archived rows: %d", archivedRows),
		fmt.Sprintf("rollout DB missing active rows: %d", len(missingActiveRows)),
		fmt.Sprintf("rollout DB missing archived rows: %d", len(missingArchivedRows)),
		fmt.Sprintf("rollout DB stale rows: %s", rolloutParityCountOrSkipped(len(staleRows), scanComplete)),
		fmt.Sprintf("rollout DB archive mismatches: %s", rolloutParityCountOrSkipped(len(archiveMismatches), scanComplete)),
		fmt.Sprintf("rollout DB duplicate rollout thread ids: %d", len(duplicateRolloutThreadIDs)),
		fmt.Sprintf("rollout DB duplicate DB paths: %d", len(duplicateDBPaths)),
		"rollout DB model providers: "+rolloutParityCountSummary(rolloutParityRecordModelProviders(records)),
		"rollout DB sources: "+rolloutParityCountSummary(rolloutParityRecordSourceCategories(records)),
	)
	pushDoctorSamples(&details, "rollout DB missing active sample", missingActiveRows)
	pushDoctorSamples(&details, "rollout DB missing archived sample", missingArchivedRows)
	pushDoctorSamples(&details, "rollout DB stale row sample", staleRows)
	pushDoctorSamples(&details, "rollout DB archive mismatch sample", archiveMismatches)
	pushDoctorSamples(&details, "rollout DB duplicate rollout thread id sample", duplicateRolloutThreadIDs)
	pushDoctorSamples(&details, "rollout DB duplicate DB path sample", duplicateDBPaths)

	status := CheckStatusOK
	summary := "rollout files and state DB thread inventory agree"
	if len(scan.ScanErrors) > 0 ||
		len(scan.MalformedNames) > 0 ||
		scan.ReachedScanCap ||
		len(missingActiveRows) > 0 ||
		len(missingArchivedRows) > 0 ||
		len(staleRows) > 0 ||
		len(archiveMismatches) > 0 ||
		len(duplicateRolloutThreadIDs) > 0 ||
		len(duplicateDBPaths) > 0 {
		status = CheckStatusWarning
		summary = "rollout files and state DB thread inventory differ"
	}
	check := NewCheck("state.rollout_db_parity", "threads", status, summary).DetailsList(details)
	if len(missingActiveRows) > 0 || len(missingArchivedRows) > 0 {
		check.Issue(NewIssue(CheckStatusWarning, "rollout files are missing from the state DB").
			WithMeasured(fmt.Sprintf("%d active, %d archived", len(missingActiveRows), len(missingArchivedRows))).
			WithExpected("every rollout file has a matching threads row"))
	}
	if len(staleRows) > 0 {
		check.Issue(NewIssue(CheckStatusWarning, "state DB rows point at missing or unusable rollout files").
			WithMeasured(fmt.Sprintf("%d stale rows", len(staleRows))).
			WithExpected("every state DB rollout path is a file on disk"))
	}
	if len(archiveMismatches) > 0 {
		check.Issue(NewIssue(CheckStatusWarning, "state DB archive flags disagree with rollout file locations").
			WithMeasured(fmt.Sprintf("%d mismatched rows", len(archiveMismatches))).
			WithExpected("rows under archived_sessions are archived and rows under sessions are active"))
	}
	if len(duplicateRolloutThreadIDs) > 0 || len(duplicateDBPaths) > 0 {
		check.Issue(NewIssue(CheckStatusWarning, "duplicate thread inventory entries found").
			WithMeasured(fmt.Sprintf("%d duplicate rollout thread ids, %d duplicate DB paths", len(duplicateRolloutThreadIDs), len(duplicateDBPaths))).
			WithExpected("one rollout path and thread id per thread").
			WithRemedy("Attach the doctor report to a bug report so support can inspect samples."))
	}
	if len(scan.ScanErrors) > 0 || len(scan.MalformedNames) > 0 || scan.ReachedScanCap {
		check.Issue(NewIssue(CheckStatusWarning, "rollout scan was incomplete or found bad files").
			WithMeasured(fmt.Sprintf("%d scan errors, %d malformed names, scan cap reached: %t", len(scan.ScanErrors), len(scan.MalformedNames), scan.ReachedScanCap)).
			WithExpected("rollout directories are fully scannable").
			WithRemedy("Check file permissions and unexpected files under CODEX_HOME sessions."))
	}
	return check
}

func scanRolloutParityFiles(codexHome string) *rolloutParityScan {
	scan := &rolloutParityScan{}
	scanRolloutParityRoot(filepath.Join(codexHome, rollout.SessionsSubdir), false, scan)
	scanRolloutParityRoot(filepath.Join(codexHome, rollout.ArchivedSessionsSubdir), true, scan)
	return scan
}

func scanRolloutParityRoot(root string, archived bool, scan *rolloutParityScan) {
	if scan == nil || scan.ReachedScanCap {
		return
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if scan.ReachedScanCap {
			return filepath.SkipAll
		}
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			scan.recordError(fmt.Sprintf("%s (%v)", path, err))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry == nil || entry.IsDir() {
			return nil
		}
		if !isRolloutParityFileName(entry.Name()) {
			return nil
		}
		item, ok := rollout.BuildThreadItem(path, archived)
		if !ok || strings.TrimSpace(item.ThreadID) == "" {
			scan.recordMalformed(path)
			return nil
		}
		scan.recordFile(&rolloutParityFile{
			Path:     path,
			Key:      rolloutParityPathKey(path),
			Archived: archived,
			ThreadID: item.ThreadID,
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		scan.recordError(fmt.Sprintf("%s (%v)", root, err))
	}
}

func (s *rolloutParityScan) recordFile(file *rolloutParityFile) {
	if s == nil || file == nil {
		return
	}
	if s.candidateCount() >= maxRolloutParityScanFiles {
		s.ReachedScanCap = true
		return
	}
	s.Files = append(s.Files, file)
}

func (s *rolloutParityScan) recordError(message string) {
	if s == nil {
		return
	}
	if s.candidateCount() >= maxRolloutParityScanFiles {
		s.ReachedScanCap = true
		return
	}
	s.ScanErrors = append(s.ScanErrors, message)
}

func (s *rolloutParityScan) recordMalformed(path string) {
	if s == nil {
		return
	}
	if s.candidateCount() >= maxRolloutParityScanFiles {
		s.ReachedScanCap = true
		return
	}
	s.MalformedNames = append(s.MalformedNames, path)
}

func (s *rolloutParityScan) candidateCount() int {
	if s == nil {
		return 0
	}
	return len(s.Files) + len(s.ScanErrors) + len(s.MalformedNames)
}

func rolloutParityRecordsFromStateDB(path string) ([]*rolloutParityRecord, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, rollout_path, archived, source, model_provider FROM threads")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []*rolloutParityRecord{}
	for rows.Next() {
		var id string
		var rolloutPath string
		var archived int
		var source string
		var modelProvider string
		if err := rows.Scan(&id, &rolloutPath, &archived, &source, &modelProvider); err != nil {
			return nil, err
		}
		records = append(records, &rolloutParityRecord{
			ID:            id,
			RolloutPath:   rolloutPath,
			Key:           rolloutParityPathKey(rolloutPath),
			Archived:      archived != 0,
			Source:        source,
			ModelProvider: modelProvider,
		})
	}
	return records, rows.Err()
}

func isRolloutParityFileName(name string) bool {
	return strings.HasPrefix(name, "rollout-") && filepath.Ext(name) == ".jsonl"
}

func rolloutParityFileCount(files []*rolloutParityFile, archived bool) int {
	count := 0
	for _, file := range files {
		if file != nil && file.Archived == archived {
			count++
		}
	}
	return count
}

func rolloutParityRecordCounts(records []*rolloutParityRecord) (int, int) {
	active := 0
	archived := 0
	for _, record := range records {
		if record == nil {
			continue
		}
		if record.Archived {
			archived++
		} else {
			active++
		}
	}
	return active, archived
}

func rolloutMissingRecordPaths(files []*rolloutParityFile, rowsByKey map[string][]*rolloutParityRecord, archived bool) []string {
	var missing []string
	for _, file := range files {
		if file == nil || file.Archived != archived {
			continue
		}
		if !hasMatchingRolloutParityRow(file, rowsByKey) {
			missing = append(missing, file.Path)
		}
	}
	sort.Strings(missing)
	return uniqueStrings(missing)
}

func hasMatchingRolloutParityRow(file *rolloutParityFile, rowsByKey map[string][]*rolloutParityRecord) bool {
	if file == nil {
		return false
	}
	for _, row := range rowsByKey[file.Key] {
		if row != nil && row.ID == file.ThreadID {
			return true
		}
	}
	return false
}

func staleRecordPaths(records []*rolloutParityRecord) []string {
	var stale []string
	for _, record := range records {
		if record == nil || !isRegularFileForRolloutParity(record.RolloutPath) {
			if record != nil {
				stale = append(stale, record.RolloutPath)
			}
		}
	}
	sort.Strings(stale)
	return stale
}

func archiveMismatchPaths(codexHome string, records []*rolloutParityRecord, rolloutByKey map[string]*rolloutParityFile) []string {
	var mismatches []string
	for _, record := range records {
		if record == nil {
			continue
		}
		expected, ok := expectedArchivedForRolloutRecord(codexHome, record, rolloutByKey)
		if !ok {
			continue
		}
		if expected != record.Archived {
			mismatches = append(mismatches, record.RolloutPath)
		}
	}
	sort.Strings(mismatches)
	return mismatches
}

func expectedArchivedForRolloutRecord(codexHome string, record *rolloutParityRecord, rolloutByKey map[string]*rolloutParityFile) (bool, bool) {
	if record == nil {
		return false, false
	}
	if file := rolloutByKey[record.Key]; file != nil {
		return file.Archived, true
	}
	if !isRegularFileForRolloutParity(record.RolloutPath) {
		return false, false
	}
	return archivedFromRolloutPath(codexHome, record.RolloutPath)
}

func duplicateRolloutIDs(files []*rolloutParityFile) []string {
	seen := map[string]bool{}
	duplicates := map[string]bool{}
	for _, file := range files {
		if file == nil || strings.TrimSpace(file.ThreadID) == "" {
			continue
		}
		if seen[file.ThreadID] {
			duplicates[file.ThreadID] = true
			continue
		}
		seen[file.ThreadID] = true
	}
	out := make([]string, 0, len(duplicates))
	for id := range duplicates {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func duplicateDBPathKeys(rowsByKey map[string][]*rolloutParityRecord) []string {
	out := []string{}
	for key, rows := range rowsByKey {
		if len(rows) > 1 {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	last := ""
	for _, value := range values {
		if value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}

func rolloutParityCountOrSkipped(count int, complete bool) string {
	if complete {
		return fmt.Sprintf("%d", count)
	}
	return "skipped (scan cap reached)"
}

func rolloutParityRecordModelProviders(records []*rolloutParityRecord) []string {
	values := make([]string, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		values = append(values, record.ModelProvider)
	}
	return values
}

func rolloutParityRecordSourceCategories(records []*rolloutParityRecord) []string {
	values := make([]string, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		values = append(values, rolloutParitySourceCategory(record.Source))
	}
	return values
}

func rolloutParityCountSummary(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	counts := map[string]int{}
	for _, value := range values {
		counts[value]++
	}
	type entry struct {
		value string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for value, count := range counts {
		entries = append(entries, entry{value: value, count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].value < entries[j].value
	})
	omittedCategories := 0
	omittedRows := 0
	if len(entries) > rolloutParitySummaryLimit {
		omittedCategories = len(entries) - rolloutParitySummaryLimit
		for _, entry := range entries[rolloutParitySummaryLimit:] {
			omittedRows += entry.count
		}
		entries = entries[:rolloutParitySummaryLimit]
	}
	parts := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		parts = append(parts, fmt.Sprintf("%s=%d", entry.value, entry.count))
	}
	if omittedCategories > 0 {
		parts = append(parts, fmt.Sprintf("other=%d across %d categories", omittedRows, omittedCategories))
	}
	return strings.Join(parts, ", ")
}

func rolloutParitySourceCategory(source string) string {
	value := strings.TrimSpace(source)
	switch strings.Trim(value, `"`) {
	case "cli":
		return "cli"
	case "vscode":
		return "vscode"
	case "exec":
		return "exec"
	case "mcp":
		return "mcp"
	case "unknown":
		return "unknown"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return "unknown"
	}
	if _, ok := parsed["custom"]; ok {
		return "custom"
	}
	if internal, ok := parsed["internal"]; ok && fmt.Sprint(internal) == "memory_consolidation" {
		return "internal:memory_consolidation"
	}
	subagent, ok := parsed["subagent"]
	if !ok {
		return "unknown"
	}
	switch typed := subagent.(type) {
	case string:
		switch typed {
		case "review":
			return "subagent:review"
		case "compact":
			return "subagent:compact"
		case "memory_consolidation":
			return "subagent:memory_consolidation"
		default:
			return "subagent:other"
		}
	case map[string]any:
		if _, ok := typed["thread_spawn"]; ok {
			return "subagent:thread_spawn"
		}
		if _, ok := typed["other"]; ok {
			return "subagent:other"
		}
	}
	return "unknown"
}

func archivedFromRolloutPath(codexHome string, path string) (bool, bool) {
	key := rolloutParityPathKey(path)
	archivedRoot := rolloutParityPathKey(filepath.Join(codexHome, rollout.ArchivedSessionsSubdir))
	activeRoot := rolloutParityPathKey(filepath.Join(codexHome, rollout.SessionsSubdir))
	if pathKeyHasPrefix(key, archivedRoot) {
		return true, true
	}
	if pathKeyHasPrefix(key, activeRoot) {
		return false, true
	}
	return false, false
}

func pathKeyHasPrefix(key string, root string) bool {
	if key == root {
		return true
	}
	if root == "" || key == "" {
		return false
	}
	rel, err := filepath.Rel(root, key)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func rolloutParityPathKey(path string) string {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return ""
	}
	normalized, err := utils.NormalizeForPathComparison(raw)
	if err != nil {
		return raw
	}
	return normalized
}

func isRegularFileForRolloutParity(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func pushDoctorSamples(details *[]string, label string, values []string) {
	if details == nil || len(values) == 0 {
		return
	}
	limit := len(values)
	if limit > rolloutParitySampleLimit {
		limit = rolloutParitySampleLimit
	}
	for _, value := range values[:limit] {
		*details = append(*details, fmt.Sprintf("%s: %s", label, value))
	}
}

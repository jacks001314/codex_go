package runtimeutil

import (
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const ZeroOID = "0000000000000000000000000000000000000000"

type FileChangeKind string

const (
	ChangeAdd    FileChangeKind = "add"
	ChangeDelete FileChangeKind = "delete"
	ChangeUpdate FileChangeKind = "update"
)

type FileChange struct {
	Kind               FileChangeKind
	Path               string
	MovePath           *string
	OldContent         string
	NewContent         string
	OverwrittenContent *string
}

type DiffTracker struct {
	valid        bool
	displayRoots map[string]string
	baseline     map[trackedPath]string
	current      map[trackedPath]string
	origin       map[trackedPath]trackedPath
	unified      *string
}

type trackedPath struct {
	EnvironmentID string
	Path          string
}

func NewDiffTracker() *DiffTracker {
	return &DiffTracker{
		valid:    true,
		baseline: map[trackedPath]string{},
		current:  map[trackedPath]string{},
		origin:   map[trackedPath]trackedPath{},
	}
}

func NewDiffTrackerWithDisplayRoots(roots map[string]string) *DiffTracker {
	tracker := NewDiffTracker()
	tracker.displayRoots = map[string]string{}
	for key, value := range roots {
		tracker.displayRoots[key] = filepath.Clean(value)
	}
	return tracker
}

func (t *DiffTracker) Track(environmentID string, changes []FileChange, exact bool) {
	if !t.valid {
		return
	}
	if !exact {
		t.Invalidate()
		return
	}
	for i := range changes {
		t.apply(environmentID, &changes[i])
	}
	t.refresh()
}

func (t *DiffTracker) Invalidate() {
	t.valid = false
	t.unified = nil
}

func (t *DiffTracker) UnifiedDiff() *string {
	if t.unified == nil {
		return nil
	}
	clone := *t.unified
	return &clone
}

func (t *DiffTracker) HasUnifiedDiff() bool {
	return t.unified != nil && *t.unified != ""
}

func (t *DiffTracker) apply(environmentID string, change *FileChange) {
	path := trackedPath{EnvironmentID: environmentID, Path: filepath.Clean(change.Path)}
	switch change.Kind {
	case ChangeAdd:
		if _, ok := t.baseline[path]; !ok && change.OverwrittenContent != nil {
			t.baseline[path] = *change.OverwrittenContent
		}
		t.current[path] = change.NewContent
		delete(t.origin, path)
	case ChangeDelete:
		if _, ok := t.baseline[path]; !ok {
			t.baseline[path] = change.OldContent
		}
		delete(t.current, path)
		delete(t.origin, path)
	case ChangeUpdate:
		if _, ok := t.baseline[path]; !ok {
			t.baseline[path] = change.OldContent
		}
		if change.MovePath != nil {
			dest := trackedPath{EnvironmentID: environmentID, Path: filepath.Clean(*change.MovePath)}
			if _, ok := t.baseline[dest]; !ok && change.OverwrittenContent != nil {
				t.baseline[dest] = *change.OverwrittenContent
			}
			origin := path
			if existing, ok := t.origin[path]; ok {
				origin = existing
			}
			delete(t.current, path)
			t.current[dest] = change.NewContent
			if dest != origin {
				t.origin[dest] = origin
			}
			return
		}
		t.current[path] = change.NewContent
	}
}

func (t *DiffTracker) refresh() {
	pathsByKey := map[trackedPath]bool{}
	for path := range t.baseline {
		pathsByKey[path] = true
	}
	for path := range t.current {
		pathsByKey[path] = true
	}
	paths := make([]trackedPath, 0, len(pathsByKey))
	for path := range pathsByKey {
		paths = append(paths, path)
	}
	sort.SliceStable(paths, func(i int, j int) bool {
		return t.displayPath(paths[i]) < t.displayPath(paths[j])
	})
	var builder strings.Builder
	for _, path := range paths {
		oldContent, oldOK := t.baseline[path]
		newContent, newOK := t.current[path]
		if oldOK && newOK && oldContent == newContent {
			continue
		}
		builder.WriteString(t.render(path, oldContent, oldOK, path, newContent, newOK))
	}
	if builder.Len() == 0 {
		t.unified = nil
		return
	}
	diff := builder.String()
	t.unified = &diff
}

func (t *DiffTracker) render(leftPath trackedPath, oldContent string, oldOK bool, rightPath trackedPath, newContent string, newOK bool) string {
	leftDisplay := t.displayPath(leftPath)
	rightDisplay := t.displayPath(rightPath)
	leftOID := ZeroOID
	if oldOK {
		leftOID = blobOID(oldContent)
	}
	rightOID := ZeroOID
	if newOK {
		rightOID = blobOID(newContent)
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", leftDisplay, rightDisplay))
	if !oldOK && newOK {
		builder.WriteString("new file mode 100644\n")
	}
	if oldOK && !newOK {
		builder.WriteString("deleted file mode 100644\n")
	}
	builder.WriteString(fmt.Sprintf("index %s..%s\n", leftOID, rightOID))
	oldHeader := "a/" + leftDisplay
	if !oldOK {
		oldHeader = "/dev/null"
	}
	newHeader := "b/" + rightDisplay
	if !newOK {
		newHeader = "/dev/null"
	}
	builder.WriteString("--- " + oldHeader + "\n")
	builder.WriteString("+++ " + newHeader + "\n")
	builder.WriteString("@@ -1 +1 @@\n")
	for _, line := range splitLines(oldContent) {
		if oldOK {
			builder.WriteString("-" + line + "\n")
		}
	}
	for _, line := range splitLines(newContent) {
		if newOK {
			builder.WriteString("+" + line + "\n")
		}
	}
	return builder.String()
}

func (t *DiffTracker) displayPath(path trackedPath) string {
	display := filepath.Clean(path.Path)
	if root, ok := t.displayRoots[path.EnvironmentID]; ok {
		if rel, err := filepath.Rel(root, display); err == nil && !strings.HasPrefix(rel, "..") {
			display = rel
		}
	}
	display = filepath.ToSlash(display)
	if len(t.displayRoots) > 1 && path.EnvironmentID != "" {
		return path.EnvironmentID + "/" + display
	}
	return display
}

func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func blobOID(content string) string {
	data := []byte(content)
	header := fmt.Sprintf("blob %d\x00", len(data))
	sum := sha1.Sum(append([]byte(header), data...))
	return fmt.Sprintf("%x", sum)
}

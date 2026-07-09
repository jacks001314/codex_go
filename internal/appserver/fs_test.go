package appserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceReadWriteFile(t *testing.T) {
	service := NewFSService()
	path := filepath.Join(t.TempDir(), "note.txt")
	payload := base64.StdEncoding.EncodeToString([]byte("hello"))

	if _, err := service.WriteFile(&WriteFileParams{Path: path, DataBase64: payload}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	response, err := service.ReadFile(&ReadFileParams{Path: path})
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("decoded = %q, want hello", decoded)
	}
}

func TestServiceRejectsRelativePath(t *testing.T) {
	service := NewFSService()
	_, err := service.ReadFile(&ReadFileParams{Path: "relative.txt"})
	if !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("ReadFile() error = %v, want ErrInvalidFSRequest", err)
	}
}

func TestFSWireShapesMatchRustSchema(t *testing.T) {
	readDirectory, err := json.Marshal(&ReadDirectoryResponse{})
	if err != nil {
		t.Fatalf("Marshal(ReadDirectoryResponse) error = %v", err)
	}
	if string(readDirectory) != `{"entries":[]}` {
		t.Fatalf("ReadDirectoryResponse JSON = %s", readDirectory)
	}

	changed, err := json.Marshal(&ChangedNotification{WatchID: "watch-1"})
	if err != nil {
		t.Fatalf("Marshal(ChangedNotification) error = %v", err)
	}
	if string(changed) != `{"watchId":"watch-1","changedPaths":[]}` {
		t.Fatalf("ChangedNotification JSON = %s", changed)
	}

	metadata, err := json.Marshal(&GetMetadataResponse{})
	if err != nil {
		t.Fatalf("Marshal(GetMetadataResponse) error = %v", err)
	}
	for _, want := range []string{`"isDirectory":false`, `"isFile":false`, `"isSymlink":false`, `"createdAtMs":0`, `"modifiedAtMs":0`} {
		if !strings.Contains(string(metadata), want) {
			t.Fatalf("GetMetadataResponse JSON missing %s: %s", want, metadata)
		}
	}

	watch, err := json.Marshal(&WatchParams{WatchID: "watch-1", Path: "/repo"})
	if err != nil {
		t.Fatalf("Marshal(WatchParams) error = %v", err)
	}
	if string(watch) != `{"watchId":"watch-1","path":"/repo"}` {
		t.Fatalf("WatchParams JSON = %s", watch)
	}

	createDirectory, err := json.Marshal(&CreateDirectoryParams{Path: "/repo/new"})
	if err != nil {
		t.Fatalf("Marshal(CreateDirectoryParams) error = %v", err)
	}
	if string(createDirectory) != `{"path":"/repo/new","recursive":null}` {
		t.Fatalf("CreateDirectoryParams JSON = %s", createDirectory)
	}

	remove, err := json.Marshal(&RemoveParams{Path: "/repo/old"})
	if err != nil {
		t.Fatalf("Marshal(RemoveParams) error = %v", err)
	}
	if string(remove) != `{"path":"/repo/old","recursive":null,"force":null}` {
		t.Fatalf("RemoveParams JSON = %s", remove)
	}

	copyFile, err := json.Marshal(&CopyParams{SourcePath: "/repo/a", DestinationPath: "/repo/b"})
	if err != nil {
		t.Fatalf("Marshal(CopyParams) error = %v", err)
	}
	if string(copyFile) != `{"sourcePath":"/repo/a","destinationPath":"/repo/b"}` {
		t.Fatalf("CopyParams JSON = %s", copyFile)
	}

	copyRecursive, err := json.Marshal(&CopyParams{SourcePath: "/repo/a", DestinationPath: "/repo/b", Recursive: true})
	if err != nil {
		t.Fatalf("Marshal(CopyParams recursive) error = %v", err)
	}
	if string(copyRecursive) != `{"sourcePath":"/repo/a","destinationPath":"/repo/b","recursive":true}` {
		t.Fatalf("CopyParams recursive JSON = %s", copyRecursive)
	}

	unwatch, err := json.Marshal(&UnwatchParams{WatchID: "watch-1"})
	if err != nil {
		t.Fatalf("Marshal(UnwatchParams) error = %v", err)
	}
	if string(unwatch) != `{"watchId":"watch-1"}` {
		t.Fatalf("UnwatchParams JSON = %s", unwatch)
	}

	for name, response := range map[string]any{
		"WriteFileResponse":       &WriteFileResponse{},
		"CreateDirectoryResponse": &CreateDirectoryResponse{},
		"RemoveResponse":          &RemoveResponse{},
		"CopyResponse":            &CopyResponse{},
		"UnwatchResponse":         &UnwatchResponse{},
	} {
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", name, err)
		}
		if string(data) != `{}` {
			t.Fatalf("%s JSON = %s", name, data)
		}
	}
}

func TestServiceDirectoryMetadataCopyAndRemove(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "src")
	nested := filepath.Join(sourceDir, "nested")
	if _, err := service.CreateDirectory(&CreateDirectoryParams{Path: nested}); err != nil {
		t.Fatalf("CreateDirectory() error = %v", err)
	}
	sourceFile := filepath.Join(nested, "a.txt")
	if err := os.WriteFile(sourceFile, []byte("alpha"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture error = %v", err)
	}

	metadata, err := service.GetMetadata(&GetMetadataParams{Path: sourceFile})
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if !metadata.IsFile || metadata.IsDirectory {
		t.Fatalf("metadata = %+v, want file", metadata)
	}
	if metadata.ModifiedAtMS == 0 {
		t.Fatalf("metadata modifiedAtMs = 0, want file timestamp")
	}
	if runtime.GOOS == "windows" && metadata.CreatedAtMS == 0 {
		t.Fatalf("metadata createdAtMs = 0 on Windows, want file creation timestamp")
	}

	listing, err := service.ReadDirectory(&ReadDirectoryParams{Path: sourceDir})
	if err != nil {
		t.Fatalf("ReadDirectory() error = %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].FileName != "nested" || !listing.Entries[0].IsDirectory {
		t.Fatalf("entries = %+v, want nested directory", listing.Entries)
	}

	destination := filepath.Join(root, "dst")
	if _, err := service.Copy(&CopyParams{SourcePath: sourceDir, DestinationPath: destination, Recursive: true}); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(destination, "nested", "a.txt"))
	if err != nil {
		t.Fatalf("ReadFile copied fixture error = %v", err)
	}
	if string(copied) != "alpha" {
		t.Fatalf("copied = %q, want alpha", copied)
	}

	recursive := true
	if _, err := service.Remove(&RemoveParams{Path: destination, Recursive: &recursive}); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(destination) error = %v, want not exist", err)
	}
}

func TestServiceCopyDirectoryRequiresRecursive(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()
	source := filepath.Join(root, "src")
	destination := filepath.Join(root, "dst")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatalf("Mkdir fixture error = %v", err)
	}

	_, err := service.Copy(&CopyParams{SourcePath: source, DestinationPath: destination})
	if !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("Copy() error = %v, want ErrInvalidFSRequest", err)
	}
	if !strings.Contains(err.Error(), "fs/copy requires recursive: true when sourcePath is a directory") {
		t.Fatalf("Copy() error = %v", err)
	}
}

func TestServiceCopyDirectoryRejectsDescendant(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()
	source := filepath.Join(root, "src")
	destination := filepath.Join(source, "nested", "copy")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll fixture error = %v", err)
	}

	_, err := service.Copy(&CopyParams{SourcePath: source, DestinationPath: destination, Recursive: true})
	if !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("Copy() error = %v, want ErrInvalidFSRequest", err)
	}
	if err.Error() != "fs/copy cannot copy a directory to itself or one of its descendants" {
		t.Fatalf("Copy() error = %v", err)
	}
}

func TestServiceCopyPreservesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on Windows")
	}
	service := NewFSService()
	root := t.TempDir()
	source := filepath.Join(root, "src")
	nested := filepath.Join(source, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll fixture error = %v", err)
	}
	if err := os.Symlink("nested", filepath.Join(source, "nested-link")); err != nil {
		t.Fatalf("Symlink fixture error = %v", err)
	}
	destination := filepath.Join(root, "dst")
	if _, err := service.Copy(&CopyParams{SourcePath: source, DestinationPath: destination, Recursive: true}); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	copiedLink := filepath.Join(destination, "nested-link")
	info, err := os.Lstat(copiedLink)
	if err != nil {
		t.Fatalf("Lstat(copiedLink) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("copied link mode = %v, want symlink", info.Mode())
	}
	target, err := os.Readlink(copiedLink)
	if err != nil {
		t.Fatalf("Readlink(copiedLink) error = %v", err)
	}
	if target != "nested" {
		t.Fatalf("symlink target = %q, want nested", target)
	}
}

func TestServiceWatchChangedAndUnwatch(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()

	response, err := service.Watch(&WatchParams{WatchID: "watch-1", Path: root})
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if !filepath.IsAbs(response.Path) {
		t.Fatalf("Watch() path = %q, want absolute", response.Path)
	}
	if _, err := service.Watch(&WatchParams{WatchID: "watch-1", Path: root}); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("duplicate Watch() error = %v, want ErrInvalidFSRequest", err)
	}
	notification, ok := service.Changed("watch-1", filepath.Join(root, "a.txt"))
	if !ok {
		t.Fatalf("Changed() ok = false, want true")
	}
	if notification.WatchID != "watch-1" || len(notification.ChangedPaths) != 1 {
		t.Fatalf("notification = %+v", notification)
	}
	if _, err := service.Unwatch(&UnwatchParams{WatchID: "watch-1"}); err != nil {
		t.Fatalf("Unwatch() error = %v", err)
	}
	if _, ok := service.Changed("watch-1", root); ok {
		t.Fatalf("Changed() ok = true after unwatch, want false")
	}
}

func TestServiceWatchIsConnectionScoped(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()

	if _, err := service.WatchWithConnection("conn-a", &WatchParams{WatchID: "watch-1", Path: root}); err != nil {
		t.Fatalf("WatchWithConnection(conn-a) error = %v", err)
	}
	if _, err := service.WatchWithConnection("conn-b", &WatchParams{WatchID: "watch-1", Path: root}); err != nil {
		t.Fatalf("WatchWithConnection(conn-b) error = %v", err)
	}
	if _, err := service.WatchWithConnection("conn-a", &WatchParams{WatchID: "watch-1", Path: root}); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("duplicate WatchWithConnection(conn-a) error = %v, want ErrInvalidFSRequest", err)
	}
	if service.WatchCount() != 2 {
		t.Fatalf("WatchCount() = %d, want 2", service.WatchCount())
	}

	if _, err := service.UnwatchWithConnection("conn-b", &UnwatchParams{WatchID: "watch-1"}); err != nil {
		t.Fatalf("UnwatchWithConnection(conn-b) error = %v", err)
	}
	if _, ok := service.ChangedForConnection("conn-a", "watch-1", filepath.Join(root, "a.txt")); !ok {
		t.Fatalf("ChangedForConnection(conn-a) ok = false, want true")
	}
	if _, ok := service.ChangedForConnection("conn-b", "watch-1", filepath.Join(root, "b.txt")); ok {
		t.Fatalf("ChangedForConnection(conn-b) ok = true after unwatch, want false")
	}

	service.ConnectionClosed("conn-a")
	if service.WatchCount() != 0 {
		t.Fatalf("WatchCount() = %d after ConnectionClosed, want 0", service.WatchCount())
	}
}

func TestServiceChangedForPathMatchesFileAndDirectDirectoryWatch(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()
	file := filepath.Join(root, "FETCH_HEAD")
	nestedDir := filepath.Join(root, "refs")
	nestedFile := filepath.Join(nestedDir, "main")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if _, err := service.WatchWithConnection("conn-a", &WatchParams{WatchID: "watch-dir", Path: root}); err != nil {
		t.Fatalf("WatchWithConnection(dir) error = %v", err)
	}
	if _, err := service.WatchWithConnection("conn-b", &WatchParams{WatchID: "watch-file", Path: file}); err != nil {
		t.Fatalf("WatchWithConnection(file) error = %v", err)
	}

	notifications := service.ChangedForPath(file)
	if len(notifications) != 2 {
		t.Fatalf("ChangedForPath(file) notifications = %d, want 2: %+v", len(notifications), notifications)
	}
	if notifications[0].connectionID != "conn-a" || notifications[0].notification.WatchID != "watch-dir" {
		t.Fatalf("dir watch notification = %+v", notifications[0])
	}
	if notifications[1].connectionID != "conn-b" || notifications[1].notification.WatchID != "watch-file" {
		t.Fatalf("file watch notification = %+v", notifications[1])
	}
	if notifications[0].notification.ChangedPaths[0] != file || notifications[1].notification.ChangedPaths[0] != file {
		t.Fatalf("changed paths = %+v %+v, want %q", notifications[0].notification.ChangedPaths, notifications[1].notification.ChangedPaths, file)
	}

	if nested := service.ChangedForPath(nestedFile); len(nested) != 0 {
		t.Fatalf("ChangedForPath(nested file) notifications = %+v, want none for non-recursive directory watch", nested)
	}
}

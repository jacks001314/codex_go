package appserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var ErrInvalidFSRequest = errors.New("invalid fs request")

type invalidFSRequestError struct {
	message string
}

func (e *invalidFSRequestError) Error() string {
	return e.message
}

func (e *invalidFSRequestError) Unwrap() error {
	return ErrInvalidFSRequest
}

func invalidFSRequest(message string) error {
	return &invalidFSRequestError{message: message}
}

type ReadFileParams struct {
	Path string `json:"path"`
}

type ReadFileResponse struct {
	DataBase64 string `json:"dataBase64"`
}

type WriteFileParams struct {
	Path       string `json:"path"`
	DataBase64 string `json:"dataBase64"`
}

type WriteFileResponse struct{}

type CreateDirectoryParams struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive,omitempty"`
}

func (p CreateDirectoryParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Path      string `json:"path"`
		Recursive *bool  `json:"recursive"`
	}{
		Path:      p.Path,
		Recursive: p.Recursive,
	})
}

type CreateDirectoryResponse struct{}

type GetMetadataParams struct {
	Path string `json:"path"`
}

type GetMetadataResponse struct {
	IsDirectory  bool  `json:"isDirectory"`
	IsFile       bool  `json:"isFile"`
	IsSymlink    bool  `json:"isSymlink"`
	CreatedAtMS  int64 `json:"createdAtMs"`
	ModifiedAtMS int64 `json:"modifiedAtMs"`
}

type ReadDirectoryParams struct {
	Path string `json:"path"`
}

type ReadDirectoryEntry struct {
	FileName    string `json:"fileName"`
	IsDirectory bool   `json:"isDirectory"`
	IsFile      bool   `json:"isFile"`
}

type ReadDirectoryResponse struct {
	Entries []ReadDirectoryEntry `json:"entries"`
}

func (r *ReadDirectoryResponse) MarshalJSON() ([]byte, error) {
	entries := append([]ReadDirectoryEntry(nil), r.Entries...)
	if entries == nil {
		entries = []ReadDirectoryEntry{}
	}
	return json.Marshal(struct {
		Entries []ReadDirectoryEntry `json:"entries"`
	}{Entries: entries})
}

type RemoveParams struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive,omitempty"`
	Force     *bool  `json:"force,omitempty"`
}

func (p RemoveParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Path      string `json:"path"`
		Recursive *bool  `json:"recursive"`
		Force     *bool  `json:"force"`
	}{
		Path:      p.Path,
		Recursive: p.Recursive,
		Force:     p.Force,
	})
}

type RemoveResponse struct{}

type CopyParams struct {
	SourcePath      string `json:"sourcePath"`
	DestinationPath string `json:"destinationPath"`
	Recursive       bool   `json:"recursive,omitempty"`
}

type CopyResponse struct{}

type WatchParams struct {
	WatchID string `json:"watchId"`
	Path    string `json:"path"`
}

type WatchResponse struct {
	Path string `json:"path"`
}

type UnwatchParams struct {
	WatchID string `json:"watchId"`
}

type UnwatchResponse struct{}

type ChangedNotification struct {
	WatchID      string   `json:"watchId"`
	ChangedPaths []string `json:"changedPaths"`
}

func (n *ChangedNotification) MarshalJSON() ([]byte, error) {
	changedPaths := append([]string(nil), n.ChangedPaths...)
	if changedPaths == nil {
		changedPaths = []string{}
	}
	return json.Marshal(struct {
		WatchID      string   `json:"watchId"`
		ChangedPaths []string `json:"changedPaths"`
	}{
		WatchID:      n.WatchID,
		ChangedPaths: changedPaths,
	})
}

type fsWatchKey struct {
	connectionID string
	watchID      string
}

type fsWatchEntry struct {
	path string
}

type FSService struct {
	mu      sync.Mutex
	watches map[fsWatchKey]fsWatchEntry
}

func NewFSService() *FSService {
	return &FSService{watches: map[fsWatchKey]fsWatchEntry{}}
}

func (s *FSService) ReadFile(params *ReadFileParams) (*ReadFileResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &ReadFileResponse{DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

func (s *FSService) WriteFile(params *WriteFileParams) (*WriteFileResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(params.DataBase64)
	if err != nil {
		return nil, invalidFSRequest(fmt.Sprintf("fs/writeFile requires valid base64 dataBase64: %v", err))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return &WriteFileResponse{}, nil
}

func (s *FSService) CreateDirectory(params *CreateDirectoryParams) (*CreateDirectoryResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	recursive := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}
	if recursive {
		err = os.MkdirAll(path, 0o755)
	} else {
		err = os.Mkdir(path, 0o755)
	}
	if err != nil {
		return nil, err
	}
	return &CreateDirectoryResponse{}, nil
}

func (s *FSService) GetMetadata(params *GetMetadataParams) (*GetMetadataResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		info = linkInfo
	}
	return &GetMetadataResponse{
		IsDirectory:  info.IsDir(),
		IsFile:       info.Mode().IsRegular(),
		IsSymlink:    linkInfo.Mode()&os.ModeSymlink != 0,
		CreatedAtMS:  createdAtMillis(info),
		ModifiedAtMS: unixMillis(info.ModTime()),
	}, nil
}

func (s *FSService) ReadDirectory(params *ReadDirectoryParams) (*ReadDirectoryResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]ReadDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, ReadDirectoryEntry{FileName: entry.Name(), IsDirectory: info.IsDir(), IsFile: info.Mode().IsRegular()})
	}
	sortEntries(out)
	return &ReadDirectoryResponse{Entries: out}, nil
}

func (s *FSService) Remove(params *RemoveParams) (*RemoveResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	recursive := true
	force := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}
	if params.Force != nil {
		force = *params.Force
	}
	if recursive {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil && !(force && errors.Is(err, os.ErrNotExist)) {
		return nil, err
	}
	return &RemoveResponse{}, nil
}

func (s *FSService) Copy(params *CopyParams) (*CopyResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	src, err := validateAbsolute(params.SourcePath)
	if err != nil {
		return nil, err
	}
	dst, err := validateAbsolute(params.DestinationPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if !params.Recursive {
			return nil, invalidFSRequest("fs/copy requires recursive: true when sourcePath is a directory")
		}
		descendant, err := destinationIsSameOrDescendantOfSource(src, dst)
		if err != nil {
			return nil, err
		}
		if descendant {
			return nil, invalidFSRequest("fs/copy cannot copy a directory to itself or one of its descendants")
		}
		return &CopyResponse{}, copyDir(src, dst)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &CopyResponse{}, copySymlink(src, dst)
	}
	if info.Mode().IsRegular() {
		return &CopyResponse{}, copyFile(src, dst, info.Mode())
	}
	return nil, invalidFSRequest("fs/copy only supports regular files, directories, and symlinks")
}

func (s *FSService) Watch(params *WatchParams) (*WatchResponse, error) {
	return s.WatchWithConnection(defaultRequestConnectionID, params)
}

func (s *FSService) WatchWithConnection(connectionID string, params *WatchParams) (*WatchResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	if strings.TrimSpace(params.WatchID) == "" {
		return nil, fmt.Errorf("%w: watchId is required", ErrInvalidFSRequest)
	}
	path, err := validateAbsolute(params.Path)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	key := fsWatchKey{connectionID: normalizeConnectionID(connectionID), watchID: params.WatchID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[key]; ok {
		return nil, fmt.Errorf("%w: watchId already exists: %s", ErrInvalidFSRequest, params.WatchID)
	}
	s.watches[key] = fsWatchEntry{path: canonical}
	return &WatchResponse{Path: canonical}, nil
}

func (s *FSService) Unwatch(params *UnwatchParams) (*UnwatchResponse, error) {
	return s.UnwatchWithConnection(defaultRequestConnectionID, params)
}

func (s *FSService) UnwatchWithConnection(connectionID string, params *UnwatchParams) (*UnwatchResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	if strings.TrimSpace(params.WatchID) == "" {
		return nil, fmt.Errorf("%w: watchId is required", ErrInvalidFSRequest)
	}
	key := fsWatchKey{connectionID: normalizeConnectionID(connectionID), watchID: params.WatchID}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.watches, key)
	return &UnwatchResponse{}, nil
}

func (s *FSService) Changed(watchID string, paths ...string) (*ChangedNotification, bool) {
	return s.ChangedForConnection(defaultRequestConnectionID, watchID, paths...)
}

func (s *FSService) ChangedForConnection(connectionID string, watchID string, paths ...string) (*ChangedNotification, bool) {
	key := fsWatchKey{connectionID: normalizeConnectionID(connectionID), watchID: watchID}
	s.mu.Lock()
	_, ok := s.watches[key]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	return &ChangedNotification{WatchID: watchID, ChangedPaths: append([]string(nil), paths...)}, true
}

func (s *FSService) ConnectionClosed(connectionID string) {
	if s == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.watches {
		if key.connectionID == connectionID {
			delete(s.watches, key)
		}
	}
}

func (s *FSService) WatchCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.watches)
}

func validateAbsolute(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: path is required", ErrInvalidFSRequest)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: path must be absolute", ErrInvalidFSRequest)
	}
	return filepath.Clean(path), nil
}

func copyFile(src string, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode.Perm())
}

func copyDir(src string, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		info, err := os.Lstat(from)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := copyDir(from, to); err != nil {
				return err
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			if err := copySymlink(from, to); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			if err := copyFile(from, to, info.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

func copySymlink(src string, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(target, dst)
}

func destinationIsSameOrDescendantOfSource(src string, dst string) (bool, error) {
	canonicalSource, err := canonicalizeExistingPath(src)
	if err != nil {
		return false, err
	}
	canonicalDestination, err := resolveExistingPath(dst)
	if err != nil {
		return false, err
	}
	if runtime.GOOS == "windows" {
		canonicalSource = strings.ToLower(canonicalSource)
		canonicalDestination = strings.ToLower(canonicalDestination)
	}
	relative, err := filepath.Rel(canonicalSource, canonicalDestination)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))), nil
}

func canonicalizeExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			return filepath.Abs(path)
		}
		return "", err
	}
	return filepath.Abs(resolved)
}

func resolveExistingPath(path string) (string, error) {
	path = filepath.Clean(path)
	var suffix []string
	current := path
	for {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
	resolved, err := canonicalizeExistingPath(current)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
}

func sortEntries(entries []ReadDirectoryEntry) {
	for i := 1; i < len(entries); i++ {
		current := entries[i]
		j := i - 1
		for j >= 0 && entries[j].FileName > current.FileName {
			entries[j+1] = entries[j]
			j--
		}
		entries[j+1] = current
	}
}

func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano() / int64(time.Millisecond)
}

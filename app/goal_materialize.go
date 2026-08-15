package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// appServerGoalFS abstracts the app-server fs calls the TUI uses to materialize
// goal files, so the same logic works against the in-process local router and
// the remote session client (mirrors Rust AppServerSession fs methods).
type appServerGoalFS struct {
	createDirectoryAll func(path string) error
	writeFile          func(path string, data []byte) error
	readFile           func(path string) ([]byte, error)
	remove             func(path string, recursive bool) error
}

// materializeOversizedGoalObjective materializes a goal objective longer than
// the protocol limit into a managed goal file and returns the file reference.
// Objectives within the limit are returned unchanged.
func materializeOversizedGoalObjective(fs appServerGoalFS, codexHome, objective string) (string, error) {
	return materializeGoalDraft(fs, codexHome, codextui.GoalDraft{Objective: objective})
}

// materializeGoalDraft writes pasted text, local images, remote image URLs, and
// oversized objectives into $CODEX_HOME/attachments/<uuid>/ app-server files
// and rewrites the objective with file references, mirroring Rust
// goal_files::materialize_goal_draft. On failure the output directory is
// removed.
func materializeGoalDraft(fs appServerGoalFS, codexHome string, draft codextui.GoalDraft) (string, error) {
	objective := draft.Objective
	if strings.TrimSpace(objective) == "" {
		return "", errors.New("Goal objective must not be empty.")
	}
	if len(draft.PendingPastes) > 0 {
		expanded, _ := expandPendingPastes(objective, draft.PendingPastes)
		if strings.TrimSpace(expanded) == "" {
			return "", errors.New("Goal objective must not be empty.")
		}
	}
	objective = strings.TrimSpace(objective)

	var outputDir string
	cleanup := func() {
		if outputDir != "" {
			_ = fs.remove(outputDir, true)
			outputDir = ""
		}
	}
	ensureOutputDir := func() error {
		if outputDir != "" {
			return nil
		}
		dir := filepath.Join(codexHome, codextui.GoalAttachmentDir, uuid.NewString())
		if err := fs.createDirectoryAll(dir); err != nil {
			return err
		}
		outputDir = dir
		return nil
	}
	writeFile := func(path string, data []byte) error {
		if err := fs.writeFile(path, data); err != nil {
			return err
		}
		return nil
	}

	pasteCount := 0
	for _, paste := range draft.PendingPastes {
		placeholder := paste[0]
		if placeholder == "" || !strings.Contains(objective, placeholder) {
			continue
		}
		if strings.TrimSpace(paste[1]) == "" {
			continue
		}
		pasteCount++
		if err := ensureOutputDir(); err != nil {
			cleanup()
			return "", err
		}
		path := filepath.Join(outputDir, fmt.Sprintf("pasted-text-%d.txt", pasteCount))
		if err := writeFile(path, []byte(paste[1])); err != nil {
			cleanup()
			return "", err
		}
		objective = strings.Replace(
			objective,
			placeholder,
			fmt.Sprintf("pasted text file: %s. Read this file before continuing.", path),
			1,
		)
	}

	var imageLines []string
	for index, image := range draft.LocalImages {
		if image.Placeholder != "" && !strings.Contains(objective, image.Placeholder) {
			continue
		}
		if err := ensureOutputDir(); err != nil {
			cleanup()
			return "", err
		}
		extension := goalImageExtension(image.Path)
		path := filepath.Join(outputDir, fmt.Sprintf("image-%d.%s", index+1, extension))
		data, err := os.ReadFile(image.Path)
		if err != nil {
			cleanup()
			return "", fmt.Errorf("Could not read goal image %s", image.Path)
		}
		if err := writeFile(path, data); err != nil {
			cleanup()
			return "", err
		}
		if image.Placeholder == "" {
			imageLines = append(imageLines, fmt.Sprintf("- [Image #%d]: %s", index+1, path))
		} else {
			objective = strings.Replace(
				objective,
				image.Placeholder,
				fmt.Sprintf("image file: %s", path),
				1,
			)
		}
	}
	appendGoalSection(&objective, "Referenced image files:", imageLines)

	var urlLines []string
	for index, url := range draft.RemoteImageURLs {
		urlLines = append(urlLines, fmt.Sprintf("- [Image #%d]: %s", index+1, url))
	}
	appendGoalSection(&objective, "Referenced image URLs:", urlLines)

	if len([]rune(objective)) > codextui.MaxGoalObjectiveRune {
		if err := ensureOutputDir(); err != nil {
			cleanup()
			return "", err
		}
		path := filepath.Join(outputDir, codextui.GoalFileName)
		reference, err := codextui.GoalObjectiveFileReference(path)
		if err != nil {
			cleanup()
			return "", err
		}
		if err := writeFile(path, []byte(objective)); err != nil {
			cleanup()
			return "", err
		}
		objective = reference
	}
	return objective, nil
}

func expandPendingPastes(objective string, pastes [][2]string) (string, []string) {
	expanded := objective
	used := make([]string, 0, len(pastes))
	for _, paste := range pastes {
		placeholder := paste[0]
		if placeholder == "" || !strings.Contains(expanded, placeholder) {
			continue
		}
		expanded = strings.Replace(expanded, placeholder, paste[1], 1)
		used = append(used, placeholder)
	}
	return expanded, used
}

func appendGoalSection(objective *string, heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	if !strings.HasSuffix(*objective, "\n") {
		*objective += "\n\n"
	}
	*objective += heading + "\n" + strings.Join(lines, "\n")
}

func goalImageExtension(path string) string {
	extension := strings.TrimPrefix(filepath.Ext(path), ".")
	cleaned := make([]rune, 0, len(extension))
	for _, char := range extension {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			cleaned = append(cleaned, char)
		}
		if len(cleaned) == 8 {
			break
		}
	}
	if len(cleaned) == 0 {
		return "png"
	}
	return string(cleaned)
}

// resolveGoalObjectiveText loads the materialized goal objective file when the
// stored objective is a managed goal file reference, mirroring Rust
// goal_files::objective_text_for_edit. Non-reference objectives are unchanged.
func resolveGoalObjectiveText(fs appServerGoalFS, codexHome, objective string) (string, error) {
	path, ok := codextui.GoalObjectiveFilePath(objective, codexHome)
	if !ok {
		return objective, nil
	}
	data, err := fs.readFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// localGoalFS adapts the in-process interactive router to appServerGoalFS.
func localGoalFS(router interactiveGoalRouter) appServerGoalFS {
	nextID := int64(100)
	request := func(method appserver.Method, params any) (any, error) {
		nextID++
		return localGoalRequest(router, appserver.IntID(nextID), method, params)
	}
	return appServerGoalFS{
		createDirectoryAll: func(path string) error {
			recursive := true
			_, err := request(appserver.MethodFSCreateDirectory, appserver.CreateDirectoryParams{Path: path, Recursive: &recursive})
			return err
		},
		writeFile: func(path string, data []byte) error {
			_, err := request(appserver.MethodFSWriteFile, appserver.WriteFileParams{
				Path:       path,
				DataBase64: base64.StdEncoding.EncodeToString(data),
			})
			return err
		},
		readFile: func(path string) ([]byte, error) {
			value, err := request(appserver.MethodFSReadFile, appserver.ReadFileParams{Path: path})
			if err != nil {
				return nil, err
			}
			response, ok := value.(*appserver.ReadFileResponse)
			if !ok || response == nil {
				return nil, errors.New("invalid fs/readFile response")
			}
			return base64.StdEncoding.DecodeString(response.DataBase64)
		},
		remove: func(path string, recursive bool) error {
			_, err := request(appserver.MethodFSRemove, appserver.RemoveParams{Path: path, Recursive: &recursive})
			return err
		},
	}
}

// remoteGoalFS adapts an initialized remote session client to appServerGoalFS.
func remoteGoalFS(ctx context.Context, client *remoteAppServerTUIClient) appServerGoalFS {
	request := func(method appserver.Method, params any, target any) error {
		return remoteSessionRequest(ctx, client, method, params, target)
	}
	return appServerGoalFS{
		createDirectoryAll: func(path string) error {
			recursive := true
			return request(appserver.MethodFSCreateDirectory, appserver.CreateDirectoryParams{Path: path, Recursive: &recursive}, &appserver.CreateDirectoryResponse{})
		},
		writeFile: func(path string, data []byte) error {
			return request(appserver.MethodFSWriteFile, appserver.WriteFileParams{
				Path:       path,
				DataBase64: base64.StdEncoding.EncodeToString(data),
			}, &appserver.WriteFileResponse{})
		},
		readFile: func(path string) ([]byte, error) {
			var response appserver.ReadFileResponse
			if err := request(appserver.MethodFSReadFile, appserver.ReadFileParams{Path: path}, &response); err != nil {
				return nil, err
			}
			return base64.StdEncoding.DecodeString(response.DataBase64)
		},
		remove: func(path string, recursive bool) error {
			return request(appserver.MethodFSRemove, appserver.RemoveParams{Path: path, Recursive: &recursive}, &appserver.RemoveResponse{})
		},
	}
}

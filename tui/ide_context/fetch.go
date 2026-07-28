package idecontext

import (
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FetchIDEContext requests the current editor context from the local Codex IDE
// extension IPC endpoint.
func FetchIDEContext(workspaceRoot string, codexHome string) (*IdeContext, error) {
	deadline := time.Now().Add(IDEContextRequestTimeout)
	stream, err := connectIDEContext(strings.TrimSpace(workspaceRoot), strings.TrimSpace(codexHome), deadline)
	if err != nil {
		var contextErr *IdeContextError
		if errors.As(err, &contextErr) {
			return nil, contextErr
		}
		return nil, &IdeContextError{Kind: IdeContextErrorConnect, Err: err}
	}
	defer stream.Close()

	return fetchIDEContextFromStream(stream, strings.TrimSpace(workspaceRoot), deadline)
}

func fetchIDEContextFromStream(stream io.ReadWriter, workspaceRoot string, deadline time.Time) (*IdeContext, error) {
	requestID := uuid.NewString()
	if err := WriteIDEContextRequest(stream, requestID, workspaceRoot); err != nil {
		return nil, &IdeContextError{Kind: IdeContextErrorSend, Err: err}
	}
	response, err := ReadResponseFrame(stream, requestID, deadline)
	if err != nil {
		return nil, err
	}
	return ExtractIDEContext(response)
}

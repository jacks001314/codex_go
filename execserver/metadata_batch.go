package execserver

import (
	"encoding/json"
	"errors"
)

// Rust parity: codex-rs/exec-server-protocol/src/protocol.rs FsGetMetadataBatch
// and codex-rs/file-system/src/metadata_batch.rs (#63266).

const (
	// MaxMetadataBatchProbes bounds one metadata batch by path count.
	MaxMetadataBatchProbes = 1024
	// MaxMetadataBatchBytes bounds one metadata batch by encoded path bytes.
	MaxMetadataBatchBytes = 1024 * 1024
)

// FSGetMetadataBatchParams is Rust FsGetMetadataBatchParams: ordered per-path
// metadata results in one request.
type FSGetMetadataBatchParams struct {
	Paths          []string                  `json:"paths"`
	FollowSymlinks *bool                     `json:"followSymlinks,omitempty"`
	Sandbox        *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

// FSBatchError is the JSON-RPC error object carried for one failed path in a
// metadata batch (Rust JSONRPCErrorError).
type FSBatchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// FSMetadataBatchResult is one Rust `Result<FileMetadata, JSONRPCErrorError>`.
// Serde's default representation is externally tagged, so the wire form is
// `{"Ok": {...}}` or `{"Err": {...}}`.
type FSMetadataBatchResult struct {
	Metadata *FSGetMetadataResponse
	Error    *FSBatchError
}

// MarshalJSON keeps Rust's externally tagged Result shape.
func (r FSMetadataBatchResult) MarshalJSON() ([]byte, error) {
	if r.Error != nil {
		return json.Marshal(map[string]FSBatchError{"Err": *r.Error})
	}
	if r.Metadata == nil {
		return json.Marshal(map[string]any{"Ok": nil})
	}
	return json.Marshal(map[string]FSGetMetadataResponse{"Ok": *r.Metadata})
}

// UnmarshalJSON accepts the externally tagged Ok/Err arms.
func (r *FSMetadataBatchResult) UnmarshalJSON(data []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if raw, ok := envelope["Ok"]; ok {
		var metadata FSGetMetadataResponse
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return err
		}
		r.Metadata = &metadata
		r.Error = nil
		return nil
	}
	if raw, ok := envelope["Err"]; ok {
		var batchError FSBatchError
		if err := json.Unmarshal(raw, &batchError); err != nil {
			return err
		}
		r.Error = &batchError
		r.Metadata = nil
		return nil
	}
	return errors.New("metadata batch result must carry Ok or Err")
}

// FSGetMetadataBatchResponse keeps one result per requested path, in order.
type FSGetMetadataBatchResponse struct {
	Results []FSMetadataBatchResult `json:"results"`
}

// metadataBatchFits is Rust metadata_batch_fits: bound the path count and the
// encoded request bytes.
func metadataBatchFits(paths []string) bool {
	if len(paths) > MaxMetadataBatchProbes {
		return false
	}
	total := 0
	for _, path := range paths {
		total += len(path)
		if total > MaxMetadataBatchBytes {
			return false
		}
	}
	return true
}

// getMetadataBatch serves one batch without leaving the connection: the
// sandboxed helper handles the whole batch in a single invocation, and the
// unsandboxed path resolves each entry in order (Rust
// FileSystemHandler::get_metadata_batch).
func getMetadataBatch(params *FSGetMetadataBatchParams) (*FSGetMetadataBatchResponse, error) {
	if params == nil {
		return nil, errors.New("fs/getMetadataBatch params are required")
	}
	if !metadataBatchFits(params.Paths) {
		return nil, requestError(-32600, "invalid request: metadata batch exceeds size limit")
	}
	var sandboxed FSGetMetadataBatchResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSGetMetadataBatch, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	followSymlinks := true
	if params.FollowSymlinks != nil {
		followSymlinks = *params.FollowSymlinks
	}
	results := make([]FSMetadataBatchResult, 0, len(params.Paths))
	for _, path := range params.Paths {
		// Each entry reports its own failure instead of failing the batch.
		metadata, err := getMetadataFollow(&FSGetMetadataParams{Path: path}, followSymlinks)
		if err != nil {
			failure := fsOperationFailure(err)
			results = append(results, FSMetadataBatchResult{
				Error: &FSBatchError{Code: failure.code, Message: failure.message},
			})
			continue
		}
		results = append(results, FSMetadataBatchResult{Metadata: metadata})
	}
	return &FSGetMetadataBatchResponse{Results: results}, nil
}

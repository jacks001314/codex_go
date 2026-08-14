package appserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/mcp"
	"codex_go/turn"
)

type environmentOpenAIFileSystem struct {
	record          EnvironmentRecord
	requiresSandbox bool
}

func (f *environmentOpenAIFileSystem) Metadata(ctx context.Context, pathURI string) (*mcp.OpenAIFileMetadata, error) {
	client, err := f.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if f.requiresSandbox {
		if err := f.requireSandboxedFileStreaming(ctx, client); err != nil {
			return nil, err
		}
	}
	metadata, err := client.FSGetMetadata(ctx, &execserverclient.FSGetMetadataParams{Path: pathURI})
	if err != nil {
		return nil, err
	}
	return &mcp.OpenAIFileMetadata{IsFile: metadata.IsFile, Size: metadata.Size}, nil
}

func (f *environmentOpenAIFileSystem) Open(ctx context.Context, pathURI string) (io.ReadCloser, error) {
	client, err := f.dial(ctx)
	if err != nil {
		return nil, err
	}
	if f.requiresSandbox {
		if err := f.requireSandboxedFileStreaming(ctx, client); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	stream, err := client.FSReadFileStream(ctx, &execserverclient.FSReadFileParams{Path: pathURI})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &environmentOpenAIFileReader{ctx: ctx, client: client, stream: stream}, nil
}

func (f *environmentOpenAIFileSystem) requireSandboxedFileStreaming(ctx context.Context, client sandboxedFileStreamingInfo) error {
	if f == nil || !f.requiresSandbox {
		return nil
	}
	info, err := client.EnvironmentInfo(ctx)
	if err != nil {
		return err
	}
	if !info.Capabilities.SandboxedFileStreaming {
		return errors.New("selected executor does not support sandboxed file streaming")
	}
	return nil
}

func (f *environmentOpenAIFileSystem) dial(ctx context.Context) (*execserverclient.Client, error) {
	if f == nil {
		return nil, errors.New("primary turn environment is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	connectCtx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(f.record.ConnectTimeoutMS))
	defer cancel()
	options := execserverclient.DialClientOptions{ClientName: "codex-go-openai-file", HTTPClient: f.record.HTTPClient}
	if f.record.NoiseProvider != nil {
		return execserverclient.DialNoiseRendezvousClient(connectCtx, f.record.NoiseProvider, options)
	}
	if strings.TrimSpace(f.record.ExecServerURL) == "" {
		return nil, errors.New("primary turn environment has no exec-server connection")
	}
	return execserverclient.DialClientWithOptions(connectCtx, f.record.ExecServerURL, options)
}

type environmentOpenAIFileReader struct {
	ctx    context.Context
	client *execserverclient.Client
	stream *execserverclient.FileReadStream

	mu     sync.Mutex
	buffer []byte
	eof    bool
	closed bool
}

type sandboxedFileStreamingInfo interface {
	EnvironmentInfo(context.Context) (*execserverclient.EnvironmentInfo, error)
}

func (r *environmentOpenAIFileReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	for len(r.buffer) == 0 && !r.eof {
		chunk, eof, err := r.stream.Next(r.ctx)
		if err != nil {
			return 0, err
		}
		r.buffer = chunk
		r.eof = eof
	}
	if len(r.buffer) == 0 && r.eof {
		return 0, io.EOF
	}
	count := copy(destination, r.buffer)
	r.buffer = r.buffer[count:]
	return count, nil
}

func (r *environmentOpenAIFileReader) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var streamErr error
	if r.stream != nil {
		streamErr = r.stream.Close(r.ctx)
	}
	var clientErr error
	if r.client != nil {
		clientErr = r.client.Close()
	}
	return errors.Join(streamErr, clientErr)
}

func (r *RuntimeRouter) primaryTurnOpenAIFileSystem(params *turn.TurnStartParams) mcp.OpenAIFileSystem {
	if r == nil || params == nil || len(params.Environments) == 0 || r.services.Environment == nil {
		return nil
	}
	environmentID := strings.TrimSpace(firstNonEmpty(
		threadItemStringFromAnyMap(params.Environments[0], "environmentId"),
		threadItemStringFromAnyMap(params.Environments[0], "environment_id"),
	))
	record, ok := r.services.Environment.Record(environmentID)
	if !ok || record == nil || (strings.TrimSpace(record.ExecServerURL) == "" && record.NoiseProvider == nil) {
		return nil
	}
	return &environmentOpenAIFileSystem{record: *record}
}

// openAIFileReadPolicy returns a read-policy hook for the local fallback
// filesystem. Remote environments already enforce the filesystem policy in
// the executor, so the hook is only used when no executor-backed file system
// is available.
func openAIFileReadPolicy(fileSystem mcp.OpenAIFileSystem, resolution *config.SandboxPermissionProfileResolution) func(string) error {
	if fileSystem != nil || resolution == nil || resolution.Profile == nil || !resolution.Profile.HasDenyReadEntries() {
		return nil
	}
	profile := resolution.Profile
	return func(path string) error {
		if profile.DeniesReadPath(path) {
			return fmt.Errorf("filesystem policy denies reading this path")
		}
		return nil
	}
}

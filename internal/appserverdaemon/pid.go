package appserverdaemon

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	StderrLogTailBytes          int64 = 4096
	RemoteControlDisabledEnvVar       = "CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED"
	PIDStartPollInterval              = 50 * time.Millisecond
	PIDStartTimeout                   = 10 * time.Second
	PIDStopPollInterval               = 50 * time.Millisecond
	PIDStopGracePeriod                = 60 * time.Second
	PIDStopTimeout                    = 70 * time.Second
)

type BackendPaths struct {
	CodexBin             string
	PIDFile              string
	UpdatePIDFile        string
	RemoteControlEnabled bool
}

type PIDCommandKind string

const (
	PIDCommandAppServer  PIDCommandKind = "appServer"
	PIDCommandUpdateLoop PIDCommandKind = "updateLoop"
)

type PIDBackend struct {
	CodexBin             string
	PIDFile              string
	LockFile             string
	CommandKind          PIDCommandKind
	RemoteControlEnabled bool
}

type PIDRecord struct {
	PID              uint32 `json:"pid"`
	ProcessStartTime string `json:"processStartTime"`
}

type PIDFileStateKind string

const (
	PIDFileMissing  PIDFileStateKind = "missing"
	PIDFileStarting PIDFileStateKind = "starting"
	PIDFileRunning  PIDFileStateKind = "running"
)

type PIDFileState struct {
	Kind   PIDFileStateKind
	Record *PIDRecord
}

type PIDLogTail struct {
	Path     string
	Contents string
}

func NewPIDBackend(paths BackendPaths) *PIDBackend {
	return &PIDBackend{
		CodexBin:             paths.CodexBin,
		PIDFile:              paths.PIDFile,
		LockFile:             pidPathWithExtension(paths.PIDFile, "pid.lock"),
		CommandKind:          PIDCommandAppServer,
		RemoteControlEnabled: paths.RemoteControlEnabled,
	}
}

func NewPIDUpdateLoopBackend(paths BackendPaths) *PIDBackend {
	return &PIDBackend{
		CodexBin:    paths.CodexBin,
		PIDFile:     paths.UpdatePIDFile,
		LockFile:    pidPathWithExtension(paths.UpdatePIDFile, "pid.lock"),
		CommandKind: PIDCommandUpdateLoop,
	}
}

func (b *PIDBackend) CommandArgs() []string {
	if b == nil {
		return nil
	}
	switch b.CommandKind {
	case PIDCommandUpdateLoop:
		return []string{"app-server", "daemon", "pid-update-loop"}
	default:
		if b.RemoteControlEnabled {
			return []string{"app-server", "--remote-control", "--listen", "unix://"}
		}
		return []string{"app-server", "--listen", "unix://"}
	}
}

func (b *PIDBackend) CommandEnv() map[string]string {
	if b == nil || b.CommandKind != PIDCommandAppServer || b.RemoteControlEnabled {
		return nil
	}
	return map[string]string{RemoteControlDisabledEnvVar: "1"}
}

func (b *PIDBackend) IsStartingOrRunning() (bool, error) {
	if b == nil {
		return false, nil
	}
	for {
		state, err := ReadPIDFileState(b.PIDFile)
		if err != nil {
			return false, err
		}
		switch state.Kind {
		case PIDFileMissing:
			active, err := fileLockIsActive(b.LockFile)
			if err != nil {
				return false, err
			}
			if active {
				return true, nil
			}
			return false, nil
		case PIDFileStarting:
			active, err := fileLockIsActive(b.LockFile)
			if err != nil {
				return false, err
			}
			if !active {
				if err := os.Remove(b.PIDFile); err != nil && !os.IsNotExist(err) {
					return false, err
				}
				return false, nil
			}
			return true, nil
		case PIDFileRunning:
			if state.Record == nil {
				return false, nil
			}
			active, err := processMatchesPIDRecord(state.Record)
			if err != nil {
				return false, err
			}
			if active {
				return true, nil
			}
			if err := b.removeStalePIDRecord(state.Record); err != nil {
				return false, err
			}
		default:
			return false, fmt.Errorf("unknown pid file state %s", state.Kind)
		}
	}
}

func (b *PIDBackend) Start() (*uint32, error) {
	if b == nil {
		return nil, fmt.Errorf("pid backend is nil")
	}
	if b.PIDFile == "" {
		return nil, fmt.Errorf("%w: pid file is empty", ErrDaemonPathsRequired)
	}
	if err := os.MkdirAll(filepath.Dir(b.PIDFile), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create pid directory %s: %w", filepath.Dir(b.PIDFile), err)
	}
	reservationLock, err := acquireExclusiveFileLock(b.LockFile, PIDStartTimeout, PIDStartPollInterval, "pid lock")
	if err != nil {
		return nil, err
	}
	defer reservationLock.Close()
	for {
		file, err := os.OpenFile(b.PIDFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			break
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("failed to reserve pid file %s: %w", b.PIDFile, err)
		}
		state, err := ReadPIDFileState(b.PIDFile)
		if err != nil {
			return nil, err
		}
		if state.Kind == PIDFileRunning && state.Record != nil {
			active, err := processMatchesPIDRecord(state.Record)
			if err != nil {
				return nil, err
			}
			if active {
				return nil, nil
			}
			if err := b.removeStalePIDRecord(state.Record); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.Remove(b.PIDFile); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if state.Kind == PIDFileStarting {
			continue
		}
		if state.Kind != PIDFileMissing {
			return nil, nil
		}
	}
	pid, processStartTime, err := startDetachedPIDProcess(b)
	if err != nil {
		_ = os.Remove(b.PIDFile)
		return nil, err
	}
	record := &PIDRecord{PID: pid, ProcessStartTime: processStartTime}
	if err := WritePIDRecord(b.PIDFile, record); err != nil {
		_ = terminatePIDProcess(pid)
		_ = os.Remove(b.PIDFile)
		return nil, err
	}
	return &record.PID, nil
}

func (b *PIDBackend) Stop() error {
	if b == nil {
		return nil
	}
	for {
		record, err := b.waitForPIDStart()
		if err != nil {
			return err
		}
		if record == nil {
			return nil
		}
		active, err := processMatchesPIDRecord(record)
		if err != nil {
			return err
		}
		if !active {
			if err := b.removeStalePIDRecord(record); err != nil {
				return err
			}
			continue
		}
		pid := record.PID
		if err := terminatePIDProcess(pid); err != nil {
			return err
		}
		started := time.Now()
		deadline := time.Now().Add(PIDStopTimeout)
		forced := false
		for time.Now().Before(deadline) {
			active, err := processMatchesPIDRecord(record)
			if err != nil {
				return err
			}
			if !active {
				return b.removeStalePIDRecord(record)
			}
			if !forced && time.Since(started) >= PIDStopGracePeriod {
				if err := forceTerminatePIDProcess(pid, b.CommandKind == PIDCommandUpdateLoop); err != nil {
					return err
				}
				forced = true
			}
			time.Sleep(PIDStopPollInterval)
		}
		active, err = processMatchesPIDRecord(record)
		if err != nil {
			return err
		}
		if active {
			return fmt.Errorf("timed out waiting for pid-managed app server %d to stop", pid)
		}
		return b.removeStalePIDRecord(record)
	}
}

func (b *PIDBackend) StderrLogPath() string {
	if b == nil || b.PIDFile == "" {
		return ""
	}
	return StderrLogPathForPIDFile(b.PIDFile)
}

func (t *PIDLogTail) AppendToContext(context *string) {
	if t == nil || context == nil {
		return
	}
	*context += fmt.Sprintf("\n\nManaged app-server stderr (%s):", t.Path)
	for _, line := range strings.Split(strings.ReplaceAll(t.Contents, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		*context += "\n  " + line
	}
}

func ReadPIDRecord(path string) (*PIDRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record PIDRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func ReadPIDFileState(path string) (*PIDFileState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &PIDFileState{Kind: PIDFileMissing}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return &PIDFileState{Kind: PIDFileStarting}, nil
	}
	var record PIDRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &PIDFileState{Kind: PIDFileRunning, Record: &record}, nil
}

func (b *PIDBackend) waitForPIDStart() (*PIDRecord, error) {
	deadline := time.Now().Add(PIDStartTimeout)
	for {
		state, err := ReadPIDFileState(b.PIDFile)
		if err != nil {
			return nil, err
		}
		switch state.Kind {
		case PIDFileMissing:
			return nil, nil
		case PIDFileRunning:
			return state.Record, nil
		case PIDFileStarting:
			active, err := fileLockIsActive(b.LockFile)
			if err != nil {
				return nil, err
			}
			if !active {
				if err := os.Remove(b.PIDFile); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				return nil, nil
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timed out waiting for pid reservation in %s to finish initializing", b.PIDFile)
			}
			time.Sleep(PIDStartPollInterval)
		default:
			return nil, fmt.Errorf("unknown pid file state %s", state.Kind)
		}
	}
}

func (b *PIDBackend) removeStalePIDRecord(expected *PIDRecord) error {
	if b == nil || expected == nil {
		return nil
	}
	current, err := ReadPIDFileState(b.PIDFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Kind != PIDFileRunning || current.Record == nil {
		return nil
	}
	if current.Record.PID != expected.PID || current.Record.ProcessStartTime != expected.ProcessStartTime {
		return nil
	}
	if err := os.Remove(b.PIDFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func WritePIDRecord(path string, record *PIDRecord) error {
	if record == nil {
		return fmt.Errorf("pid record is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ReadStderrLogTail(pidFile string) (*PIDLogTail, error) {
	path := StderrLogPathForPIDFile(pidFile)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}
	offset := int64(0)
	if info.Size() > StderrLogTailBytes {
		offset = info.Size() - StderrLogTailBytes
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if newline := strings.IndexByte(string(data), '\n'); newline >= 0 {
			data = data[newline+1:]
		}
	}
	contents := strings.TrimRight(string(data), "\r\n")
	if contents == "" {
		return nil, nil
	}
	return &PIDLogTail{Path: path, Contents: contents}, nil
}

func StderrLogPathForPIDFile(pidFile string) string {
	return pidPathWithExtension(pidFile, "stderr.log")
}

func pidPathWithExtension(path string, extension string) string {
	if path == "" {
		return ""
	}
	ext := filepath.Ext(path)
	if ext == "" {
		return path + "." + extension
	}
	return strings.TrimSuffix(path, ext) + "." + extension
}

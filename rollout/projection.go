package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type ProjectionStepKind string

const (
	ProjectionLine                ProjectionStepKind = "line"
	ProjectionSkippedOrdinalRange ProjectionStepKind = "skipped_ordinal_range"
)

// ProjectionStep is one ordered update to apply while advancing a durable
// rollout projection checkpoint.
type ProjectionStep struct {
	Kind                ProjectionStepKind
	Line                *Line
	Ordinal             uint64
	StartByteOffset     uint64
	EndByteOffset       uint64
	EndOrdinalExclusive uint64
}

// ReadProjectionSteps reads only the newline-terminated suffix beginning at
// startOffset. Rejected lines remain pending until a later valid ordinal proves
// whether they consumed history.
func ReadProjectionSteps(path string, startOffset uint64, expectedOrdinal uint64) ([]ProjectionStep, uint64, error) {
	return ReadProjectionStepsValidated(path, startOffset, expectedOrdinal, nil)
}

// ReadProjectionStepsValidated applies validate after a line is structurally
// decoded. A validation failure has the same deferred semantics as an unknown
// rollout record.
func ReadProjectionStepsValidated(path string, startOffset uint64, expectedOrdinal uint64, validate func(*Line) error) ([]ProjectionStep, uint64, error) {
	data, _, err := readProjectionSuffix(path, startOffset)
	if err != nil {
		return nil, startOffset, err
	}
	return readProjectionStepsData(data, startOffset, expectedOrdinal, validate)
}

func readProjectionStepsData(data []byte, startOffset uint64, expectedOrdinal uint64, validate func(*Line) error) ([]ProjectionStep, uint64, error) {
	completeByteCount := bytes.LastIndexByte(data, '\n') + 1
	if completeByteCount == 0 {
		return nil, startOffset, nil
	}

	steps := make([]ProjectionStep, 0)
	nextOrdinal := expectedOrdinal
	nextOffset := startOffset
	pendingRejected := uint64(0)
	lineStart := startOffset
	for _, physicalLine := range bytes.SplitAfter(data[:completeByteCount], []byte{'\n'}) {
		if len(physicalLine) == 0 {
			continue
		}
		lineEnd := lineStart + uint64(len(physicalLine))
		if len(bytes.TrimSpace(physicalLine)) == 0 {
			if pendingRejected == 0 {
				nextOffset = lineEnd
			}
			lineStart = lineEnd
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(physicalLine, &raw); err != nil || raw == nil {
			pendingRejected++
			lineStart = lineEnd
			continue
		}
		ordinal, hasOrdinal := projectionOrdinal(raw["ordinal"])
		line, recognized := projectionLine(physicalLine, raw)
		if !recognized {
			pendingRejected++
			lineStart = lineEnd
			continue
		}
		if !hasOrdinal {
			return nil, startOffset, fmt.Errorf("paginated rollout line is missing an ordinal")
		}
		if validate != nil && validate(line) != nil {
			pendingRejected++
			lineStart = lineEnd
			continue
		}
		if ordinal < nextOrdinal {
			return nil, startOffset, fmt.Errorf("rollout projection expected ordinal %d, got %d", nextOrdinal, ordinal)
		}
		skipped := ordinal - nextOrdinal
		if skipped > pendingRejected {
			return nil, startOffset, fmt.Errorf("rollout projection expected ordinal %d, got %d; %d rejected rollout lines cannot cover that gap", nextOrdinal, ordinal, pendingRejected)
		}
		if skipped > 0 {
			steps = append(steps, ProjectionStep{
				Kind:                ProjectionSkippedOrdinalRange,
				Ordinal:             nextOrdinal,
				EndOrdinalExclusive: ordinal,
			})
		}
		if ordinal == ^uint64(0) {
			return nil, startOffset, errors.New("rollout ordinal exceeds integer range")
		}
		pendingRejected = 0
		line.Ordinal = &ordinal
		steps = append(steps, ProjectionStep{
			Kind:            ProjectionLine,
			Line:            line,
			Ordinal:         ordinal,
			StartByteOffset: lineStart,
			EndByteOffset:   lineEnd,
		})
		nextOrdinal = ordinal + 1
		nextOffset = lineEnd
		lineStart = lineEnd
	}
	return steps, nextOffset, nil
}

func readProjectionSuffix(path string, startOffset uint64) ([]byte, uint64, error) {
	existing, found := ExistingRolloutPath(path)
	if !found {
		if startOffset == 0 {
			return nil, 0, nil
		}
		return nil, startOffset, os.ErrNotExist
	}
	file, err := os.Open(existing)
	if err != nil {
		return nil, startOffset, err
	}
	defer file.Close()
	if strings.HasSuffix(strings.ToLower(existing), ".zst") {
		decoder, decodeErr := zstd.NewReader(file)
		if decodeErr != nil {
			return nil, startOffset, decodeErr
		}
		decompressed, readErr := io.ReadAll(decoder)
		decoder.Close()
		if readErr != nil {
			return nil, startOffset, readErr
		}
		fileEnd := uint64(len(decompressed))
		if fileEnd < startOffset {
			return nil, startOffset, errors.New("durable rollout shrank before projection")
		}
		return decompressed[startOffset:], fileEnd, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, startOffset, err
	}
	fileEnd := uint64(info.Size())
	if fileEnd < startOffset {
		return nil, startOffset, errors.New("durable rollout shrank before projection")
	}
	if _, err := file.Seek(int64(startOffset), io.SeekStart); err != nil {
		return nil, startOffset, err
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(fileEnd-startOffset)))
	return data, fileEnd, err
}

func projectionOrdinal(raw json.RawMessage) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var ordinal uint64
	if err := json.Unmarshal(raw, &ordinal); err != nil {
		return 0, false
	}
	return ordinal, true
}

func projectionLine(data []byte, raw map[string]json.RawMessage) (*Line, bool) {
	var kind string
	if err := json.Unmarshal(raw["type"], &kind); err != nil || !knownProjectionLineType(kind) {
		return nil, false
	}
	var line Line
	if err := unmarshalLine(data, &line); err != nil {
		return nil, false
	}
	return &line, true
}

func knownProjectionLineType(kind string) bool {
	switch kind {
	case "session_meta", "response_item", "item", "event_msg", "inter_agent_communication",
		"inter_agent_communication_metadata", "compacted", "turn_context", "world_state", "security_risk_score":
		return true
	default:
		return false
	}
}

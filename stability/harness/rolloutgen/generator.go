// Package rolloutgen writes deterministic rollout JSONL fixtures of a
// requested size. It exists so large-data scenarios can build a realistic
// session history without depending on the agent loop or the network.
package rolloutgen

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultMaxLineSize  = 64 << 10
	defaultItemsPerTurn = 4
	minLineSize         = 512
)

// Spec describes one fixture.
type Spec struct {
	// Path is the rollout JSONL file to create.
	Path string
	// TotalBytes is the minimum size to reach. Generation stops after the
	// first complete line that crosses it, so the file can overshoot by one
	// line.
	TotalBytes int64
	// MaxLineSize caps a single JSONL record. Defaults to 64 KiB.
	MaxLineSize int
	// ItemsPerTurn is how many item records follow each turn_context.
	// Defaults to 4.
	ItemsPerTurn int
	// Seed makes line sizes reproducible.
	Seed int64
}

// Stats reports what was written.
type Stats struct {
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	Lines       int    `json:"lines"`
	Turns       int    `json:"turns"`
	LargestLine int    `json:"largest_line"`
}

// Generate writes the fixture described by spec.
func Generate(spec Spec) (Stats, error) {
	if strings.TrimSpace(spec.Path) == "" {
		return Stats{}, fmt.Errorf("rolloutgen: path is required")
	}
	if spec.TotalBytes <= 0 {
		return Stats{}, fmt.Errorf("rolloutgen: total bytes must be positive")
	}
	maxLine := spec.MaxLineSize
	if maxLine == 0 {
		maxLine = defaultMaxLineSize
	}
	if maxLine < minLineSize {
		return Stats{}, fmt.Errorf("rolloutgen: max line size %d is below the %d byte minimum", maxLine, minLineSize)
	}
	itemsPerTurn := spec.ItemsPerTurn
	if itemsPerTurn == 0 {
		itemsPerTurn = defaultItemsPerTurn
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o755); err != nil {
		return Stats{}, err
	}
	file, err := os.Create(spec.Path)
	if err != nil {
		return Stats{}, err
	}
	writer := bufio.NewWriterSize(file, 256<<10)
	stats := Stats{Path: spec.Path}
	rng := rand.New(rand.NewSource(spec.Seed))

	write := func(line []byte) error {
		if len(line)+1 > maxLine {
			return fmt.Errorf("rolloutgen: line of %d bytes exceeds the %d byte cap", len(line)+1, maxLine)
		}
		if _, err := writer.Write(line); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		stats.Bytes += int64(len(line) + 1)
		stats.Lines++
		if len(line) > stats.LargestLine {
			stats.LargestLine = len(line)
		}
		return nil
	}

	header, err := sessionMeta()
	if err != nil {
		_ = file.Close()
		return Stats{}, err
	}
	if err := write(header); err != nil {
		_ = file.Close()
		return Stats{}, err
	}
	for stats.Bytes < spec.TotalBytes {
		turn := stats.Turns
		context, err := turnContext(turn)
		if err != nil {
			_ = file.Close()
			return Stats{}, err
		}
		if err := write(context); err != nil {
			_ = file.Close()
			return Stats{}, err
		}
		for index := 0; index < itemsPerTurn && stats.Bytes < spec.TotalBytes; index++ {
			target := minLineSize + rng.Intn(maxLine-minLineSize-64)
			line, err := itemLine(turn, index, target)
			if err != nil {
				_ = file.Close()
				return Stats{}, err
			}
			if err := write(line); err != nil {
				_ = file.Close()
				return Stats{}, err
			}
		}
		stats.Turns++
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return Stats{}, err
	}
	if err := file.Close(); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func sessionMeta() ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":      "session_meta",
		"timestamp": "2026-10-29T00:00:00Z",
		"payload": map[string]any{
			"id":             "thread-stability",
			"timestamp":      "2026-10-29T00:00:00Z",
			"cwd":            "/tmp/repo",
			"originator":     "codex_cli_rs",
			"cli_version":    "0.0.0",
			"source":         "cli",
			"model_provider": "openai",
			"history_mode":   "legacy",
		},
	})
}

func turnContext(turn int) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "turn_context",
		"payload": map[string]any{
			"cwd":   "/tmp/repo",
			"model": "gpt-5",
			"turn":  turn,
		},
	})
}

// itemLine builds a user message whose marshalled form is close to target
// bytes. ASCII padding keeps the byte length linear in the text length, so the
// adjustment converges in one or two passes.
func itemLine(turn, index, target int) ([]byte, error) {
	text := strings.Repeat("a", 64)
	var line []byte
	for attempt := 0; attempt < 6; attempt++ {
		var err error
		line, err = marshalItem(turn, index, text)
		if err != nil {
			return nil, err
		}
		delta := target - len(line)
		if delta == 0 {
			return line, nil
		}
		next := len(text) + delta
		if next < 1 {
			next = 1
		}
		text = strings.Repeat("a", next)
	}
	return line, nil
}

func marshalItem(turn, index int, text string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":    "item",
		"turn_id": fmt.Sprintf("turn-%06d", turn),
		"item": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": text},
			},
		},
		"item_id": fmt.Sprintf("item-%06d-%02d", turn, index),
	})
}

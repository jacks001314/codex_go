package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	MessageHistoryFilename     = "history.jsonl"
	MessageHistorySoftCapRatio = 0.8
)

type MessageHistoryPersistence string

const (
	MessageHistoryPersistenceSaveAll MessageHistoryPersistence = "save_all"
	MessageHistoryPersistenceNone    MessageHistoryPersistence = "none"
)

type MessageHistoryEntry struct {
	SessionID string `json:"session_id"`
	TS        uint64 `json:"ts"`
	Text      string `json:"text"`
}

type MessageHistoryConfig struct {
	CodexHome                 string                    `json:"codexHome"`
	MessageHistoryPersistence MessageHistoryPersistence `json:"persistence"`
	MaxBytes                  *int                      `json:"maxBytes,omitempty"`
}

func NewMessageHistoryConfig(codexHome string, persistence MessageHistoryPersistence, maxBytes *int) *MessageHistoryConfig {
	if persistence == "" {
		persistence = MessageHistoryPersistenceSaveAll
	}
	return &MessageHistoryConfig{CodexHome: codexHome, MessageHistoryPersistence: persistence, MaxBytes: cloneInt(maxBytes)}
}

func (c *MessageHistoryConfig) HistoryPath() string {
	if c == nil {
		return MessageHistoryFilename
	}
	return filepath.Join(c.CodexHome, MessageHistoryFilename)
}

func AppendMessageHistoryEntry(text string, conversationID string, config *MessageHistoryConfig) error {
	if config != nil && config.MessageHistoryPersistence == MessageHistoryPersistenceNone {
		return nil
	}
	path := config.HistoryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := MessageHistoryEntry{SessionID: conversationID, TS: uint64(time.Now().Unix()), Text: text}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := file.Write(line); err != nil {
		return err
	}
	return EnforceMessageHistoryLimit(file, maxBytes(config))
}

func EnforceMessageHistoryLimit(file *os.File, limit *int) error {
	if file == nil || limit == nil || *limit <= 0 {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= int64(*limit) {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	var lines [][]byte
	var newestLen int
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		line = append(line, '\n')
		newestLen = len(line)
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	target := MessageHistoryTrimTargetBytes(*limit, newestLen)
	total := 0
	kept := [][]byte{}
	for i := len(lines) - 1; i >= 0; i-- {
		if total >= target && len(kept) > 0 {
			break
		}
		kept = append(kept, lines[i])
		total += len(lines[i])
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	for _, line := range kept {
		if _, err := file.Write(line); err != nil {
			return err
		}
	}
	return nil
}

func MessageHistoryTrimTargetBytes(maxBytes int, newestEntryLen int) int {
	if maxBytes <= 0 {
		return newestEntryLen
	}
	soft := int(float64(maxBytes) * MessageHistorySoftCapRatio)
	if soft < 1 {
		soft = 1
	}
	if soft > maxBytes {
		soft = maxBytes
	}
	if newestEntryLen > soft {
		return newestEntryLen
	}
	return soft
}

func MessageHistoryMetadata(config *MessageHistoryConfig) (uint64, int) {
	path := config.HistoryPath()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || err != nil {
		return 0, 0
	}
	id := logIdentity(info)
	file, err := os.Open(path)
	if err != nil {
		return id, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	return id, count
}

func LookupMessageHistoryEntry(logID uint64, offset int, config *MessageHistoryConfig) (*MessageHistoryEntry, bool) {
	entries := LookupMessageHistoryEntries(logID, []int{offset}, config)
	entry, ok := entries[offset]
	return entry, ok
}

func LookupMessageHistoryEntries(logID uint64, offsets []int, config *MessageHistoryConfig) map[int]*MessageHistoryEntry {
	out := make(map[int]*MessageHistoryEntry)
	wanted := make(map[int]bool, len(offsets))
	for _, offset := range offsets {
		if offset >= 0 {
			wanted[offset] = true
		}
	}
	if len(wanted) == 0 {
		return out
	}
	path := config.HistoryPath()
	info, err := os.Stat(path)
	if err != nil {
		return out
	}
	if logID != 0 && logIdentity(info) != logID {
		return out
	}
	file, err := os.Open(path)
	if err != nil {
		return out
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	index := 0
	for scanner.Scan() {
		if wanted[index] {
			var entry MessageHistoryEntry
			if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
				value := entry
				out[index] = &value
			}
			if len(out) == len(wanted) {
				break
			}
		}
		index++
	}
	return out
}

func logIdentity(info os.FileInfo) uint64 {
	if info == nil {
		return 0
	}
	if runtime.GOOS == "windows" {
		return uint64(info.ModTime().UnixNano())
	}
	return uint64(info.ModTime().UnixNano())
}

func maxBytes(config *MessageHistoryConfig) *int {
	if config == nil {
		return nil
	}
	return config.MaxBytes
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

package history

import (
	"bufio"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Filename     = "history.jsonl"
	softCapRatio = 0.8
)

type Persistence string

const (
	PersistenceSaveAll Persistence = "save-all"
	PersistenceNone    Persistence = "none"
)

type Entry struct {
	SessionID string `json:"session_id"`
	Timestamp uint64 `json:"ts"`
	Text      string `json:"text"`
}

type Config struct {
	CodexHome   string
	Persistence Persistence
	MaxBytes    int64
}

func Path(config *Config) string {
	if config == nil {
		return Filename
	}
	return filepath.Join(config.CodexHome, Filename)
}

func AppendEntry(text string, sessionID string, config *Config, now time.Time) error {
	if config != nil && config.Persistence == PersistenceNone {
		return nil
	}
	path := Path(config)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	entry := Entry{SessionID: sessionID, Timestamp: uint64(now.Unix()), Text: text}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if config != nil && config.MaxBytes > 0 {
		return EnforceLimit(file, config.MaxBytes)
	}
	return nil
}

func Metadata(config *Config) (uint64, int, error) {
	path := Path(config)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, 0, err
	}
	count := 0
	reader := bufio.NewReader(file)
	for {
		_, err := reader.ReadString('\n')
		if err == nil {
			count++
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return identity(info), 0, err
	}
	return identity(info), count, nil
}

func Lookup(logID uint64, offset int, config *Config) (*Entry, bool) {
	path := Path(config)
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false
	}
	if logID != 0 && identity(info) != logID {
		return nil, false
	}
	scanner := bufio.NewScanner(file)
	index := 0
	for scanner.Scan() {
		if index == offset {
			var entry Entry
			if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
				return nil, false
			}
			return &entry, true
		}
		index++
	}
	return nil, false
}

func EnforceLimit(file *os.File, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	target := int64(float64(maxBytes) * softCapRatio)
	if target < 1 {
		target = 1
	}
	var kept []string
	size := int64(0)
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" {
			continue
		}
		if len(kept) > 0 && size+int64(len(line)) > target {
			break
		}
		kept = append([]string{line}, kept...)
		size += int64(len(line))
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	_, err = file.WriteString(strings.Join(kept, ""))
	return err
}

func identity(info os.FileInfo) uint64 {
	hash := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%d", info.Name(), info.Size(), info.ModTime().UnixNano())))
	var out uint64
	for _, b := range hash[:8] {
		out = (out << 8) | uint64(b)
	}
	return out
}

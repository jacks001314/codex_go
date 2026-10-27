package agentboard

// Operator-only setup for local message boards.
//
// Rust parity: codex-rs/ext/agent-message-board/src/local/setup.rs.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/state"
)

const (
	localBoardTemplateVersion   = 1
	localBoardTemplateMaxBytes  = 64 * 1024
	localBoardTemplateMaxChans  = 64
	localBoardDescriptionMaxLen = 512
)

// LocalBoardTemplateChannel configures one channel in a board template.
type LocalBoardTemplateChannel struct {
	Description *string `json:"description,omitempty"`
}

// LocalBoardTemplate is the versioned channel set pinned to a local board.
type LocalBoardTemplate struct {
	Version  uint32                               `json:"version"`
	Channels map[string]LocalBoardTemplateChannel `json:"channels"`
}

// LocalBoardSetup is operator input for configuring an empty local board.
// Agent tools cannot invoke this setup operation.
type LocalBoardSetup struct {
	Timestamp *time.Time         `json:"timestamp,omitempty"`
	Template  LocalBoardTemplate `json:"template"`
}

// UnmarshalJSON rejects unknown fields and trailing data, matching Rust's
// deny_unknown_fields setup contract.
func (s *LocalBoardSetup) UnmarshalJSON(data []byte) error {
	type setupJSON LocalBoardSetup
	var decoded setupJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("local board setup has trailing JSON data")
		}
		return err
	}
	*s = LocalBoardSetup(decoded)
	if s.Template.Channels == nil {
		s.Template.Channels = map[string]LocalBoardTemplateChannel{}
	}
	return nil
}

// ConfigureLocalBoard pins a template and creates its initial channels before
// agents start. Identical retries are safe; conflicting templates, previously
// used unpinned boards and permanently deleted boards are rejected.
func ConfigureLocalBoard(ctx context.Context, sqlite state.SqliteConfig, boardID string, setup LocalBoardSetup) error {
	return ConfigureLocalBoardWithOptions(ctx, sqlite, LocalBoardOptions{}, boardID, setup)
}

// ConfigureLocalBoardWithOptions pins a template in the selected storage
// namespace (Rust #55971).
func ConfigureLocalBoardWithOptions(ctx context.Context, sqlite state.SqliteConfig, options LocalBoardOptions, boardID string, setup LocalBoardSetup) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(boardID) == "" {
		return invalid("invalid board session identity")
	}
	template := setup.Template
	if template.Channels == nil {
		template.Channels = map[string]LocalBoardTemplateChannel{}
	}
	encoded, err := encodeLocalBoardTemplate(template)
	if err != nil {
		return err
	}
	path, err := localBoardDatabasePath(sqlite, options)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	db, err := acquireLocalBoardPool(ctx, sqlite, path)
	if err != nil {
		return err
	}
	defer releaseLocalBoardPool(path)

	conn, err := state.BeginImmediate(ctx, db)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
		_ = conn.Close()
	}()

	var deleted bool
	if err := conn.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM deleted_boards WHERE board=?)", boardID,
	).Scan(&deleted); err != nil {
		return err
	}
	if deleted {
		return invalid("the board has been permanently deleted")
	}

	var pinned string
	err = conn.QueryRowContext(ctx,
		"SELECT template FROM board_templates WHERE board=?", boardID,
	).Scan(&pinned)
	switch {
	case err == nil:
		if pinned != encoded {
			return invalid("the board template is already pinned")
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	default:
		var used bool
		if err := conn.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM channels WHERE board=?)", boardID,
		).Scan(&used); err != nil {
			return err
		}
		if used {
			return invalid("pin the template before creating channels or posts")
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO board_templates(board,template) VALUES(?,?)", boardID, encoded,
		); err != nil {
			return err
		}
		now := time.Now().UTC()
		if setup.Timestamp != nil {
			now = setup.Timestamp.UTC()
		}
		names := sortedLocalBoardChannelNames(template.Channels)
		for _, name := range names {
			if err := insertLocalBoardChannel(
				ctx, conn, boardID, name, agent.AgentPathRoot, now, template.Channels[name].Description,
			); err != nil {
				return err
			}
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func encodeLocalBoardTemplate(template LocalBoardTemplate) (string, error) {
	if template.Version != localBoardTemplateVersion ||
		len(template.Channels) > localBoardTemplateMaxChans {
		return "", invalid("board templates require version 1 and at most 64 KiB / 64 channels")
	}
	names := sortedLocalBoardChannelNames(template.Channels)
	for _, name := range names {
		if err := validateChannelName(name); err != nil {
			return "", err
		}
		description := template.Channels[name].Description
		if description != nil && len(*description) > localBoardDescriptionMaxLen {
			return "", invalid("channel descriptions must contain at most 512 UTF-8 bytes")
		}
	}
	encoded, err := json.Marshal(template)
	if err != nil {
		return "", err
	}
	if len(encoded) > localBoardTemplateMaxBytes {
		return "", invalid("board templates require version 1 and at most 64 KiB / 64 channels")
	}
	return string(encoded), nil
}

func sortedLocalBoardChannelNames(channels map[string]LocalBoardTemplateChannel) []string {
	names := make([]string, 0, len(channels))
	for name := range channels {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

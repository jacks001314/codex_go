package agentboard

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
)

func TestConfigureLocalBoardSetupSurvivesReopenLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	setupJSON := []byte(`{
		"timestamp":"2026-09-18T12:00:00Z",
		"template":{"version":1,"channels":{"policy":{"description":"Shared policies"}}}
	}`)
	var setup LocalBoardSetup
	if err := json.Unmarshal(setupJSON, &setup); err != nil {
		t.Fatalf("decode setup error = %v", err)
	}
	if err := ConfigureLocalBoard(ctx, sqlite, root, setup); err != nil {
		t.Fatalf("ConfigureLocalBoard() error = %v", err)
	}
	if err := ConfigureLocalBoard(ctx, sqlite, root, setup); err != nil {
		t.Fatalf("ConfigureLocalBoard() retry error = %v", err)
	}

	board := openLocalBoard(t, sqlite, root, host)
	channels, err := board.ListChannels(ctx, root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 20}})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels.Results) != 1 {
		t.Fatalf("channels = %#v, want one", channels)
	}
	policy := channels.Results[0]
	if policy.ChannelName != "policy" || policy.Description == nil || *policy.Description != "Shared policies" {
		t.Fatalf("policy = %#v", policy)
	}
	if policy.CreatedBy != agent.AgentPathRoot {
		t.Fatalf("CreatedBy = %q, want /root", policy.CreatedBy)
	}
	wantTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if !policy.CreatedAt.Equal(wantTime) {
		t.Fatalf("CreatedAt = %v, want %v", policy.CreatedAt, wantTime)
	}
	board.Close()

	board = openLocalBoard(t, sqlite, root, host)
	defer board.Close()
	channels, err = board.ListChannels(ctx, root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 20}})
	if err != nil {
		t.Fatalf("ListChannels() after reopen error = %v", err)
	}
	if len(channels.Results) != 1 || channels.Results[0].Description == nil ||
		*channels.Results[0].Description != "Shared policies" {
		t.Fatalf("channels after reopen = %#v", channels)
	}

	var conflicting LocalBoardSetup
	if err := json.Unmarshal([]byte(`{
		"template":{"version":1,"channels":{"policy":{"description":"Changed"}}}
	}`), &conflicting); err != nil {
		t.Fatalf("decode conflicting setup error = %v", err)
	}
	err = ConfigureLocalBoard(ctx, sqlite, root, conflicting)
	if err == nil || !IsInvalidRequest(err) || err.Error() != "the board template is already pinned" {
		t.Fatalf("conflicting setup error = %v", err)
	}

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "new"}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if err := ConfigureLocalBoard(ctx, sqlite, root, setup); err != nil {
		t.Fatalf("identical retry after CreateChannel() error = %v", err)
	}
	channels, err = board.ListChannels(ctx, root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 20}})
	if err != nil {
		t.Fatalf("ListChannels() after retry error = %v", err)
	}
	if len(channels.Results) != 2 {
		t.Fatalf("channels after retry = %#v, want two", channels)
	}

	if err := DeleteLocalBoards(ctx, sqlite, []string{root}); err != nil {
		t.Fatalf("DeleteLocalBoards() error = %v", err)
	}
	err = ConfigureLocalBoard(ctx, sqlite, root, setup)
	if err == nil || !IsInvalidRequest(err) || err.Error() != "the board has been permanently deleted" {
		t.Fatalf("configure deleted board error = %v", err)
	}
}

func TestConfigureLocalBoardRequiresPinnedTemplateOnUsedBoardLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()
	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "existing"}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	setup := LocalBoardSetup{
		Template: LocalBoardTemplate{
			Version: 1,
			Channels: map[string]LocalBoardTemplateChannel{
				"policy": {Description: pointerTo("Shared policies")},
			},
		},
	}
	err := ConfigureLocalBoard(ctx, sqlite, root, setup)
	if err == nil || !IsInvalidRequest(err) ||
		err.Error() != "pin the template before creating channels or posts" {
		t.Fatalf("configure used board error = %v", err)
	}
}

func TestConfigureLocalBoardValidatesTemplateLikeRust(t *testing.T) {
	tooMany := make(map[string]LocalBoardTemplateChannel, localBoardTemplateMaxChans+1)
	for i := 0; i <= localBoardTemplateMaxChans; i++ {
		tooMany[fmt.Sprintf("channel-%02d", i)] = LocalBoardTemplateChannel{}
	}
	tooLarge := make(map[string]LocalBoardTemplateChannel, localBoardTemplateMaxChans)
	largeDescription := strings.Repeat(`\`, localBoardDescriptionMaxLen)
	for i := 0; i < localBoardTemplateMaxChans; i++ {
		tooLarge[fmt.Sprintf("channel-%02d", i)] = LocalBoardTemplateChannel{Description: &largeDescription}
	}
	tests := []struct {
		name  string
		setup LocalBoardSetup
	}{
		{
			name: "wrong version",
			setup: LocalBoardSetup{Template: LocalBoardTemplate{
				Version:  2,
				Channels: map[string]LocalBoardTemplateChannel{},
			}},
		},
		{
			name: "too many channels",
			setup: LocalBoardSetup{Template: LocalBoardTemplate{
				Version:  1,
				Channels: tooMany,
			}},
		},
		{
			name: "encoded template too large",
			setup: LocalBoardSetup{Template: LocalBoardTemplate{
				Version:  1,
				Channels: tooLarge,
			}},
		},
		{
			name: "description too long",
			setup: LocalBoardSetup{Template: LocalBoardTemplate{
				Version: 1,
				Channels: map[string]LocalBoardTemplateChannel{
					"policy": {Description: pointerTo(strings.Repeat("a", localBoardDescriptionMaxLen+1))},
				},
			}},
		},
		{
			name: "invalid channel name",
			setup: LocalBoardSetup{Template: LocalBoardTemplate{
				Version: 1,
				Channels: map[string]LocalBoardTemplateChannel{
					" invalid": {},
				},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sqlite := openBoardSqliteConfig(t)
			err := ConfigureLocalBoard(context.Background(), sqlite, "root-thread", test.setup)
			if err == nil || !IsInvalidRequest(err) {
				t.Fatalf("ConfigureLocalBoard() error = %v, want invalid request", err)
			}
		})
	}
}

func TestLocalBoardSetupRejectsUnknownFieldsLikeRust(t *testing.T) {
	tests := []string{
		`{"unknown":true,"template":{"version":1,"channels":{}}}`,
		`{"template":{"unknown":true,"version":1,"channels":{}}}`,
		`{"template":{"version":1,"channels":{"policy":{"unknown":true}}}}`,
		`{"template":{"version":1,"channels":{}}} trailing`,
	}
	for _, input := range tests {
		var setup LocalBoardSetup
		if err := json.Unmarshal([]byte(input), &setup); err == nil {
			t.Errorf("json.Unmarshal(%s) succeeded, want unknown-field or trailing-data error", input)
		}
	}
}

func TestLocalBoardChannelDescriptionsMigrateLegacyStorageLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	path := filepath.Join(sqlite.Home(), AgentMessageBoardDatabaseFile)
	db, err := sqlite.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadWrite() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE channels (
		board TEXT NOT NULL, name TEXT NOT NULL, name_search TEXT NOT NULL,
		created_at TEXT NOT NULL, timestamp INTEGER NOT NULL, author TEXT NOT NULL,
		PRIMARY KEY(board,name)
	)`); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy channels error = %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO channels VALUES(?,?,?,?,?,?)",
		root, "legacy", "legacy", "2026-09-17T12:00:00Z", 0, "/root",
	); err != nil {
		_ = db.Close()
		t.Fatalf("insert legacy channel error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database error = %v", err)
	}

	board := openLocalBoard(t, sqlite, root, host)
	channels, err := board.ListChannels(ctx, root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 20}})
	if err != nil {
		board.Close()
		t.Fatalf("ListChannels() error = %v", err)
	}
	board.Close()
	if len(channels.Results) != 1 || channels.Results[0].ChannelName != "legacy" ||
		channels.Results[0].Description != nil {
		t.Fatalf("legacy channels = %#v", channels)
	}
}

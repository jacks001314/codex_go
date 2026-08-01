package state

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBundledSQLiteMeetsRustMinimum(t *testing.T) {
	db, err := OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer db.Close()

	var version string
	if err := db.QueryRow(`SELECT sqlite_version()`).Scan(&version); err != nil {
		t.Fatalf("query sqlite_version(): %v", err)
	}
	ok, err := SQLiteVersionAtLeast(version, MinimumSQLiteVersion)
	if err != nil || !ok {
		t.Fatalf("bundled SQLite version = %q, minimum = %q, ok = %v, err = %v", version, MinimumSQLiteVersion, ok, err)
	}
}

func TestNativeSQLiteArtifactPlatform(t *testing.T) {
	wantOS := os.Getenv("CODEX_EXPECT_GOOS")
	wantArch := os.Getenv("CODEX_EXPECT_GOARCH")
	if wantOS == "" && wantArch == "" {
		t.Skip("native artifact expectations are set by sqlite-platform-gates")
	}
	if runtime.GOOS != wantOS || runtime.GOARCH != wantArch {
		t.Fatalf("native artifact platform = %s/%s, want %s/%s", runtime.GOOS, runtime.GOARCH, wantOS, wantArch)
	}
	db, err := OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version string
	if err := db.QueryRow(`SELECT sqlite_version()`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("native artifact %s/%s embeds SQLite %s", runtime.GOOS, runtime.GOARCH, version)
}

func TestSQLiteVersionAtLeast(t *testing.T) {
	tests := []struct {
		name    string
		version string
		minimum string
		want    bool
		wantErr bool
	}{
		{name: "equal", version: "3.51.3", minimum: "3.51.3", want: true},
		{name: "newer patch", version: "3.51.4", minimum: "3.51.3", want: true},
		{name: "newer minor", version: "3.52.0", minimum: "3.51.3", want: true},
		{name: "older patch", version: "3.51.2", minimum: "3.51.3"},
		{name: "older minor", version: "3.50.9", minimum: "3.51.3"},
		{name: "trimmed", version: " 3.51.3 ", minimum: "3.51.3", want: true},
		{name: "missing patch", version: "3.51", minimum: "3.51.3", wantErr: true},
		{name: "invalid", version: "3.latest.3", minimum: "3.51.3", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := SQLiteVersionAtLeast(test.version, test.minimum)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("SQLiteVersionAtLeast(%q, %q) = %v, %v", test.version, test.minimum, got, err)
			}
		})
	}
}

func TestSQLiteRecoversCommittedWALAfterAbruptExit(t *testing.T) {
	if os.Getenv("CODEX_GO_SQLITE_WAL_HELPER") == "1" {
		writeCommittedWALAndExit(os.Getenv("CODEX_GO_SQLITE_WAL_PATH"))
		return
	}
	dbPath := filepath.Join(t.TempDir(), "wal-recovery.sqlite")
	command := exec.Command(os.Args[0], "-test.run=^TestSQLiteRecoversCommittedWALAfterAbruptExit$")
	command.Env = append(os.Environ(),
		"CODEX_GO_SQLITE_WAL_HELPER=1",
		"CODEX_GO_SQLITE_WAL_PATH="+dbPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("WAL writer subprocess: %v\n%s", err, output)
	}
	walInfo, err := os.Stat(dbPath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("writer did not leave committed WAL frames: info=%#v err=%v", walInfo, err)
	}
	config, err := NewSqliteConfig(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	db, err := config.OpenReadWrite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen WAL database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wal_rows`).Scan(&count); err != nil || count != 256 {
		t.Fatalf("recovered row count = %d, %v", count, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
}

func writeCommittedWALAndExit(dbPath string) {
	config, err := NewSqliteConfig(filepath.Dir(dbPath))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db, err := config.OpenReadWrite(context.Background(), dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA wal_autocheckpoint = 0`); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := db.Exec(`CREATE TABLE wal_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for i := 0; i < 256; i++ {
		if _, err := tx.Exec(`INSERT INTO wal_rows (value) VALUES (?)`, fmt.Sprintf("row-%03d", i)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if err := tx.Commit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

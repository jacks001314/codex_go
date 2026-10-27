package state

// Converts WAL databases to DELETE journaling while preserving recoverable WAL
// backups (Rust #57225/#57226, codex-rs/state/src/nfs_migration.rs). Callers
// serialize startup and migration with the directory lock; other SQLite clients
// must remain stopped during replacement and NFS must enforce cross-client POSIX
// locks. Migration needs write access to the directory and its persistent lock.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var errNFSMigrationLocked = errors.New("another database migration is running")

const (
	nfsMigrationMarker     = "codex-nfs-migration-v1\n"
	nfsMigrationLockName   = ".codex-nfs-migration.lock"
	nfsMigrationBackupTail = "_wal_migration_backup"
	nfsMigrationCopyName   = "delete.sqlite"
)

// NFSMigrationOutcomeKind reports what Migrate did for one database.
type NFSMigrationOutcomeKind string

const (
	NFSMigrationMissing       NFSMigrationOutcomeKind = "missing"
	NFSMigrationAlreadyDelete NFSMigrationOutcomeKind = "alreadyDelete"
	NFSMigrationMigrated      NFSMigrationOutcomeKind = "migrated"
)

// NFSMigrationOutcome is the caller-owned reporting result of a migration.
type NFSMigrationOutcome struct {
	Kind            NFSMigrationOutcomeKind
	BackupDirectory string
}

// NFSDBMigration holds the directory lock shared by automatic startup
// migration and `codex migrate-nfs-db`.
type NFSDBMigration struct {
	lock *fileLock
}

// NFSSQLiteMigrationSupported reports whether this platform provides the Unix
// filesystem locking and directory fsync the migration relies on.
func NFSSQLiteMigrationSupported() bool {
	return runtime.GOOS != "windows"
}

// LockNFSDBMigration takes the non-blocking directory lock. The lock file is
// never removed: concurrent openers must lock the same inode.
func LockNFSDBMigration(sqliteHome string) (*NFSDBMigration, error) {
	if !NFSSQLiteMigrationSupported() {
		return nil, errors.New("NFS database migration requires Unix filesystem locking and directory fsync")
	}
	lock, err := tryLockFile(filepath.Join(sqliteHome, nfsMigrationLockName))
	if err != nil {
		return nil, err
	}
	if lock == nil {
		return nil, errNFSMigrationLocked
	}
	return &NFSDBMigration{lock: lock}, nil
}

// Close releases the migration lock.
func (m *NFSDBMigration) Close() error {
	if m == nil || m.lock == nil {
		return nil
	}
	return m.lock.Unlock()
}

// NFSDatabaseNeedsMigration inspects the on-disk SQLite header without opening
// the database, so it neither accesses WAL shared memory nor releases another
// connection's locks. A missing source still reports pending recovery files.
func NFSDatabaseNeedsMigration(source string) (bool, error) {
	exists, err := nfsRegularFileExists(source)
	if err != nil {
		return false, err
	}
	replacement := newNFSReplacement(source)
	if !exists {
		backup, err := nfsRegularFileExists(replacement.backupDatabase)
		if err != nil {
			return false, err
		}
		if backup {
			return true, nil
		}
		return nfsRegularFileExists(replacement.replacementPath())
	}
	file, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer file.Close()
	header := make([]byte, 20)
	if _, err := io.ReadFull(file, header); err != nil {
		return false, fmt.Errorf("cannot read database header: %w", err)
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		return false, errors.New("invalid database header")
	}
	// SQLite file-format read/write versions are 1 for rollback and 2 for WAL.
	return header[18] == 2 || header[19] == 2, nil
}

// NFSDatabaseNeedsPermissionConfirmation reports whether the source or recovery
// files have permission bits other than 0600. Only metadata is inspected:
// opening WAL databases can create shared-memory files.
func NFSDatabaseNeedsPermissionConfirmation(source string) (bool, error) {
	// Rust only compares Unix permission bits (`#[cfg(unix)]`), so Windows
	// never asks for the mode-specific confirmation.
	if runtime.GOOS == "windows" {
		return false, nil
	}
	replacement := newNFSReplacement(source)
	for _, path := range []string{source, replacement.backupDatabase, replacement.replacementPath()} {
		exists, err := nfsRegularFileExists(path)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		if info.Mode().Perm() != 0o600 {
			return true, nil
		}
	}
	return false, nil
}

// Migrate converts an existing WAL database or resumes an interrupted
// replacement. All other clients must remain stopped until it returns.
func (m *NFSDBMigration) Migrate(ctx context.Context, source, stagingDir string) (NFSMigrationOutcome, error) {
	replacement := newNFSReplacement(source)
	installed, err := replacement.resume(ctx)
	if err != nil {
		return NFSMigrationOutcome{}, err
	}
	if installed {
		return NFSMigrationOutcome{Kind: NFSMigrationMigrated, BackupDirectory: replacement.backupDirectory}, nil
	}
	exists, err := nfsRegularFileExists(source)
	if err != nil {
		return NFSMigrationOutcome{}, err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecarExists, err := nfsRegularFileExists(source + suffix)
		if err != nil {
			return NFSMigrationOutcome{}, err
		}
		if !exists && sidecarExists {
			return NFSMigrationOutcome{}, errors.New("database is missing but a SQLite sidecar exists")
		}
	}
	if !exists {
		return NFSMigrationOutcome{Kind: NFSMigrationMissing}, nil
	}
	if strings.TrimSpace(stagingDir) == "" {
		stagingDir = os.TempDir()
	}

	db, err := openNFSMigrationSource(ctx, source)
	if err != nil {
		return NFSMigrationOutcome{}, fmt.Errorf("cannot open source database exclusively; close its SQLite clients on all hosts: %w", err)
	}
	// migrateOpenSource owns the connection: it closes the source before moving
	// it. Closing again here on the error path is a no-op.
	changed, migrateErr := migrateOpenSource(ctx, db, source, stagingDir, replacement)
	if migrateErr != nil {
		_ = db.Close()
		return NFSMigrationOutcome{}, migrateErr
	}
	if !changed {
		return NFSMigrationOutcome{Kind: NFSMigrationAlreadyDelete}, nil
	}
	if err := replacement.install(); err != nil {
		return NFSMigrationOutcome{}, err
	}
	return NFSMigrationOutcome{Kind: NFSMigrationMigrated, BackupDirectory: replacement.backupDirectory}, nil
}

func migrateOpenSource(ctx context.Context, db *sql.DB, source, stagingDir string, replacement nfsReplacement) (bool, error) {
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return false, fmt.Errorf("cannot access source database exclusively; close its SQLite clients on all hosts: %w", err)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == sqliteJournalModeDelete {
		if prepared, err := nfsRegularFileExists(replacement.replacementPath()); err != nil {
			return false, err
		} else if prepared {
			return false, errors.New("unfinished backup exists")
		}
		return false, nil
	}
	if mode != sqliteJournalModeWAL {
		return false, fmt.Errorf("source database must be in WAL mode (found %s)", mode)
	}
	// BEGIN EXCLUSIVE alone blocks only WAL writers. EXCLUSIVE locking mode also
	// blocks readers and keeps the database lock after ROLLBACK.
	if _, err := db.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		return false, fmt.Errorf("cannot lock source database exclusively; close its SQLite clients on all hosts: %w", err)
	}
	if _, err := db.ExecContext(ctx, "ROLLBACK"); err != nil {
		return false, fmt.Errorf("cannot lock source database exclusively; close its SQLite clients on all hosts: %w", err)
	}

	info, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	if info.Mode().Perm()&0o600 != 0o600 {
		return false, errors.New("database owner permissions must include read and write so the replacement remains accessible to its new owner")
	}
	sourceFiles := make([]*os.File, 0, 2)
	mainFile, err := os.Open(source)
	if err != nil {
		return false, err
	}
	sourceFiles = append(sourceFiles, mainFile)
	defer func() {
		for _, file := range sourceFiles {
			_ = file.Close()
		}
	}()
	if walFile, err := os.Open(source + "-wal"); err == nil {
		sourceFiles = append(sourceFiles, walFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("cannot open source WAL: %w", err)
	}

	staging, err := os.MkdirTemp(stagingDir, "codex-nfs-migration-")
	if err != nil {
		return false, fmt.Errorf("cannot create local staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	stagedDatabase := filepath.Join(staging, "database.sqlite")
	names := []string{"database.sqlite", "database.sqlite-wal"}
	for i, file := range sourceFiles {
		if err := copyFileBuffered(file, filepath.Join(staging, names[i])); err != nil {
			return false, fmt.Errorf("cannot stage source database and WAL: %w", err)
		}
	}

	if err := convertStagedDatabaseToDelete(ctx, stagedDatabase); err != nil {
		return false, err
	}
	if err := replacement.prepareBackupDirectory(); err != nil {
		return false, err
	}
	staged, err := os.Open(stagedDatabase)
	if err != nil {
		return false, err
	}
	written, err := writeNFSReplacementFile(replacement.backupDirectory, staged, info.Mode().Perm())
	_ = staged.Close()
	if err != nil {
		return false, fmt.Errorf("cannot write replacement copy: %w", err)
	}
	if err := os.Rename(written, replacement.replacementPath()); err != nil {
		return false, fmt.Errorf("cannot save replacement copy: %w", err)
	}
	if err := syncDirectory(replacement.backupDirectory); err != nil {
		return false, err
	}
	// Deliberate Go difference: modernc has no SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE,
	// so the source connection is closed before the original is moved. The
	// checkpoint then lands in the still-installed source instead of the backup,
	// which keeps the preserved database complete; Rust disables the checkpoint
	// and moves the WAL aside first. The staged copy is already validated, and the
	// source descriptors are closed only after this connection, so SQLite keeps
	// its exclusive lock while the copy is written.
	if err := db.Close(); err != nil {
		return false, fmt.Errorf("cannot close source database; keep clients stopped: %w", err)
	}
	if err := replacement.moveOriginalToBackup(); err != nil {
		return false, err
	}
	return true, nil
}

func openNFSMigrationSource(ctx context.Context, source string) (*sql.DB, error) {
	dsn, err := nfsMigrationDSN(source, []string{
		"locking_mode(EXCLUSIVE)",
		"busy_timeout(5000)",
		"synchronous(FULL)",
	})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := RequireSQLiteVersion(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func convertStagedDatabaseToDelete(ctx context.Context, path string) error {
	dsn, err := nfsMigrationDSN(path, []string{
		"locking_mode(EXCLUSIVE)",
		"journal_mode(DELETE)",
		"synchronous(FULL)",
		"busy_timeout(5000)",
	})
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	validateErr := validateDeleteDatabase(ctx, db)
	closeErr := db.Close()
	if validateErr != nil {
		return fmt.Errorf("staged database failed validation: %w", validateErr)
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func validateDeleteDatabase(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(mode), sqliteJournalModeDelete) {
		return fmt.Errorf("database did not enter DELETE journal mode (found %s)", mode)
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check(1)`).Scan(&integrity); err != nil {
		return fmt.Errorf("database failed integrity check: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(integrity), "ok") {
		return fmt.Errorf("database failed integrity check: %s", integrity)
	}
	return nil
}

func nfsMigrationDSN(path string, pragmas []string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uriPath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	var query strings.Builder
	query.WriteString("mode=rw")
	for _, pragma := range pragmas {
		query.WriteString("&_pragma=")
		query.WriteString(pragma)
	}
	return "file:" + uriPath + "?" + query.String(), nil
}

type nfsReplacement struct {
	source          string
	backupDirectory string
	backupDatabase  string
}

func newNFSReplacement(source string) nfsReplacement {
	name := filepath.Base(source)
	directory := source + nfsMigrationBackupTail
	return nfsReplacement{
		source:          source,
		backupDirectory: directory,
		backupDatabase:  filepath.Join(directory, name),
	}
}

func (r nfsReplacement) replacementPath() string {
	return filepath.Join(r.backupDirectory, nfsMigrationCopyName)
}

// prepareBackupDirectory creates the private backup directory (or validates its
// marker) and removes an unused replacement copy. Call only while the original
// database is locked.
func (r nfsReplacement) prepareBackupDirectory() error {
	info, err := os.Lstat(r.backupDirectory)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("backup path already exists and is not a directory: %s", r.backupDirectory)
		}
		if exists, err := nfsRegularFileExists(r.backupDatabase); err != nil {
			return err
		} else if exists {
			return errors.New("WAL backup already exists")
		}
		if exists, err := nfsRegularFileExists(r.replacementPath()); err != nil {
			return err
		} else if exists {
			if err := os.Remove(r.replacementPath()); err != nil {
				return err
			}
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(r.backupDirectory, 0o700); err != nil {
			return err
		}
		marker := filepath.Join(r.backupDirectory, "migration-v1")
		file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.WriteString(nfsMigrationMarker); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	default:
		return err
	}
	return r.syncDirectories()
}

// moveOriginalToBackup moves the main file first so an incomplete switch never
// looks like an intact source, then the SQLite sidecars.
func (r nfsReplacement) moveOriginalToBackup() error {
	if exists, err := nfsRegularFileExists(r.backupDatabase); err != nil {
		return err
	} else if exists {
		return errors.New("WAL backup already exists")
	}
	if err := os.Rename(r.source, r.backupDatabase); err != nil {
		return err
	}
	if err := r.syncDirectories(); err != nil {
		return err
	}
	return r.moveSidecarsToBackup()
}

func (r nfsReplacement) moveSidecarsToBackup() error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		source := r.source + suffix
		destination := r.backupDatabase + suffix
		exists, err := nfsRegularFileExists(source)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if exists, err := nfsRegularFileExists(destination); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("backup sidecar already exists: %s", destination)
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
	}
	return r.syncDirectories()
}

// install links the prepared copy at the source path atomically without
// replacing an unexpected database.
func (r nfsReplacement) install() error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if exists, err := nfsRegularFileExists(r.source + suffix); err != nil {
			return err
		} else if exists {
			return errors.New("database or sidecar already exists at the installation path")
		}
	}
	if err := os.Link(r.replacementPath(), r.source); err != nil {
		return err
	}
	return r.finishInstallation()
}

func (r nfsReplacement) finishInstallation() error {
	if err := r.syncDirectories(); err != nil {
		return err
	}
	if err := os.Remove(r.replacementPath()); err != nil {
		return err
	}
	return r.syncDirectories()
}

// resume inspects the files an interrupted replacement left behind. It returns
// true when it installed the saved copy, so no new replacement is necessary.
func (r nfsReplacement) resume(ctx context.Context) (bool, error) {
	info, err := os.Lstat(r.backupDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, errors.New("backup path already exists and is not a directory")
	}
	markerPath := filepath.Join(r.backupDirectory, "migration-v1")
	markerExists, err := nfsRegularFileExists(markerPath)
	if err != nil {
		return false, err
	}
	if !markerExists {
		return false, fmt.Errorf("backup directory already exists without a valid migration marker: %s", r.backupDirectory)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		return false, err
	}
	if string(marker) != nfsMigrationMarker {
		return false, fmt.Errorf("backup directory already exists without a valid migration marker: %s", r.backupDirectory)
	}
	backupExists, err := nfsRegularFileExists(r.backupDatabase)
	if err != nil {
		return false, err
	}
	replacementExists, err := nfsRegularFileExists(r.replacementPath())
	if err != nil {
		return false, err
	}
	sourceExists, err := nfsRegularFileExists(r.source)
	if err != nil {
		return false, err
	}
	if !backupExists {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if sidecar, err := nfsRegularFileExists(r.backupDatabase + suffix); err != nil {
				return false, err
			} else if sidecar {
				return false, errors.New("backup has a SQLite sidecar without its original database")
			}
		}
		if !sourceExists {
			return false, errors.New("original database is missing from both source and backup")
		}
		return false, nil
	}
	if !replacementExists {
		if !sourceExists {
			return false, errors.New("replacement is missing. Preserve the WAL backup and restore it before retrying")
		}
		return false, nil
	}
	if sourceExists {
		same, err := sameFile(r.source, r.replacementPath())
		if err != nil {
			return false, err
		}
		if !same {
			return false, errors.New("another database already exists at the installation path")
		}
		if err := r.finishInstallation(); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := validatePreparedReplacement(ctx, r.replacementPath()); err != nil {
		return false, fmt.Errorf("saved replacement failed validation: %w", err)
	}
	if err := r.moveSidecarsToBackup(); err != nil {
		return false, err
	}
	if err := r.install(); err != nil {
		return false, err
	}
	return true, nil
}

func validatePreparedReplacement(ctx context.Context, path string) error {
	dsn, err := nfsReadOnlyImmutableDSN(path)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	err = validateDeleteDatabase(ctx, db)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	return err
}

func nfsReadOnlyImmutableDSN(path string) (string, error) {
	dsn, err := nfsMigrationDSN(path, nil)
	if err != nil {
		return "", err
	}
	return strings.Replace(dsn, "?mode=rw", "?mode=ro&immutable=1", 1), nil
}

func nfsRegularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("path already exists and is not a regular file: %s", path)
	}
	return true, nil
}

func (r nfsReplacement) syncDirectories() error {
	if err := syncDirectory(r.backupDirectory); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(r.source))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func sameFile(first, second string) (bool, error) {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false, err
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		return false, err
	}
	return os.SameFile(firstInfo, secondInfo), nil
}

func writeNFSReplacementFile(directory string, source *os.File, mode os.FileMode) (string, error) {
	target, err := os.CreateTemp(directory, "replacement-")
	if err != nil {
		return "", err
	}
	name := target.Name()
	cleanup := func() {
		_ = target.Close()
		_ = os.Remove(name)
	}
	if err := copyFileTo(source, target); err != nil {
		cleanup()
		return "", err
	}
	if err := target.Chmod(mode); err != nil {
		cleanup()
		return "", err
	}
	if err := target.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := target.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func copyFileBuffered(source *os.File, destination string) error {
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := copyFileTo(source, target); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}

func copyFileTo(source *os.File, destination *os.File) error {
	// Large reads avoid the fallback to small reads for copy_file_range on
	// NFSv4.1 (Rust copy_buffered).
	buffer := make([]byte, 1024*1024)
	for {
		read, err := source.Read(buffer)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

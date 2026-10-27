package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/state"
)

// runMigrateNFSDB mirrors Rust cli/src/migrate_nfs_db.rs (#57225/#57226):
// confirm the unsafe cutover, disclose the ownership effects, then convert every
// configured runtime database to DELETE journaling with WAL backups.
func runMigrateNFSDB(ctx context.Context, opts cli.MigrateNFSDBOptions, root cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if !state.NFSSQLiteMigrationSupported() {
		return errors.New("migrate-nfs-db currently requires Unix filesystem locking and directory fsync")
	}
	fmt.Fprintln(stderr, "WARNING: This operation is unsafe under concurrency.")
	fmt.Fprintln(stderr, "Stop all Codex instances on every host that uses these databases.")
	fmt.Fprintln(stderr, "Keep them stopped until the migration completes successfully.")

	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{
		RawOverrides:         append([]string(nil), root.ConfigOverrides...),
		EnableFeatures:       append([]string(nil), root.EnableFeatures...),
		DisableFeatures:      append([]string(nil), root.DisableFeatures...),
		IncludeManagedConfig: true,
	})
	if err != nil {
		return err
	}
	// An explicit log directory moves logs out of the shared home, so its
	// database keeps its own journal mode.
	skipLogs := strings.TrimSpace(configStringValue(loaded.Values["log_dir"])) != ""
	sqlite, err := state.SqliteConfigForCodexHomeWithOverride(codexHome, loaded.SQLiteHome())
	if err != nil {
		return err
	}
	if _, err := os.Stat(sqlite.Home()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stdout, "No databases found.")
			return nil
		}
		return err
	}
	needsPermissionConfirmation := false
	for _, database := range sqlite.RuntimeDBPaths() {
		if skipLogs && database.Path == sqlite.LogsDBPath() {
			continue
		}
		needs, err := state.NFSDatabaseNeedsPermissionConfirmation(database.Path)
		if err != nil {
			return err
		}
		needsPermissionConfirmation = needsPermissionConfirmation || needs
	}
	if needsPermissionConfirmation {
		fmt.Fprintln(stderr, "Some database files have permissions other than 0600.")
		fmt.Fprintln(stderr, "Migration preserves file mode bits, including group and other access.")
	}
	fmt.Fprintln(stderr, "Replacement files belong to you; the original owner, group, and extended ACLs are not copied.")
	fmt.Fprintln(stderr, "This can remove another user's access even when file permissions are 0600.")
	fmt.Fprintln(stderr, "Original files remain in a private backup directory.")
	fmt.Fprintln(stderr, "You need write access to the database directory and its migration lock.")
	prompt := "Continue? [y/N]:"
	if needsPermissionConfirmation {
		prompt = "Migrate databases, including files with permissions other than 0600? [y/N]:"
	}
	confirmed, err := confirmNFSMigration(stdin, stderr, prompt)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("Migration cancelled. No databases were changed.")
	}

	migration, err := state.LockNFSDBMigration(sqlite.Home())
	if err != nil {
		return err
	}
	defer migration.Close()
	for _, database := range sqlite.RuntimeDBPaths() {
		if skipLogs && database.Path == sqlite.LogsDBPath() {
			fmt.Fprintln(stdout, "Skipped logs database: log_dir is configured.")
			continue
		}
		outcome, err := migration.Migrate(ctx, database.Path, opts.StagingDir)
		if err != nil {
			return fmt.Errorf("migration failed for %s. Keep all Codex instances stopped. Correct the error and rerun codex migrate-nfs-db: %w", database.Path, err)
		}
		switch outcome.Kind {
		case state.NFSMigrationMissing:
		case state.NFSMigrationAlreadyDelete:
			fmt.Fprintf(stdout, "Already in DELETE mode: %s\n", database.Path)
		case state.NFSMigrationMigrated:
			fmt.Fprintf(stdout, "Migrated database: %s\nWAL backup: %s\n", database.Path, outcome.BackupDirectory)
		}
	}
	fmt.Fprintln(stdout, "Migration completed successfully. You can restart Codex.")
	return nil
}

func confirmNFSMigration(stdin io.Reader, stderr io.Writer, prompt string) (bool, error) {
	if stdin == nil {
		return false, nil
	}
	fmt.Fprintln(stderr, prompt)
	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func configStringValue(value any) string {
	text, _ := value.(string)
	return text
}

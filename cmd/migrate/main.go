package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kompu-backend/internal/config"
	"kompu-backend/internal/database"
)

const (
	migrationsDir = "migrations"
	trackingTable = "kompu_schema_migrations"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		log.Fatal("usage: go run ./cmd/migrate [up|down]")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := database.New(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer pool.Close()

	if err := ensureTrackingTable(ctx, pool); err != nil {
		log.Fatalf("tracking table: %v", err)
	}

	switch os.Args[1] {
	case "up":
		err = up(ctx, pool)
	case "down":
		err = down(ctx, pool)
	}
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func ensureTrackingTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.`+trackingTable+` (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		ALTER TABLE public.`+trackingTable+` ENABLE ROW LEVEL SECURITY;
	`)
	return err
}

func up(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	count := 0
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".up.sql")
		if applied[version] {
			continue
		}
		script, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		track := `INSERT INTO public.` + trackingTable + ` (version) VALUES ($1)`
		if err := runInTx(ctx, pool, string(script), track, version); err != nil {
			return fmt.Errorf("%s: %w", version, err)
		}
		fmt.Println("applied:", version)
		count++
	}
	if count == 0 {
		fmt.Println("nothing to apply")
	}
	return nil
}

// down reverts only the most recently applied migration.
func down(ctx context.Context, pool *pgxpool.Pool) error {
	var version string
	err := pool.QueryRow(ctx,
		`SELECT version FROM public.`+trackingTable+` ORDER BY version DESC LIMIT 1`,
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		fmt.Println("nothing to revert")
		return nil
	}
	if err != nil {
		return err
	}

	script, err := os.ReadFile(filepath.Join(migrationsDir, version+".down.sql"))
	if err != nil {
		return err
	}
	track := `DELETE FROM public.` + trackingTable + ` WHERE version = $1`
	if err := runInTx(ctx, pool, string(script), track, version); err != nil {
		return fmt.Errorf("%s: %w", version, err)
	}
	fmt.Println("reverted:", version)
	return nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM public.`+trackingTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// runInTx runs the SQL script and the tracking statement atomically:
// either both succeed or nothing changes.
func runInTx(ctx context.Context, pool *pgxpool.Pool, script, trackSQL, version string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(ctx, script); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, trackSQL, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

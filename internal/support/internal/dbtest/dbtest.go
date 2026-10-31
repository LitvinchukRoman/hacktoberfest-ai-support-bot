// Package dbtest gives a test its own empty Postgres schema with every migration
// applied, so database tests can run in parallel with each other and with the
// operations side's tests, which clean the shared tables. Only tests import it.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool returns a pool whose connections live in a fresh schema. It skips the test
// when TEST_DATABASE_URL is not set, and drops the schema when the test ends.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	schema := "t_" + hex.EncodeToString(id[:])
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect to schema: %v", err)
	}
	t.Cleanup(pool.Close)

	for _, name := range migrations(t) {
		sql, err := os.ReadFile(name) // #nosec G304 -- the repository's own migration files
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(name), err)
		}
	}
	return pool
}

// migrations lists the up migrations of the repository in version order.
func migrations(t testing.TB) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations")
	names, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations under %s: %v", dir, err)
	}
	sort.Strings(names)
	return names
}

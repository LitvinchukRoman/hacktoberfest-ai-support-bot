package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// freshSchema is a pool whose connections live in a new empty schema.
func freshSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	schema := "m_" + hex.EncodeToString(id[:])
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestListIsOrderedAndComplete(t *testing.T) {
	all, err := list()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || all[0].version != 1 {
		t.Fatalf("migrations = %+v", all)
	}
	for i := 1; i < len(all); i++ {
		if all[i].version <= all[i-1].version {
			t.Fatalf("out of order: %s after %s", all[i].name, all[i-1].name)
		}
	}
}

func TestUpAppliesOnceAndRecordsTheVersion(t *testing.T) {
	pool := freshSchema(t)
	ctx := context.Background()
	all, _ := list()

	n, err := Up(ctx, pool)
	if err != nil || n != len(all) {
		t.Fatalf("first Up: applied %d of %d, err %v", n, len(all), err)
	}
	var version int64
	var dirty bool
	if err := pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != all[len(all)-1].version || dirty {
		t.Fatalf("version %d dirty %v, want %d clean", version, dirty, all[len(all)-1].version)
	}
	if n, err := Up(ctx, pool); err != nil || n != 0 {
		t.Fatalf("second Up: applied %d, err %v", n, err)
	}
}

func TestUpRefusesADirtyDatabase(t *testing.T) {
	pool := freshSchema(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
		INSERT INTO schema_migrations VALUES (23, true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(ctx, pool); err == nil {
		t.Fatal("Up ran on a dirty database")
	}
}

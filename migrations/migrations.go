// Package migrations embeds the schema, so a release brings its own database up to
// date on start: the box runs one binary and has no migrate CLI. The bookkeeping is
// golang-migrate's (one schema_migrations row: the latest version and a dirty flag),
// so `make migrate-up` and Up can take turns on the same database.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.up.sql
var files embed.FS

// lockID keeps two instances from migrating at once.
const lockID = 7413005

type migration struct {
	version int64
	name    string
}

func list() ([]migration, error) {
	names, err := fs.Glob(files, "*.up.sql")
	if err != nil {
		return nil, err
	}
	out := make([]migration, 0, len(names))
	for _, n := range names {
		v, err := strconv.ParseInt(strings.SplitN(n, "_", 2)[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %s: version: %w", n, err)
		}
		out = append(out, migration{version: v, name: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Up applies every migration newer than the database's version, each in its own
// transaction, and returns how many it applied. A dirty database is left alone:
// a person has to look at what half-ran.
func Up(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	all, err := list()
	if err != nil {
		return 0, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return 0, fmt.Errorf("lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`); err != nil {
		return 0, fmt.Errorf("schema_migrations: %w", err)
	}
	var current int64
	var dirty bool
	err = conn.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&current, &dirty)
	switch {
	case err == pgx.ErrNoRows:
	case err != nil:
		return 0, fmt.Errorf("read version: %w", err)
	case dirty:
		return 0, fmt.Errorf("database is dirty at version %d: fix it by hand, then clear the flag", current)
	}

	applied := 0
	for _, m := range all {
		if m.version <= current {
			continue
		}
		sql, err := files.ReadFile(m.name)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)`, m.version)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("apply %s: %w", m.name, err)
		}
		applied++
	}
	return applied, nil
}

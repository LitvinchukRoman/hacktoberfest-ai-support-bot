// Package postgres implements internal/support/ports against Postgres via pgx.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

var (
	_ ports.TicketRepo   = (*Store)(nil)
	_ ports.MessageRepo  = (*Store)(nil)
	_ ports.EventRepo    = (*Store)(nil)
	_ ports.JobQueue     = (*Store)(nil)
	_ ports.ActionRepo   = (*Store)(nil)
	_ ports.DecisionRepo = (*Store)(nil)
	_ ports.TxManager    = (*Store)(nil)
)

// Store implements every ports interface (TicketRepo, MessageRepo,
// EventRepo, JobQueue, TxManager) on one type: callers that only need one
// of them simply declare that interface as their dependency.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type txKey struct{}

// WithinTx runs fn inside one transaction. A nested WithinTx call (fn itself
// calling WithinTx again with the same ctx) joins the already-open
// transaction instead of starting a new one, so handlers can compose
// several repo calls into one atomic commit.
func (s *Store) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	if txFromContext(ctx) != nil {
		return fn(ctx)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func txFromContext(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(txKey{}).(pgx.Tx)
	return tx
}

// querier is the subset of pgx.Tx / pgxpool.Pool every repo method needs;
// q(ctx) picks whichever is active.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Store) q(ctx context.Context) querier {
	if tx := txFromContext(ctx); tx != nil {
		return tx
	}
	return s.pool
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

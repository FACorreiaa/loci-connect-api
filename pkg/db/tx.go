package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TxBeginner begins a pgx transaction. Both *pgxpool.Pool and the per-repository
// pool interfaces satisfy it.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// WithTx runs fn inside a single transaction. It begins the transaction, calls
// fn, and commits. If fn returns an error the transaction is rolled back and the
// error is returned (joined with any rollback error). If fn panics, the
// transaction is rolled back and the panic is re-raised so panics are never
// swallowed.
//
// This centralizes the begin/commit/rollback dance so call sites no longer
// hand-roll defer/recover logic (which previously varied per repository and, in
// SaveInteraction, used a fragile recover()/panic() pattern for rollback).
func WithTx(ctx context.Context, b TxBeginner, fn func(pgx.Tx) error) (err error) {
	tx, err := b.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err = fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rbErr))
		}
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

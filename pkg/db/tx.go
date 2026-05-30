package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TxBeginner begins a pgx transaction with options. Both *pgxpool.Pool and the
// per-repository pool interfaces that expose BeginTx satisfy it.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// SimpleTxBeginner begins a pgx transaction with default options. Repositories
// whose pool interface only exposes Begin satisfy it.
type SimpleTxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// WithTx runs fn inside a single transaction begun with the given options
// (BeginTx). See runTx for the commit/rollback/panic semantics.
//
// This centralizes the begin/commit/rollback dance so call sites no longer
// hand-roll defer/recover logic (which previously varied per repository and, in
// SaveInteraction, used a fragile recover()/panic() pattern for rollback).
func WithTx(ctx context.Context, b TxBeginner, fn func(pgx.Tx) error) error {
	tx, err := b.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	return runTx(ctx, tx, fn)
}

// WithTxBegin is WithTx for pools that only expose Begin (default tx options).
func WithTxBegin(ctx context.Context, b SimpleTxBeginner, fn func(pgx.Tx) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	return runTx(ctx, tx, fn)
}

// runTx calls fn and commits. If fn returns an error the transaction is rolled
// back and the error is returned (joined with any rollback error). If fn panics,
// the transaction is rolled back and the panic is re-raised so panics are never
// swallowed.
func runTx(ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) error) (err error) {
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

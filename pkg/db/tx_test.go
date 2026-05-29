package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func TestWithTx(t *testing.T) {
	t.Run("commits when fn succeeds", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("new mock pool: %v", err)
		}
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{})
		mock.ExpectCommit()

		called := false
		err = WithTx(context.Background(), mock, func(pgx.Tx) error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatalf("WithTx returned error: %v", err)
		}
		if !called {
			t.Fatal("fn was not called")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("rolls back and returns fn error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("new mock pool: %v", err)
		}
		defer mock.Close()

		wantErr := errors.New("boom")
		mock.ExpectBeginTx(pgx.TxOptions{})
		mock.ExpectRollback()

		err = WithTx(context.Background(), mock, func(pgx.Tx) error {
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected wrapped %v, got %v", wantErr, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("wraps begin error and never calls fn", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("new mock pool: %v", err)
		}
		defer mock.Close()

		beginErr := errors.New("no connection")
		mock.ExpectBeginTx(pgx.TxOptions{}).WillReturnError(beginErr)

		called := false
		err = WithTx(context.Background(), mock, func(pgx.Tx) error {
			called = true
			return nil
		})
		if !errors.Is(err, beginErr) {
			t.Fatalf("expected wrapped %v, got %v", beginErr, err)
		}
		if called {
			t.Fatal("fn must not run when begin fails")
		}
	})

	t.Run("rolls back and re-raises panic", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("new mock pool: %v", err)
		}
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{})
		mock.ExpectRollback()

		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		}()

		_ = WithTx(context.Background(), mock, func(pgx.Tx) error {
			panic("kaboom")
		})
	})
}

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
)

func TestPostgresAuthRepository_CreateUser(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	returnedID := uuid.New()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(createUserQuery)).
		WithArgs(pgxmock.AnyArg(), "repo@example.com", "repo", "hashed", "Repo User", "member", true, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(returnedID, now, now))

	repo := NewPostgresAuthRepository(mock)
	user, err := repo.CreateUser(context.Background(), "repo@example.com", "repo", "hashed", "Repo User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID != returnedID {
		t.Fatalf("expected id %s, got %s", returnedID, user.ID)
	}
	if user.Role != "member" || !user.IsActive {
		t.Fatalf("defaults not applied: %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresAuthRepository_GetUserByEmail_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	rows := pgxmock.NewRows([]string{
		"id", "email", "username", "hashed_password", "display_name", "avatar_url", "role",
		"is_active", "email_verified_at", "created_at", "updated_at", "last_login_at",
	})
	mock.ExpectQuery(regexp.QuoteMeta(getUserByEmailQuery)).
		WithArgs("missing@example.com").
		WillReturnRows(rows)

	repo := NewPostgresAuthRepository(mock)
	_, err = repo.GetUserByEmail(context.Background(), "missing@example.com")
	if err == nil || err != common.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresAuthRepository_GetUserSessionByToken_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"id", "user_id", "hashed_refresh_token", "user_agent", "client_ip", "expires_at", "created_at"})
	mock.ExpectQuery(regexp.QuoteMeta(getUserSessionQuery)).
		WithArgs("missing", pgxmock.AnyArg()).
		WillReturnRows(rows)

	repo := NewPostgresAuthRepository(mock)
	_, err = repo.GetUserSessionByToken(context.Background(), "missing")
	if err == nil || err != common.ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresAuthRepository_GetUserTokenByHash_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"token_hash", "user_id", "type", "expires_at", "created_at"})
	mock.ExpectQuery(regexp.QuoteMeta(getUserTokenQuery)).
		WithArgs("hash", "password_reset", pgxmock.AnyArg()).
		WillReturnRows(rows)

	repo := NewPostgresAuthRepository(mock)
	_, err = repo.GetUserTokenByHash(context.Background(), "hash", "password_reset")
	if err == nil || err != common.ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresAuthRepository_VerifyEmail(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta(verifyEmailQuery)).
		WithArgs(pgxmock.AnyArg(), userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	repo := NewPostgresAuthRepository(mock)
	if err := repo.VerifyEmail(context.Background(), userID); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresAuthRepository_CreateUserSession(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	sessionID := uuid.New()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(createSessionQuery)).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "hash", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at"}).
			AddRow(sessionID, now))

	repo := NewPostgresAuthRepository(mock)
	session, err := repo.CreateUserSession(context.Background(), uuid.New(), "hash", "ua", "ip", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	if session.ID != sessionID {
		t.Fatalf("unexpected session id %s", session.ID)
	}
	if session.UserAgent == nil || *session.UserAgent != "ua" {
		t.Fatalf("user agent not stored")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresAuthRepository_GetUserByOAuthIdentity_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	rows := pgxmock.NewRows([]string{
		"id", "email", "username", "hashed_password", "display_name", "avatar_url", "role",
		"is_active", "email_verified_at", "created_at", "updated_at", "last_login_at",
	})
	mock.ExpectQuery(regexp.QuoteMeta(getUserByOAuthQuery)).
		WithArgs("provider", "id").
		WillReturnRows(rows)

	repo := NewPostgresAuthRepository(mock)
	_, err = repo.GetUserByOAuthIdentity(context.Background(), "provider", "id")
	if err == nil || err != common.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// --- Queries used in tests ---

var (
	createUserQuery = `
		INSERT INTO users (id, email, username, hashed_password, display_name, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at
	`
	getUserByEmailQuery = `
		SELECT id, email, username, hashed_password, display_name, avatar_url, role,
		       is_active, email_verified_at, created_at, updated_at, last_login_at
		FROM users
		WHERE email = $1
	`
	getUserSessionQuery = `
		SELECT id, user_id, hashed_refresh_token, user_agent, client_ip, expires_at, created_at
		FROM user_sessions
		WHERE hashed_refresh_token = $1 AND expires_at > $2
	`
	getUserTokenQuery = `
		SELECT token_hash, user_id, type, expires_at, created_at
		FROM user_tokens
		WHERE token_hash = $1 AND type = $2 AND expires_at > $3
	`
	verifyEmailQuery   = `UPDATE users SET email_verified_at = $1 WHERE id = $2`
	createSessionQuery = `
		INSERT INTO user_sessions (id, user_id, hashed_refresh_token, user_agent, client_ip, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`
	getUserByOAuthQuery = `
		SELECT u.id, u.email, u.username, u.hashed_password, u.display_name, u.avatar_url, u.role,
		       u.is_active, u.email_verified_at, u.created_at, u.updated_at, u.last_login_at
		FROM users u
		INNER JOIN user_oauth_identities o ON u.id = o.user_id
		WHERE o.provider_name = $1 AND o.provider_user_id = $2
	`
)

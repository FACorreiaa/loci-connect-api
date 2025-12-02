package repository

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgmock"
	"github.com/jackc/pgproto3/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
)

func TestPostgresAuthRepository_CreateUser(t *testing.T) {
	userID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)

	script := acceptScript(
		expectQuery(createUserQuery),
		sendRowDescription(
			field("id", oidUUID),
			field("created_at", oidTimestamptz),
			field("updated_at", oidTimestamptz),
		),
		sendDataRow(userID.String(), formatTime(now), formatTime(now)),
		sendCommandComplete("INSERT 0 1"),
		sendReady(),
	)

	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
	user, err := repo.CreateUser(context.Background(), "repo@example.com", "repo", "hashed", "Repo User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID != userID {
		t.Fatalf("expected id %s, got %s", userID, user.ID)
	}
	if user.Email != "repo@example.com" || user.Username != "repo" {
		t.Fatalf("user fields not set as expected: %+v", user)
	}
	if user.Role != "member" || !user.IsActive {
		t.Fatalf("defaults not applied: %+v", user)
	}
}

func TestPostgresAuthRepository_GetUserByEmail_NotFound(t *testing.T) {
	script := acceptScript(
		expectQuery(getUserByEmailQuery),
		sendRowDescription(userFieldsRowDesc()...),
		sendCommandComplete("SELECT 0"),
		sendReady(),
	)
	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
	_, err := repo.GetUserByEmail(context.Background(), "missing@example.com")
	if err == nil || !errors.Is(err, common.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestPostgresAuthRepository_GetUserSessionByToken_NotFound(t *testing.T) {
	script := acceptScript(
		expectQuery(getUserSessionQuery),
		sendRowDescription(
			field("id", oidUUID),
			field("user_id", oidUUID),
			field("hashed_refresh_token", oidText),
			field("user_agent", oidText),
			field("client_ip", oidText),
			field("expires_at", oidTimestamptz),
			field("created_at", oidTimestamptz),
		),
		sendCommandComplete("SELECT 0"),
		sendReady(),
	)
	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
	_, err := repo.GetUserSessionByToken(context.Background(), "missing")
	if err == nil || !errors.Is(err, common.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestPostgresAuthRepository_GetUserTokenByHash_NotFound(t *testing.T) {
	script := acceptScript(
		expectQuery(getUserTokenQuery),
		sendRowDescription(
			field("token_hash", oidText),
			field("user_id", oidUUID),
			field("type", oidText),
			field("expires_at", oidTimestamptz),
			field("created_at", oidTimestamptz),
		),
		sendCommandComplete("SELECT 0"),
		sendReady(),
	)
	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
	_, err := repo.GetUserTokenByHash(context.Background(), "hash", "password_reset")
	if err == nil || !errors.Is(err, common.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestPostgresAuthRepository_VerifyEmail(t *testing.T) {
	script := acceptScript(
		expectQuery(verifyEmailQuery),
		sendCommandComplete("UPDATE 1"),
		sendReady(),
	)
	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
	if err := repo.VerifyEmail(context.Background(), uuid.New()); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
}

func TestPostgresAuthRepository_CreateUserSession(t *testing.T) {
	sessionID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	script := acceptScript(
		expectQuery(createSessionQuery),
		sendRowDescription(field("id", oidUUID), field("created_at", oidTimestamptz)),
		sendDataRow(sessionID.String(), formatTime(now)),
		sendCommandComplete("INSERT 0 1"),
		sendReady(),
	)
	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
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
}

func TestPostgresAuthRepository_GetUserByOAuthIdentity_NotFound(t *testing.T) {
	script := acceptScript(
		expectQuery(getUserByOAuthQuery),
		sendRowDescription(userFieldsRowDesc()...),
		sendCommandComplete("SELECT 0"),
		sendReady(),
	)
	db, cleanup := startMockDB(t, script)
	defer cleanup()

	repo := NewPostgresAuthRepository(db)
	_, err := repo.GetUserByOAuthIdentity(context.Background(), "provider", "id")
	if err == nil || !errors.Is(err, common.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// --- Helpers ---

const (
	oidUUID        = 2950
	oidText        = 25
	oidBool        = 16
	oidTimestamptz = 1184
)

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

func acceptScript(steps ...pgmock.Step) *pgmock.Script {
	base := []pgmock.Step{
		pgmock.ExpectAnyMessage(&pgproto3.StartupMessage{ProtocolVersion: pgproto3.ProtocolVersionNumber, Parameters: map[string]string{}}),
		pgmock.SendMessage(&pgproto3.AuthenticationOk{}),
		pgmock.SendMessage(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"}),
		pgmock.SendMessage(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"}),
		pgmock.SendMessage(&pgproto3.ParameterStatus{Name: "server_version", Value: "14"}),
		pgmock.SendMessage(&pgproto3.BackendKeyData{ProcessID: 0, SecretKey: 0}),
		pgmock.SendMessage(&pgproto3.ReadyForQuery{TxStatus: 'I'}),
	}
	s := &pgmock.Script{Steps: append(base, steps...)}
	s.Steps = append(s.Steps, pgmock.WaitForClose())
	return s
}

func expectQuery(q string) pgmock.Step {
	_ = q
	return pgmock.ExpectAnyMessage(&pgproto3.Query{})
}

func sendRowDescription(fields ...pgproto3.FieldDescription) pgmock.Step {
	return pgmock.SendMessage(&pgproto3.RowDescription{Fields: fields})
}

func sendDataRow(values ...string) pgmock.Step {
	row := make([][]byte, len(values))
	for i, v := range values {
		if v == "" {
			row[i] = nil
		} else {
			row[i] = []byte(v)
		}
	}
	return pgmock.SendMessage(&pgproto3.DataRow{Values: row})
}

func sendCommandComplete(tag string) pgmock.Step {
	return pgmock.SendMessage(&pgproto3.CommandComplete{CommandTag: []byte(tag)})
}

func sendReady() pgmock.Step {
	return pgmock.SendMessage(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.999999999Z07:00")
}

func field(name string, oid uint32) pgproto3.FieldDescription {
	return pgproto3.FieldDescription{
		Name:        []byte(name),
		DataTypeOID: oid,
		Format:      0,
	}
}

func userFieldsRowDesc() []pgproto3.FieldDescription {
	return []pgproto3.FieldDescription{
		field("id", oidUUID),
		field("email", oidText),
		field("username", oidText),
		field("hashed_password", oidText),
		field("display_name", oidText),
		field("avatar_url", oidText),
		field("role", oidText),
		field("is_active", oidBool),
		field("email_verified_at", oidTimestamptz),
		field("created_at", oidTimestamptz),
		field("updated_at", oidTimestamptz),
		field("last_login_at", oidTimestamptz),
	}
}

func startMockDB(t *testing.T, script *pgmock.Script) (*sql.DB, func()) {
	t.Helper()

	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go func() {
		defer close(serverErr)
		defer serverConn.Close()
		if err := serverConn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			serverErr <- err
			return
		}
		backend := pgproto3.NewBackend(pgproto3.NewChunkReader(serverConn), serverConn)
		serverErr <- script.Run(backend)
	}()

	cfg, err := pgx.ParseConfig("user=postgres host=localhost dbname=postgres sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.DialFunc = func(_ context.Context, _, _ string) (net.Conn, error) {
		return clientConn, nil
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["standard_conforming_strings"] = "on"
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	cleanup := func() {
		_ = db.Close()
		_ = clientConn.Close()
		if err := <-serverErr; err != nil {
			t.Fatalf("server error: %v", err)
		}
	}

	return db, cleanup
}

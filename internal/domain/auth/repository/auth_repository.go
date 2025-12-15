package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
)

// PgxPool abstracts the subset of pgxpool.Pool used by the repository to allow mocking in tests.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// PostgresAuthRepository handles database operations for authentication
type PostgresAuthRepository struct {
	pgpool PgxPool
}

// NewAuthRepository creates a new service repository
func NewPostgresAuthRepository(pgpool PgxPool) *PostgresAuthRepository {
	return &PostgresAuthRepository{pgpool: pgpool}
}

type userInsertRow struct {
	ID        uuid.UUID `db:"id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

type userSessionInsertRow struct {
	ID        uuid.UUID `db:"id"`
	CreatedAt time.Time `db:"created_at"`
}

// CreateUser creates a new user
func (r *PostgresAuthRepository) CreateUser(ctx context.Context, email, username, hashedPassword, displayName string) (*User, error) {
	user := &User{
		ID:             uuid.New(),
		Email:          email,
		Username:       username,
		HashedPassword: hashedPassword,
		DisplayName:    displayName,
		Role:           "member",
		IsActive:       true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	query := `
		INSERT INTO users (id, email, username, hashed_password, display_name, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at
	`

	rows, err := r.pgpool.Query(
		ctx, query,
		user.ID, user.Email, user.Username, user.HashedPassword, user.DisplayName,
		user.Role, user.IsActive, user.CreatedAt, user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[userInsertRow])
	if err != nil {
		return nil, err
	}

	user.ID = dbRow.ID
	user.CreatedAt = dbRow.CreatedAt
	user.UpdatedAt = dbRow.UpdatedAt

	return user, nil
}

// GetUserByEmail retrieves a user by email
func (r *PostgresAuthRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, email, username, hashed_password, display_name, avatar_url, role,
		       is_active, email_verified_at, created_at, updated_at, last_login_at
		FROM users
		WHERE email = $1
	`

	rows, err := r.pgpool.Query(ctx, query, email)
	if err != nil {
		return nil, err
	}

	user, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// GetUserByID retrieves a user by ID
func (r *PostgresAuthRepository) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	query := `
		SELECT id, email, username, hashed_password, display_name, avatar_url, role,
		       is_active, email_verified_at, created_at, updated_at, last_login_at
		FROM users
		WHERE id = $1
	`

	rows, err := r.pgpool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}

	user, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// UpdateLastLogin updates the user's last login timestamp
func (r *PostgresAuthRepository) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET last_login_at = $1 WHERE id = $2`
	_, err := r.pgpool.Exec(ctx, query, time.Now(), userID)
	return err
}

// CreateUserSession creates a new refresh token session
func (r *PostgresAuthRepository) CreateUserSession(ctx context.Context, userID uuid.UUID, hashedRefreshToken, userAgent, clientIP string, expiresAt time.Time) (*UserSession, error) {
	session := &UserSession{
		ID:                 uuid.New(),
		UserID:             userID,
		HashedRefreshToken: hashedRefreshToken,
		UserAgent:          &userAgent,
		ClientIP:           &clientIP,
		ExpiresAt:          expiresAt,
		CreatedAt:          time.Now(),
	}

	query := `
		INSERT INTO user_sessions (id, user_id, hashed_refresh_token, user_agent, client_ip, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`

	rows, err := r.pgpool.Query(
		ctx, query,
		session.ID, session.UserID, session.HashedRefreshToken,
		session.UserAgent, session.ClientIP, session.ExpiresAt, session.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[userSessionInsertRow])
	if err != nil {
		return nil, err
	}

	session.ID = dbRow.ID
	session.CreatedAt = dbRow.CreatedAt

	return session, nil
}

// GetUserSessionByToken retrieves a session by hashed refresh token
func (r *PostgresAuthRepository) GetUserSessionByToken(ctx context.Context, hashedToken string) (*UserSession, error) {
	query := `
		SELECT id, user_id, hashed_refresh_token, user_agent, client_ip, expires_at, created_at
		FROM user_sessions
		WHERE hashed_refresh_token = $1 AND expires_at > $2
	`

	rows, err := r.pgpool.Query(ctx, query, hashedToken, time.Now())
	if err != nil {
		return nil, err
	}

	session, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[UserSession])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, common.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}

	return &session, nil
}

// DeleteUserSession deletes a session
func (r *PostgresAuthRepository) DeleteUserSession(ctx context.Context, hashedToken string) error {
	query := `DELETE FROM user_sessions WHERE hashed_refresh_token = $1`
	_, err := r.pgpool.Exec(ctx, query, hashedToken)
	return err
}

// DeleteAllUserSessions deletes all sessions for a user
func (r *PostgresAuthRepository) DeleteAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM user_sessions WHERE user_id = $1`
	_, err := r.pgpool.Exec(ctx, query, userID)
	return err
}

// CreateUserToken creates a verification or reset token
func (r *PostgresAuthRepository) CreateUserToken(ctx context.Context, userID uuid.UUID, tokenHash, tokenType string, expiresAt time.Time) error {
	query := `
		INSERT INTO user_tokens (token_hash, user_id, type, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.pgpool.Exec(ctx, query, tokenHash, userID, tokenType, expiresAt, time.Now())
	return err
}

// GetUserTokenByHash retrieves a token by hash
func (r *PostgresAuthRepository) GetUserTokenByHash(ctx context.Context, tokenHash, tokenType string) (*UserToken, error) {
	query := `
		SELECT token_hash, user_id, type, expires_at, created_at
		FROM user_tokens
		WHERE token_hash = $1 AND type = $2 AND expires_at > $3
	`

	rows, err := r.pgpool.Query(ctx, query, tokenHash, tokenType, time.Now())
	if err != nil {
		return nil, err
	}

	token, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[UserToken])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, common.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}

	return &token, nil
}

// DeleteUserToken deletes a token
func (r *PostgresAuthRepository) DeleteUserToken(ctx context.Context, tokenHash string) error {
	query := `DELETE FROM user_tokens WHERE token_hash = $1`
	_, err := r.pgpool.Exec(ctx, query, tokenHash)
	return err
}

// VerifyEmail marks a user's email as verified
func (r *PostgresAuthRepository) VerifyEmail(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET email_verified_at = $1 WHERE id = $2`
	_, err := r.pgpool.Exec(ctx, query, time.Now(), userID)
	return err
}

// UpdatePassword updates a user's password
func (r *PostgresAuthRepository) UpdatePassword(ctx context.Context, userID uuid.UUID, hashedPassword string) error {
	query := `UPDATE users SET hashed_password = $1, updated_at = $2 WHERE id = $3`
	_, err := r.pgpool.Exec(ctx, query, hashedPassword, time.Now(), userID)
	return err
}

// CreateOrUpdateOAuthIdentity creates or updates an OAuth identity
func (r *PostgresAuthRepository) CreateOrUpdateOAuthIdentity(ctx context.Context, providerName, providerUserID string, userID uuid.UUID, accessToken, refreshToken *string) error {
	query := `
		INSERT INTO user_oauth_identities (provider_name, provider_user_id, user_id, provider_access_token, provider_refresh_token, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider_name, provider_user_id)
		DO UPDATE SET
			provider_access_token = EXCLUDED.provider_access_token,
			provider_refresh_token = EXCLUDED.provider_refresh_token,
			updated_at = EXCLUDED.updated_at
	`

	now := time.Now()
	_, err := r.pgpool.Exec(ctx, query, providerName, providerUserID, userID, accessToken, refreshToken, now, now)
	return err
}

// GetUserByOAuthIdentity retrieves a user by OAuth provider identity
func (r *PostgresAuthRepository) GetUserByOAuthIdentity(ctx context.Context, providerName, providerUserID string) (*User, error) {
	query := `
		SELECT u.id, u.email, u.username, u.hashed_password, u.display_name, u.avatar_url, u.role,
		       u.is_active, u.email_verified_at, u.created_at, u.updated_at, u.last_login_at
		FROM users u
		INNER JOIN user_oauth_identities o ON u.id = o.user_id
		WHERE o.provider_name = $1 AND o.provider_user_id = $2
	`

	rows, err := r.pgpool.Query(ctx, query, providerName, providerUserID)
	if err != nil {
		return nil, err
	}

	user, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// GetUserByPhone retrieves a user by phone number
func (r *PostgresAuthRepository) GetUserByPhone(ctx context.Context, phone string) (*User, error) {
	query := `
		SELECT id, email, username, hashed_password, display_name, avatar_url, role,
		       is_active, email_verified_at, created_at, updated_at, last_login_at
		FROM users
		WHERE phone = $1
	`

	rows, err := r.pgpool.Query(ctx, query, phone)
	if err != nil {
		return nil, err
	}

	user, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// CreateUserWithPhone creates a new user with phone number (no email/password)
func (r *PostgresAuthRepository) CreateUserWithPhone(ctx context.Context, phone, username string) (*User, error) {
	user := &User{
		ID:        uuid.New(),
		Username:  username,
		Role:      "member",
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	query := `
		INSERT INTO users (id, username, phone, role, is_active, phone_verified_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`

	rows, err := r.pgpool.Query(
		ctx, query,
		user.ID, user.Username, phone, user.Role, user.IsActive, time.Now(), user.CreatedAt, user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[userInsertRow])
	if err != nil {
		return nil, err
	}

	user.ID = dbRow.ID
	user.CreatedAt = dbRow.CreatedAt
	user.UpdatedAt = dbRow.UpdatedAt

	return user, nil
}

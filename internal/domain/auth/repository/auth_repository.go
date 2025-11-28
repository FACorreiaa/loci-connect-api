package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
)

// PostgresAuthRepository handles database operations for authentication
type PostgresAuthRepository struct {
	db *sql.DB
}

// NewAuthRepository creates a new service repository
func NewPostgresAuthRepository(db *sql.DB) *PostgresAuthRepository {
	return &PostgresAuthRepository{db: db}
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

	err := r.db.QueryRowContext(
		ctx, query,
		user.ID, user.Email, user.Username, user.HashedPassword, user.DisplayName,
		user.Role, user.IsActive, user.CreatedAt, user.UpdatedAt,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// GetUserByEmail retrieves a user by email
func (r *PostgresAuthRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	user := &User{}
	query := `
		SELECT id, email, username, hashed_password, display_name, avatar_url, role,
		       is_active, email_verified_at, created_at, updated_at, last_login_at
		FROM users
		WHERE email = $1
	`

	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.Email, &user.Username, &user.HashedPassword, &user.DisplayName,
		&user.AvatarURL, &user.Role, &user.IsActive, &user.EmailVerifiedAt,
		&user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return user, nil
}

// GetUserByID retrieves a user by ID
func (r *PostgresAuthRepository) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	user := &User{}
	query := `
		SELECT id, email, username, hashed_password, display_name, avatar_url, role,
		       is_active, email_verified_at, created_at, updated_at, last_login_at
		FROM users
		WHERE id = $1
	`

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&user.ID, &user.Email, &user.Username, &user.HashedPassword, &user.DisplayName,
		&user.AvatarURL, &user.Role, &user.IsActive, &user.EmailVerifiedAt,
		&user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return user, nil
}

// UpdateLastLogin updates the user's last login timestamp
func (r *PostgresAuthRepository) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET last_login_at = $1 WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, time.Now(), userID)
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

	err := r.db.QueryRowContext(
		ctx, query,
		session.ID, session.UserID, session.HashedRefreshToken,
		session.UserAgent, session.ClientIP, session.ExpiresAt, session.CreatedAt,
	).Scan(&session.ID, &session.CreatedAt)
	if err != nil {
		return nil, err
	}

	return session, nil
}

// GetUserSessionByToken retrieves a session by hashed refresh token
func (r *PostgresAuthRepository) GetUserSessionByToken(ctx context.Context, hashedToken string) (*UserSession, error) {
	session := &UserSession{}
	query := `
		SELECT id, user_id, hashed_refresh_token, user_agent, client_ip, expires_at, created_at
		FROM user_sessions
		WHERE hashed_refresh_token = $1 AND expires_at > $2
	`

	err := r.db.QueryRowContext(ctx, query, hashedToken, time.Now()).Scan(
		&session.ID, &session.UserID, &session.HashedRefreshToken,
		&session.UserAgent, &session.ClientIP, &session.ExpiresAt, &session.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, common.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}

	return session, nil
}

// DeleteUserSession deletes a session
func (r *PostgresAuthRepository) DeleteUserSession(ctx context.Context, hashedToken string) error {
	query := `DELETE FROM user_sessions WHERE hashed_refresh_token = $1`
	_, err := r.db.ExecContext(ctx, query, hashedToken)
	return err
}

// DeleteAllUserSessions deletes all sessions for a user
func (r *PostgresAuthRepository) DeleteAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM user_sessions WHERE user_id = $1`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}

// CreateUserToken creates a verification or reset token
func (r *PostgresAuthRepository) CreateUserToken(ctx context.Context, userID uuid.UUID, tokenHash, tokenType string, expiresAt time.Time) error {
	query := `
		INSERT INTO user_tokens (token_hash, user_id, type, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.db.ExecContext(ctx, query, tokenHash, userID, tokenType, expiresAt, time.Now())
	return err
}

// GetUserTokenByHash retrieves a token by hash
func (r *PostgresAuthRepository) GetUserTokenByHash(ctx context.Context, tokenHash, tokenType string) (*UserToken, error) {
	token := &UserToken{}
	query := `
		SELECT token_hash, user_id, type, expires_at, created_at
		FROM user_tokens
		WHERE token_hash = $1 AND type = $2 AND expires_at > $3
	`

	err := r.db.QueryRowContext(ctx, query, tokenHash, tokenType, time.Now()).Scan(
		&token.TokenHash, &token.UserID, &token.Type, &token.ExpiresAt, &token.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, common.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}

	return token, nil
}

// DeleteUserToken deletes a token
func (r *PostgresAuthRepository) DeleteUserToken(ctx context.Context, tokenHash string) error {
	query := `DELETE FROM user_tokens WHERE token_hash = $1`
	_, err := r.db.ExecContext(ctx, query, tokenHash)
	return err
}

// VerifyEmail marks a user's email as verified
func (r *PostgresAuthRepository) VerifyEmail(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET email_verified_at = $1 WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, time.Now(), userID)
	return err
}

// UpdatePassword updates a user's password
func (r *PostgresAuthRepository) UpdatePassword(ctx context.Context, userID uuid.UUID, hashedPassword string) error {
	query := `UPDATE users SET hashed_password = $1, updated_at = $2 WHERE id = $3`
	_, err := r.db.ExecContext(ctx, query, hashedPassword, time.Now(), userID)
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
	_, err := r.db.ExecContext(ctx, query, providerName, providerUserID, userID, accessToken, refreshToken, now, now)
	return err
}

// GetUserByOAuthIdentity retrieves a user by OAuth provider identity
func (r *PostgresAuthRepository) GetUserByOAuthIdentity(ctx context.Context, providerName, providerUserID string) (*User, error) {
	user := &User{}
	query := `
		SELECT u.id, u.email, u.username, u.hashed_password, u.display_name, u.avatar_url, u.role,
		       u.is_active, u.email_verified_at, u.created_at, u.updated_at, u.last_login_at
		FROM users u
		INNER JOIN user_oauth_identities o ON u.id = o.user_id
		WHERE o.provider_name = $1 AND o.provider_user_id = $2
	`

	err := r.db.QueryRowContext(ctx, query, providerName, providerUserID).Scan(
		&user.ID, &user.Email, &user.Username, &user.HashedPassword, &user.DisplayName,
		&user.AvatarURL, &user.Role, &user.IsActive, &user.EmailVerifiedAt,
		&user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, common.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	return user, nil
}

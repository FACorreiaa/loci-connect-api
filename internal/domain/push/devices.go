// Package push delivers "your search is done" to the devices a person has
// registered. Web push (VAPID) only for now; APNs rows are stored for the
// iOS app but not sent to.
package push

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Device struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Platform string
	Endpoint string
	P256dh   string
	Auth     string
}

type DeviceStore interface {
	Upsert(ctx context.Context, userID uuid.UUID, platform, endpoint, p256dh, auth, userAgent string) error
	Remove(ctx context.Context, userID uuid.UUID, endpoint string) error
	RemoveEndpoint(ctx context.Context, endpoint string) error
	ForUser(ctx context.Context, userID uuid.UUID, platform string) ([]Device, error)
}

type PostgresDeviceStore struct{ pool *pgxpool.Pool }

func NewPostgresDeviceStore(pool *pgxpool.Pool) *PostgresDeviceStore {
	return &PostgresDeviceStore{pool: pool}
}

func (s *PostgresDeviceStore) Upsert(ctx context.Context, userID uuid.UUID, platform, endpoint, p256dh, auth, userAgent string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO push_devices (user_id, platform, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (endpoint) DO UPDATE SET
			user_id = EXCLUDED.user_id, platform = EXCLUDED.platform,
			p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
			user_agent = EXCLUDED.user_agent, last_seen_at = NOW()
		WHERE push_devices.platform = EXCLUDED.platform`,
		userID, platform, endpoint, p256dh, auth, userAgent)
	if err != nil {
		return fmt.Errorf("upsert push device: %w", err)
	}
	return nil
}

func (s *PostgresDeviceStore) Remove(ctx context.Context, userID uuid.UUID, endpoint string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE user_id = $1 AND endpoint = $2`, userID, endpoint); err != nil {
		return fmt.Errorf("remove push device: %w", err)
	}
	return nil
}

func (s *PostgresDeviceStore) RemoveEndpoint(ctx context.Context, endpoint string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE endpoint = $1`, endpoint); err != nil {
		return fmt.Errorf("remove push endpoint: %w", err)
	}
	return nil
}

func (s *PostgresDeviceStore) ForUser(ctx context.Context, userID uuid.UUID, platform string) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, platform, endpoint, p256dh, auth
		FROM push_devices WHERE user_id = $1 AND platform = $2`, userID, platform)
	if err != nil {
		return nil, fmt.Errorf("list push devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.Endpoint, &d.P256dh, &d.Auth); err != nil {
			return nil, fmt.Errorf("scan push device: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

package store

import (
	"context"
	"fmt"
)

// Keys of the settings table.
const (
	SettingOwnerPasswordHash   = "owner_password_hash"
	SettingOwnerTOTPSecret     = "owner_totp_secret"
	SettingGitHubAppID         = "github_app_id"
	SettingGitHubAppSlug       = "github_app_slug"
	SettingGitHubWebhookSecret = "github_webhook_secret"
)

// Settings returns all rows of the settings table.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// InsertSettings writes all values in one transaction and fails with
// ErrConflict, writing nothing, if any key already exists.
func (s *Store) InsertSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for k, v := range values {
		tag, err := tx.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, k, v)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: setting %s already exists", ErrConflict, k)
		}
	}
	return tx.Commit(ctx)
}

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kompu-backend/internal/model"
)

// SSORepository stores single-use browser-SSO flows and reads Kompu
// membership from the Supabase database shared with sso-balai.
// It never writes users, sessions or memberships: sso-balai owns them.
type SSORepository struct {
	pool *pgxpool.Pool
}

func NewSSORepository(pool *pgxpool.Pool) *SSORepository {
	return &SSORepository{pool: pool}
}

// CreateFlow stores only the SHA-256 of the state.
func (r *SSORepository) CreateFlow(ctx context.Context, stateHash []byte, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO public.kompu_sso_flows (state_hash, expires_at) VALUES ($1, $2)`,
		stateHash, expiresAt)
	if err != nil {
		return fmt.Errorf("create sso flow: %w", err)
	}
	return nil
}

// ConsumeFlow marks the flow used, atomically and once. It reports false for an
// unknown, expired or already used state.
func (r *SSORepository) ConsumeFlow(ctx context.Context, stateHash []byte) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE public.kompu_sso_flows SET used_at = now()
		WHERE state_hash = $1 AND used_at IS NULL AND expires_at > now()`, stateHash)
	if err != nil {
		return false, fmt.Errorf("consume sso flow: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Member loads the user and their Kompu membership. It returns nil when the
// user has no live membership row for an active "kompu" application.
// SessionValid tells whether auth.sessions still holds sessionID for this user.
func (r *SSORepository) Member(ctx context.Context, appUserID, sessionID string) (*model.Member, error) {
	var m model.Member
	err := r.pool.QueryRow(ctx, `
		SELECT u.id::text, u.email, COALESCE(u.full_name, ''), m.role, m.status,
		       EXISTS (SELECT 1 FROM auth.sessions s
		               WHERE s.id::text = $2 AND s.user_id = u.auth_user_id)
		FROM public.app_users u
		JOIN public.app_memberships m ON m.app_user_id = u.id AND m.deleted_at IS NULL
		JOIN public.applications a ON a.id = m.application_id AND a.is_active
		WHERE u.id::text = $1 AND u.deleted_at IS NULL AND a.code = $3`,
		appUserID, sessionID, model.ApplicationCode,
	).Scan(&m.ID, &m.Email, &m.FullName, &m.Role, &m.Status, &m.SessionValid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load kompu member: %w", err)
	}
	return &m, nil
}

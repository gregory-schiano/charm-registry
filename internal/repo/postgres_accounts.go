package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/gschiano/charm-registry/internal/core"
)

// EnsureAccount is part of the [Repository] interface.
func (p *Postgres) EnsureAccount(ctx context.Context, account core.Account) (core.Account, error) {
	query := `
		INSERT INTO accounts (id, subject, username, display_name, email, validation, is_admin, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (subject) DO UPDATE SET
			username = EXCLUDED.username,
			display_name = EXCLUDED.display_name,
			email = EXCLUDED.email,
			validation = EXCLUDED.validation,
			is_admin = EXCLUDED.is_admin
		RETURNING id, subject, username, display_name, email, validation, is_admin, created_at
	`
	var stored core.Account
	err := p.db.QueryRow(ctx, query,
		account.ID,
		account.Subject,
		account.Username,
		account.DisplayName,
		account.Email,
		account.Validation,
		account.IsAdmin,
		account.CreatedAt,
	).Scan(
		&stored.ID,
		&stored.Subject,
		&stored.Username,
		&stored.DisplayName,
		&stored.Email,
		&stored.Validation,
		&stored.IsAdmin,
		&stored.CreatedAt,
	)
	return stored, err
}

// GetAccountByID is part of the [Repository] interface.
func (p *Postgres) GetAccountByID(ctx context.Context, accountID string) (core.Account, error) {
	row := p.db.QueryRow(ctx, `
		SELECT id, subject, username, display_name, email, validation, is_admin, created_at
		FROM accounts WHERE id = $1
	`, accountID)
	var account core.Account
	err := row.Scan(
		&account.ID,
		&account.Subject,
		&account.Username,
		&account.DisplayName,
		&account.Email,
		&account.Validation,
		&account.IsAdmin,
		&account.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Account{}, ErrNotFound
	}
	return account, err
}

// CreateStoreToken is part of the [Repository] interface.
func (p *Postgres) CreateStoreToken(ctx context.Context, token core.StoreToken) error {
	packagesJSON, err := marshalJSON(token.Packages)
	if err != nil {
		return err
	}
	channelsJSON, err := marshalJSON(token.Channels)
	if err != nil {
		return err
	}
	permissionsJSON, err := marshalJSON(token.Permissions)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		INSERT INTO store_tokens (
			session_id, token_hash, account_id, description, packages, channels, permissions,
			valid_since, valid_until, revoked_at, revoked_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`,
		token.SessionID,
		token.TokenHash,
		token.AccountID,
		token.Description,
		packagesJSON,
		channelsJSON,
		permissionsJSON,
		token.ValidSince,
		token.ValidUntil,
		token.RevokedAt,
		token.RevokedBy,
	)
	return err
}

// ListStoreTokens is part of the [Repository] interface.
func (p *Postgres) ListStoreTokens(
	ctx context.Context,
	accountID string,
	includeInactive bool,
) ([]core.StoreToken, error) {
	query := `
		SELECT session_id, token_hash, account_id, description, packages, channels, permissions,
		       valid_since, valid_until, revoked_at, revoked_by
		FROM store_tokens WHERE account_id = $1
	`
	if !includeInactive {
		query += ` AND revoked_at IS NULL AND valid_until > NOW()`
	}
	query += ` ORDER BY valid_since ASC`
	rows, err := p.db.Query(ctx, query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.StoreToken
	for rows.Next() {
		token, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, rows.Err()
}

// RevokeStoreToken is part of the [Repository] interface.
func (p *Postgres) RevokeStoreToken(ctx context.Context, accountID, sessionID, revokedBy string) error {
	tag, err := p.db.Exec(ctx, `
		UPDATE store_tokens
		SET revoked_at = NOW(), revoked_by = $3
		WHERE account_id = $1 AND session_id = $2
	`, accountID, sessionID, revokedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FindStoreTokenByHash is part of the [Repository] interface.
func (p *Postgres) FindStoreTokenByHash(ctx context.Context, hash string) (core.StoreToken, core.Account, error) {
	row := p.db.QueryRow(ctx, `
		SELECT
			t.session_id, t.token_hash, t.account_id, t.description, t.packages, t.channels, t.permissions,
			t.valid_since, t.valid_until, t.revoked_at, t.revoked_by,
			a.id, a.subject, a.username, a.display_name, a.email, a.validation, a.is_admin, a.created_at
		FROM store_tokens t
		JOIN accounts a ON a.id = t.account_id
		WHERE t.token_hash = $1
	`, hash)
	var token core.StoreToken
	var account core.Account
	var packagesJSON []byte
	var channelsJSON []byte
	var permissionsJSON []byte
	err := row.Scan(
		&token.SessionID,
		&token.TokenHash,
		&token.AccountID,
		&token.Description,
		&packagesJSON,
		&channelsJSON,
		&permissionsJSON,
		&token.ValidSince,
		&token.ValidUntil,
		&token.RevokedAt,
		&token.RevokedBy,
		&account.ID,
		&account.Subject,
		&account.Username,
		&account.DisplayName,
		&account.Email,
		&account.Validation,
		&account.IsAdmin,
		&account.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.StoreToken{}, core.Account{}, ErrNotFound
	}
	if err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	if err := unmarshalJSON(packagesJSON, &token.Packages); err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	if err := unmarshalJSON(channelsJSON, &token.Channels); err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	if err := unmarshalJSON(permissionsJSON, &token.Permissions); err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	return token, account, nil
}

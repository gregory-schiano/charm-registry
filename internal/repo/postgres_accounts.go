package repo

import (
	"context"
	"fmt"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// EnsureAccount is part of the [Repository] interface.
func (p *Postgres) EnsureAccount(ctx context.Context, account core.Account) (core.Account, error) {
	stored, err := p.queries().EnsureAccount(ctx, sqlcdb.EnsureAccountParams{
		ID:          account.ID,
		Subject:     account.Subject,
		Username:    account.Username,
		DisplayName: account.DisplayName,
		Email:       account.Email,
		Validation:  account.Validation,
		IsAdmin:     account.IsAdmin,
		CreatedAt:   account.CreatedAt,
	})
	if err != nil {
		return core.Account{}, err
	}
	return accountFromSQLC(stored), nil
}

// GetAccountByID is part of the [Repository] interface.
func (p *Postgres) GetAccountByID(ctx context.Context, accountID string) (core.Account, error) {
	account, err := p.queries().GetAccountByID(ctx, accountID)
	if pgxNotFound(err) {
		return core.Account{}, ErrNotFound
	}
	if err != nil {
		return core.Account{}, err
	}
	return accountFromSQLC(account), nil
}

// CreateStoreToken is part of the [Repository] interface.
func (p *Postgres) CreateStoreToken(ctx context.Context, token core.StoreToken) error {
	packagesJSON, err := rawJSON(token.Packages)
	if err != nil {
		return err
	}
	channelsJSON, err := rawJSON(token.Channels)
	if err != nil {
		return err
	}
	permissionsJSON, err := rawJSON(token.Permissions)
	if err != nil {
		return err
	}
	return p.queries().CreateStoreToken(ctx, sqlcdb.CreateStoreTokenParams{
		SessionID:   token.SessionID,
		TokenHash:   token.TokenHash,
		AccountID:   token.AccountID,
		Description: token.Description,
		Packages:    packagesJSON,
		Channels:    channelsJSON,
		Permissions: permissionsJSON,
		ValidSince:  token.ValidSince,
		ValidUntil:  token.ValidUntil,
		RevokedAt:   timestamptzPtr(token.RevokedAt),
		RevokedBy:   token.RevokedBy,
	})
}

// ListStoreTokens is part of the [Repository] interface.
func (p *Postgres) ListStoreTokens(
	ctx context.Context,
	accountID string,
	includeInactive bool,
) ([]core.StoreToken, error) {
	var (
		rows []sqlcdb.StoreToken
		err  error
	)
	if includeInactive {
		rows, err = p.queries().ListAllStoreTokens(ctx, accountID)
	} else {
		rows, err = p.queries().ListActiveStoreTokens(ctx, accountID)
	}
	if err != nil {
		return nil, err
	}
	out := make([]core.StoreToken, 0, len(rows))
	for _, row := range rows {
		token, err := tokenFromSQLC(row)
		if err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, nil
}

// RevokeStoreToken is part of the [Repository] interface.
func (p *Postgres) RevokeStoreToken(ctx context.Context, accountID, sessionID, revokedBy string) error {
	rowsAffected, err := p.queries().RevokeStoreToken(ctx, sqlcdb.RevokeStoreTokenParams{
		AccountID: accountID,
		SessionID: sessionID,
		RevokedBy: &revokedBy,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// FindStoreTokenByHash is part of the [Repository] interface.
func (p *Postgres) FindStoreTokenByHash(ctx context.Context, hash string) (core.StoreToken, core.Account, error) {
	row, err := p.queries().FindStoreTokenByHash(ctx, hash)
	if pgxNotFound(err) {
		return core.StoreToken{}, core.Account{}, ErrNotFound
	}
	if err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	token, account, err := tokenAndAccountFromSQLC(row)
	if err != nil {
		return core.StoreToken{}, core.Account{}, fmt.Errorf("decode store token row: %w", err)
	}
	return token, account, nil
}

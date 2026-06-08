package repo

import (
	"context"
	"fmt"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

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
		SessionID:       token.SessionID,
		TokenHash:       token.TokenHash,
		TokenPrefix:     &token.TokenPrefix,
		TokenHashScheme: token.HashScheme,
		AccountID:       token.AccountID,
		Description:     token.Description,
		Packages:        packagesJSON,
		Channels:        channelsJSON,
		Permissions:     permissionsJSON,
		ValidSince:      token.ValidSince,
		ValidUntil:      token.ValidUntil,
		RevokedAt:       timestamptzPtr(token.RevokedAt),
		RevokedBy:       token.RevokedBy,
	})
}

func (p *Postgres) ListStoreTokens(
	ctx context.Context,
	accountID string,
	includeInactive bool,
) ([]core.StoreToken, error) {
	var (
		rows []sqlcdb.ListAllStoreTokensRow
		err  error
	)
	if includeInactive {
		rows, err = p.queries().ListAllStoreTokens(ctx, accountID)
	} else {
		activeRows, activeErr := p.queries().ListActiveStoreTokens(ctx, accountID)
		// Convert active rows to all rows type for uniform processing.
		if activeErr != nil {
			return nil, activeErr
		}
		rows = make([]sqlcdb.ListAllStoreTokensRow, len(activeRows))
		for i, r := range activeRows {
			rows[i] = sqlcdb.ListAllStoreTokensRow{
				SessionID:       r.SessionID,
				TokenHash:       r.TokenHash,
				TokenPrefix:     r.TokenPrefix,
				TokenHashScheme: r.TokenHashScheme,
				AccountID:       r.AccountID,
				Description:     r.Description,
				Packages:        r.Packages,
				Channels:        r.Channels,
				Permissions:     r.Permissions,
				ValidSince:      r.ValidSince,
				ValidUntil:      r.ValidUntil,
				RevokedAt:       r.RevokedAt,
				RevokedBy:       r.RevokedBy,
			}
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]core.StoreToken, 0, len(rows))
	for _, row := range rows {
		token, err := tokenFromSQLC(sqlcdb.StoreToken{
			SessionID:       row.SessionID,
			TokenHash:       row.TokenHash,
			TokenPrefix:     row.TokenPrefix,
			TokenHashScheme: row.TokenHashScheme,
			AccountID:       row.AccountID,
			Description:     row.Description,
			Packages:        row.Packages,
			Channels:        row.Channels,
			Permissions:     row.Permissions,
			ValidSince:      row.ValidSince,
			ValidUntil:      row.ValidUntil,
			RevokedAt:       row.RevokedAt,
			RevokedBy:       row.RevokedBy,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, nil
}

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

func (p *Postgres) FindStoreTokenByPrefix(ctx context.Context, prefix string) (core.StoreToken, core.Account, error) {
	row, err := p.queries().FindStoreTokenByPrefix(ctx, &prefix)
	if pgxNotFound(err) {
		return core.StoreToken{}, core.Account{}, ErrNotFound
	}
	if err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	token, account, err := tokenAndAccountFromPrefixRow(row)
	if err != nil {
		return core.StoreToken{}, core.Account{}, fmt.Errorf("decode store token row: %w", err)
	}
	return token, account, nil
}

func (p *Postgres) UpdateTokenHashScheme(ctx context.Context, sessionID, hash, prefix, scheme string) error {
	return p.queries().UpdateTokenHashScheme(ctx, sqlcdb.UpdateTokenHashSchemeParams{
		SessionID:       sessionID,
		TokenHash:       hash,
		TokenPrefix:     &prefix,
		TokenHashScheme: scheme,
	})
}

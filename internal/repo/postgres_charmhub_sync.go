package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

// CreateCharmhubSyncRule is part of the [Repository] interface.
func (p *Postgres) CreateCharmhubSyncRule(ctx context.Context, rule core.CharmhubSyncRule) error {
	err := p.queries().CreateCharmhubSyncRule(ctx, sqlcdb.CreateCharmhubSyncRuleParams{
		PackageName:        rule.PackageName,
		Track:              rule.Track,
		CreatedByAccountID: rule.CreatedByAccountID,
		CreatedAt:          rule.CreatedAt,
		UpdatedAt:          rule.UpdatedAt,
		LastSyncStatus:     rule.LastSyncStatus,
		LastSyncStartedAt:  timestamptzPtr(rule.LastSyncStartedAt),
		LastSyncFinishedAt: timestamptzPtr(rule.LastSyncFinishedAt),
		LastSyncError:      rule.LastSyncError,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return err
	}
	return nil
}

// DeleteCharmhubSyncRule is part of the [Repository] interface.
func (p *Postgres) DeleteCharmhubSyncRule(ctx context.Context, packageName, track string) error {
	rowsAffected, err := p.queries().DeleteCharmhubSyncRule(ctx, sqlcdb.DeleteCharmhubSyncRuleParams{
		PackageName: packageName,
		Track:       track,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListCharmhubSyncRules is part of the [Repository] interface.
func (p *Postgres) ListCharmhubSyncRules(ctx context.Context) ([]core.CharmhubSyncRule, error) {
	rows, err := p.queries().ListCharmhubSyncRules(ctx)
	if err != nil {
		return nil, err
	}
	rules := make([]core.CharmhubSyncRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, charmhubSyncRuleFromSQLC(row))
	}
	return rules, nil
}

// ListCharmhubSyncRulesByPackageName is part of the [Repository] interface.
func (p *Postgres) ListCharmhubSyncRulesByPackageName(
	ctx context.Context,
	packageName string,
) ([]core.CharmhubSyncRule, error) {
	rows, err := p.queries().ListCharmhubSyncRulesByPackageName(ctx, packageName)
	if err != nil {
		return nil, err
	}
	rules := make([]core.CharmhubSyncRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, charmhubSyncRuleFromSQLC(row))
	}
	return rules, nil
}

// UpdateCharmhubSyncRule is part of the [Repository] interface.
func (p *Postgres) UpdateCharmhubSyncRule(ctx context.Context, rule core.CharmhubSyncRule) error {
	rowsAffected, err := p.queries().UpdateCharmhubSyncRule(ctx, sqlcdb.UpdateCharmhubSyncRuleParams{
		PackageName:        rule.PackageName,
		Track:              rule.Track,
		UpdatedAt:          rule.UpdatedAt,
		LastSyncStatus:     rule.LastSyncStatus,
		LastSyncStartedAt:  timestamptzPtr(rule.LastSyncStartedAt),
		LastSyncFinishedAt: timestamptzPtr(rule.LastSyncFinishedAt),
		LastSyncError:      rule.LastSyncError,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

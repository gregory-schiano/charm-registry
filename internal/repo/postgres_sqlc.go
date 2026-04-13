package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gschiano/charm-registry/internal/core"
	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)

func (p *Postgres) queries() *sqlcdb.Queries {
	return sqlcdb.New(p.db)
}

func timestamptzPtr(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func int32Ptr(value *int) *int32 {
	if value == nil {
		return nil
	}
	out := int32(*value)
	return &out
}

func nullInt64(robot *core.RobotCredential) *int64 {
	if robot == nil || robot.ID == 0 {
		return nil
	}
	return &robot.ID
}

func robotUsername(robot *core.RobotCredential) string {
	if robot == nil {
		return ""
	}
	return robot.Username
}

func robotSecret(robot *core.RobotCredential) string {
	if robot == nil {
		return ""
	}
	return robot.EncryptedSecret
}

func fromInt32Ptr(value *int32) *int {
	if value == nil {
		return nil
	}
	out := int(*value)
	return &out
}

func accountFromSQLC(item sqlcdb.Account) core.Account {
	return core.Account{
		ID:          item.ID,
		Subject:     item.Subject,
		Username:    item.Username,
		DisplayName: item.DisplayName,
		Email:       item.Email,
		Validation:  item.Validation,
		IsAdmin:     item.IsAdmin,
		CreatedAt:   item.CreatedAt,
	}
}

func releaseFromSQLC(item sqlcdb.Release) (core.Release, error) {
	release := core.Release{
		ID:          item.ID,
		PackageID:   item.PackageID,
		Channel:     item.Channel,
		Revision:    int(item.Revision),
		When:        item.WhenCreated,
		Progressive: item.Progressive,
	}
	if item.ExpirationDate.Valid {
		release.ExpirationDate = &item.ExpirationDate.Time
	}
	if string(item.Base) != "null" && len(item.Base) != 0 {
		var base core.Base
		if err := unmarshalJSON(item.Base, &base); err != nil {
			return core.Release{}, err
		}
		release.Base = &base
	}
	if err := unmarshalJSON(item.Resources, &release.Resources); err != nil {
		return core.Release{}, err
	}
	return release, nil
}

func uploadFromSQLC(item sqlcdb.Upload) (core.Upload, error) {
	upload := core.Upload{
		ID:        item.ID,
		Filename:  item.Filename,
		ObjectKey: item.ObjectKey,
		Size:      item.Size,
		SHA256:    item.Sha256,
		SHA384:    item.Sha384,
		Status:    item.Status,
		Kind:      item.Kind,
		CreatedAt: item.CreatedAt,
		Revision:  fromInt32Ptr(item.Revision),
	}
	if item.ApprovedAt.Valid {
		upload.ApprovedAt = &item.ApprovedAt.Time
	}
	if err := unmarshalJSON(item.Errors, &upload.Errors); err != nil {
		return core.Upload{}, fmt.Errorf("unmarshal upload errors: %w", err)
	}
	return upload, nil
}

func revisionFromSQLC(item sqlcdb.Revision) (core.Revision, error) {
	revision := core.Revision{
		ID:           item.ID,
		PackageID:    item.PackageID,
		Revision:     int(item.Revision),
		Version:      item.Version,
		Status:       item.Status,
		CreatedAt:    item.CreatedAt,
		CreatedBy:    item.CreatedBy,
		Size:         item.Size,
		SHA256:       item.Sha256,
		SHA384:       item.Sha384,
		ObjectKey:    item.ObjectKey,
		MetadataYAML: item.MetadataYaml,
		ConfigYAML:   item.ConfigYaml,
		ActionsYAML:  item.ActionsYaml,
		BundleYAML:   item.BundleYaml,
		ReadmeMD:     item.ReadmeMd,
		Subordinate:  item.Subordinate,
	}
	if err := unmarshalJSON(item.Bases, &revision.Bases); err != nil {
		return core.Revision{}, fmt.Errorf("unmarshal revision bases: %w", err)
	}
	if err := unmarshalJSON(item.Attributes, &revision.Attributes); err != nil {
		return core.Revision{}, fmt.Errorf("unmarshal revision attributes: %w", err)
	}
	if err := unmarshalJSON(item.Relations, &revision.Relations); err != nil {
		return core.Revision{}, fmt.Errorf("unmarshal revision relations: %w", err)
	}
	return revision, nil
}

func tokenFromSQLC(item sqlcdb.StoreToken) (core.StoreToken, error) {
	token := core.StoreToken{
		SessionID:   item.SessionID,
		TokenHash:   item.TokenHash,
		AccountID:   item.AccountID,
		Description: item.Description,
		ValidSince:  item.ValidSince,
		ValidUntil:  item.ValidUntil,
		RevokedBy:   item.RevokedBy,
	}
	if item.RevokedAt.Valid {
		token.RevokedAt = &item.RevokedAt.Time
	}
	if err := unmarshalJSON(item.Packages, &token.Packages); err != nil {
		return core.StoreToken{}, err
	}
	if err := unmarshalJSON(item.Channels, &token.Channels); err != nil {
		return core.StoreToken{}, err
	}
	if err := unmarshalJSON(item.Permissions, &token.Permissions); err != nil {
		return core.StoreToken{}, err
	}
	return token, nil
}

func tokenAndAccountFromSQLC(item sqlcdb.FindStoreTokenByHashRow) (core.StoreToken, core.Account, error) {
	token, err := tokenFromSQLC(sqlcdb.StoreToken{
		SessionID:   item.SessionID,
		TokenHash:   item.TokenHash,
		AccountID:   item.AccountID,
		Description: item.Description,
		Packages:    item.Packages,
		Channels:    item.Channels,
		Permissions: item.Permissions,
		ValidSince:  item.ValidSince,
		ValidUntil:  item.ValidUntil,
		RevokedAt:   item.RevokedAt,
		RevokedBy:   item.RevokedBy,
	})
	if err != nil {
		return core.StoreToken{}, core.Account{}, err
	}
	account := core.Account{
		ID:          item.AccID,
		Subject:     item.AccSubject,
		Username:    item.AccUsername,
		DisplayName: item.AccDisplayName,
		Email:       item.AccEmail,
		Validation:  item.AccValidation,
		IsAdmin:     item.AccIsAdmin,
		CreatedAt:   item.AccCreatedAt,
	}
	return token, account, nil
}

func trackFromSQLC(item sqlcdb.ListTracksRow) core.Track {
	return core.Track{
		Name:                       item.Name,
		VersionPattern:             item.VersionPattern,
		AutomaticPhasingPercentage: item.AutomaticPhasingPercentage,
		CreatedAt:                  item.CreatedAt,
	}
}

func robotFromSQLC(id *int64, username, secret string) *core.RobotCredential {
	if id == nil || *id == 0 || username == "" || secret == "" {
		return nil
	}
	return &core.RobotCredential{
		ID:              *id,
		Username:        username,
		EncryptedSecret: secret,
	}
}

func packageFromParts(
	id, name, packageType string,
	private bool,
	status, ownerAccountID string,
	harborProject string,
	harborPushRobotID *int64,
	harborPushRobotName, harborPushRobotSecret string,
	harborPullRobotID *int64,
	harborPullRobotName, harborPullRobotSecret string,
	harborSyncedAt pgtype.Timestamptz,
	authority, contact, defaultTrack, description, summary, title, website *string,
	linksJSON, mediaJSON, guardrailsJSON json.RawMessage,
	createdAt, updatedAt time.Time,
	pubID, pubUsername, pubDisplayName, pubEmail, pubValidation string,
) (core.Package, error) {
	pkg := core.Package{
		ID:              id,
		Name:            name,
		Type:            packageType,
		Private:         private,
		Status:          status,
		OwnerAccountID:  ownerAccountID,
		HarborProject:   harborProject,
		HarborPushRobot: robotFromSQLC(harborPushRobotID, harborPushRobotName, harborPushRobotSecret),
		HarborPullRobot: robotFromSQLC(harborPullRobotID, harborPullRobotName, harborPullRobotSecret),
		Authority:       authority,
		Contact:         contact,
		DefaultTrack:    defaultTrack,
		Description:     description,
		Summary:         summary,
		Title:           title,
		Website:         website,
		Publisher: core.Publisher{
			ID:          pubID,
			Username:    pubUsername,
			DisplayName: pubDisplayName,
			Email:       pubEmail,
			Validation:  pubValidation,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if harborSyncedAt.Valid {
		pkg.HarborSyncedAt = &harborSyncedAt.Time
	}
	if err := unmarshalJSON(linksJSON, &pkg.Links); err != nil {
		return core.Package{}, fmt.Errorf("unmarshal package links: %w", err)
	}
	if err := unmarshalJSON(mediaJSON, &pkg.Media); err != nil {
		return core.Package{}, fmt.Errorf("unmarshal package media: %w", err)
	}
	if err := unmarshalJSON(guardrailsJSON, &pkg.TrackGuardrails); err != nil {
		return core.Package{}, fmt.Errorf("unmarshal package track guardrails: %w", err)
	}
	return pkg, nil
}

func packageFromGetPackageByNameRow(row sqlcdb.GetPackageByNameRow) (core.Package, error) {
	return packageFromParts(
		row.ID, row.Name, row.Type, row.Private, row.Status, row.OwnerAccountID,
		row.HarborProject,
		row.HarborPushRobotID, row.HarborPushRobotName, row.HarborPushRobotSecret,
		row.HarborPullRobotID, row.HarborPullRobotName, row.HarborPullRobotSecret,
		row.HarborSyncedAt,
		row.Authority, row.Contact, row.DefaultTrack, row.Description, row.Summary, row.Title, row.Website,
		row.Links, row.Media, row.TrackGuardrails,
		row.CreatedAt, row.UpdatedAt,
		row.PubID, row.PubUsername, row.PubDisplayName, row.PubEmail, row.PubValidation,
	)
}

func packageFromGetPackageByIDRow(row sqlcdb.GetPackageByIDRow) (core.Package, error) {
	return packageFromParts(
		row.ID, row.Name, row.Type, row.Private, row.Status, row.OwnerAccountID,
		row.HarborProject,
		row.HarborPushRobotID, row.HarborPushRobotName, row.HarborPushRobotSecret,
		row.HarborPullRobotID, row.HarborPullRobotName, row.HarborPullRobotSecret,
		row.HarborSyncedAt,
		row.Authority, row.Contact, row.DefaultTrack, row.Description, row.Summary, row.Title, row.Website,
		row.Links, row.Media, row.TrackGuardrails,
		row.CreatedAt, row.UpdatedAt,
		row.PubID, row.PubUsername, row.PubDisplayName, row.PubEmail, row.PubValidation,
	)
}

func packageFromListPackagesForAccountRow(row sqlcdb.ListPackagesForAccountRow) (core.Package, error) {
	return packageFromParts(
		row.ID, row.Name, row.Type, row.Private, row.Status, row.OwnerAccountID,
		row.HarborProject,
		row.HarborPushRobotID, row.HarborPushRobotName, row.HarborPushRobotSecret,
		row.HarborPullRobotID, row.HarborPullRobotName, row.HarborPullRobotSecret,
		row.HarborSyncedAt,
		row.Authority, row.Contact, row.DefaultTrack, row.Description, row.Summary, row.Title, row.Website,
		row.Links, row.Media, row.TrackGuardrails,
		row.CreatedAt, row.UpdatedAt,
		row.PubID, row.PubUsername, row.PubDisplayName, row.PubEmail, row.PubValidation,
	)
}

func packageFromListPackagesForAccountWithCollaborationsRow(
	row sqlcdb.ListPackagesForAccountWithCollaborationsRow,
) (core.Package, error) {
	return packageFromParts(
		row.ID, row.Name, row.Type, row.Private, row.Status, row.OwnerAccountID,
		row.HarborProject,
		row.HarborPushRobotID, row.HarborPushRobotName, row.HarborPushRobotSecret,
		row.HarborPullRobotID, row.HarborPullRobotName, row.HarborPullRobotSecret,
		row.HarborSyncedAt,
		row.Authority, row.Contact, row.DefaultTrack, row.Description, row.Summary, row.Title, row.Website,
		row.Links, row.Media, row.TrackGuardrails,
		row.CreatedAt, row.UpdatedAt,
		row.PubID, row.PubUsername, row.PubDisplayName, row.PubEmail, row.PubValidation,
	)
}

func packageFromSearchPackagesRow(row sqlcdb.SearchPackagesRow) (core.Package, error) {
	return packageFromParts(
		row.ID, row.Name, row.Type, row.Private, row.Status, row.OwnerAccountID,
		row.HarborProject,
		row.HarborPushRobotID, row.HarborPushRobotName, row.HarborPushRobotSecret,
		row.HarborPullRobotID, row.HarborPullRobotName, row.HarborPullRobotSecret,
		row.HarborSyncedAt,
		row.Authority, row.Contact, row.DefaultTrack, row.Description, row.Summary, row.Title, row.Website,
		row.Links, row.Media, row.TrackGuardrails,
		row.CreatedAt, row.UpdatedAt,
		row.PubID, row.PubUsername, row.PubDisplayName, row.PubEmail, row.PubValidation,
	)
}

func resourceDefinitionFromSQLC(item sqlcdb.ResourceDefinition) core.ResourceDefinition {
	return core.ResourceDefinition{
		ID:          item.ID,
		PackageID:   item.PackageID,
		Name:        item.Name,
		Type:        item.Type,
		Description: item.Description,
		Filename:    item.Filename,
		Optional:    item.Optional,
		CreatedAt:   item.CreatedAt,
	}
}

func resourceRevisionFromSQLC(item sqlcdb.ResourceRevision) (core.ResourceRevision, error) {
	revision := core.ResourceRevision{
		ID:              item.ID,
		ResourceID:      item.ResourceID,
		Name:            item.Name,
		Type:            item.Type,
		Description:     item.Description,
		Filename:        item.Filename,
		Revision:        int(item.Revision),
		CreatedAt:       item.CreatedAt,
		Size:            item.Size,
		SHA256:          item.Sha256,
		SHA384:          item.Sha384,
		SHA512:          item.Sha512,
		SHA3384:         item.Sha3384,
		ObjectKey:       item.ObjectKey,
		PackageRevision: fromInt32Ptr(item.PackageRevision),
		OCIImageDigest:  item.OciImageDigest,
		OCIImageBlob:    item.OciImageBlob,
	}
	if err := unmarshalJSON(item.Bases, &revision.Bases); err != nil {
		return core.ResourceRevision{}, fmt.Errorf("unmarshal resource revision bases: %w", err)
	}
	if err := unmarshalJSON(item.Architectures, &revision.Architectures); err != nil {
		return core.ResourceRevision{}, fmt.Errorf("unmarshal resource revision architectures: %w", err)
	}
	return revision, nil
}

func trackBatchFromSQLC(item sqlcdb.Track) core.Track {
	return core.Track{
		Name:                       item.Name,
		VersionPattern:             item.VersionPattern,
		AutomaticPhasingPercentage: item.AutomaticPhasingPercentage,
		CreatedAt:                  item.CreatedAt,
	}
}

func pgxNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func rawJSON(value any) (json.RawMessage, error) {
	return marshalJSON(value)
}

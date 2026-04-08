package repo

import (
	"encoding/json"
	"fmt"

	"github.com/gschiano/charm-registry/internal/core"
)

func scanToken(row interface{ Scan(dest ...any) error }) (core.StoreToken, error) {
	var token core.StoreToken
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
	)
	if err != nil {
		return core.StoreToken{}, err
	}
	if err := unmarshalJSON(packagesJSON, &token.Packages); err != nil {
		return core.StoreToken{}, fmt.Errorf("unmarshal token packages: %w", err)
	}
	if err := unmarshalJSON(channelsJSON, &token.Channels); err != nil {
		return core.StoreToken{}, fmt.Errorf("unmarshal token channels: %w", err)
	}
	if err := unmarshalJSON(permissionsJSON, &token.Permissions); err != nil {
		return core.StoreToken{}, fmt.Errorf("unmarshal token permissions: %w", err)
	}
	return token, nil
}

func marshalJSON(value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func unmarshalJSON(payload []byte, target any) error {
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	return json.Unmarshal(payload, target)
}

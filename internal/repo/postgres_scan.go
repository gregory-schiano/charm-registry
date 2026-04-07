package repo

import (
	"encoding/json"

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
	unmarshalJSON(packagesJSON, &token.Packages)
	unmarshalJSON(channelsJSON, &token.Channels)
	unmarshalJSON(permissionsJSON, &token.Permissions)
	return token, nil
}

func mustJSON(value any) []byte {
	if value == nil {
		return []byte("null")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func unmarshalJSON(payload []byte, target any) {
	if len(payload) == 0 || string(payload) == "null" {
		return
	}
	_ = json.Unmarshal(payload, target)
}

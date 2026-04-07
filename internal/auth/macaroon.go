package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// WrapInMacaroon wraps a raw store token in a minimal pymacaroons-compatible
// JSON string so that charmcraft's bakery-based login can parse and process it.
//
// The returned string, when json.loads()'d in Python, gives a legacy-format
// bakery Macaroon with no third-party caveats.  bakery.discharge_all() will
// return immediately (nothing to discharge via SSO), and the serialized
// macaroon bundle is then sent by charmcraft to POST /v1/tokens/exchange in
// the "Macaroons" header.  The handler extracts the identifier (the raw token)
// via [ExtractTokenFromMacaroons] to complete the exchange.
func WrapInMacaroon(raw, location string) string {
	// Derive a stable root key from the raw token so the HMAC is reproducible.
	rootKey := sha256.Sum256([]byte(raw))
	h := hmac.New(sha256.New, rootKey[:])
	h.Write([]byte(raw))
	sig := hex.EncodeToString(h.Sum(nil))

	// pymacaroons v1 legacy JSON format (no "m" wrapper key).
	// bakery.Macaroon.from_dict() treats dicts without "m" as legacy v1.
	m := struct {
		Identifier string `json:"identifier"`
		Location   string `json:"location"`
		Signature  string `json:"signature"`
		Caveats    []any  `json:"caveats"`
	}{
		Identifier: raw,
		Location:   location,
		Signature:  sig,
		Caveats:    []any{},
	}
	data, _ := json.Marshal(m)
	return string(data)
}

// ExtractTokenFromMacaroons decodes the "Macaroons" header sent by charmcraft
// to POST /v1/tokens/exchange.  The header is a URL-safe base64-encoded JSON
// array of serialized pymacaroon objects; the first element's "identifier"
// field contains the original raw store token that was issued via
// POST /v1/tokens.
func ExtractTokenFromMacaroons(header string) (string, error) {
	// craft-store uses base64.urlsafe_b64encode (with padding).
	decoded, err := base64.URLEncoding.DecodeString(header)
	if err != nil {
		// Fall back to no-padding variant.
		decoded, err = base64.RawURLEncoding.DecodeString(header)
		if err != nil {
			return "", fmt.Errorf("cannot decode Macaroons header: %w", err)
		}
	}
	var macaroons []map[string]any
	if err := json.Unmarshal(decoded, &macaroons); err != nil {
		return "", fmt.Errorf("cannot parse macaroon JSON array: %w", err)
	}
	if len(macaroons) == 0 {
		return "", fmt.Errorf("empty macaroon array in Macaroons header")
	}
	identifier, ok := macaroons[0]["identifier"].(string)
	if !ok || identifier == "" {
		return "", fmt.Errorf("macaroon missing identifier field")
	}
	return identifier, nil
}

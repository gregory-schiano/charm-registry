package auth

import (
	"encoding/base64"
	"testing"
)

func FuzzExtractTokenFromMacaroons(f *testing.F) {
	validPayload := "[" + WrapInMacaroon("cr_seed", "http://localhost:8080") + "]"
	f.Add(base64.URLEncoding.EncodeToString([]byte(validPayload)))
	f.Add(base64.RawURLEncoding.EncodeToString([]byte(validPayload)))
	f.Add("not-valid-base64!!!")

	f.Fuzz(func(t *testing.T, header string) {
		token, err := ExtractTokenFromMacaroons(header)
		if err != nil {
			return
		}
		if token == "" {
			t.Fatal("expected non-empty token")
		}
	})
}

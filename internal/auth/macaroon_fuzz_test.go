package auth

import (
	"encoding/base64"
	"testing"
)

func FuzzExtractTokenFromMacaroons(f *testing.F) {
	validPayload := "[" + WrapInMacaroon("cr_seed", "http://localhost:8080") + "]"
	f.Add(base64.URLEncoding.EncodeToString([]byte(validPayload)))
	f.Add(base64.RawURLEncoding.EncodeToString([]byte(validPayload)))
	f.Add(base64.RawURLEncoding.EncodeToString([]byte("[]")))
	f.Add(base64.RawURLEncoding.EncodeToString([]byte(`[{}]`)))
	f.Add("not-valid-base64!!!")

	f.Fuzz(func(t *testing.T, header string) {
		token, err := ExtractTokenFromMacaroons(header)
		if err != nil {
			return
		}
		if token == "" {
			t.Fatal("expected non-empty token")
		}

		reencoded := base64.RawURLEncoding.EncodeToString([]byte("[" + WrapInMacaroon(token, "http://localhost:8080") + "]"))
		roundTripToken, roundTripErr := ExtractTokenFromMacaroons(reencoded)
		if roundTripErr != nil {
			t.Fatalf("canonical macaroon parse failed: %v", roundTripErr)
		}
		if roundTripToken != token {
			t.Fatalf("canonical macaroon parse mismatch: got %q, want %q", roundTripToken, token)
		}
	})
}

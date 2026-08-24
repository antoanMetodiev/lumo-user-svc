// svix.go hand-rolls Svix's public webhook-signing scheme
// (https://docs.svix.com/receiving/verifying-payloads/how-manual) instead of
// pulling in the official SDK — the algorithm is small, stable, and
// well-documented, so this avoids depending on an unfamiliar library's exact
// API surface (same call the lumo-api-gateway rewrite made for Clerk's own
// JWKS verification).
package clerkwebhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var errInvalidSignature = errors.New("clerkwebhook: signature verification failed")

// tolerance mirrors the Svix SDK's default timestamp tolerance. Not a
// const so tests can temporarily widen it to isolate the HMAC computation
// itself from the freshness check.
var tolerance = 5 * time.Minute

// verifySignature mirrors Webhook.verify: computes
// HMAC-SHA256(secret, "{svixID}.{svixTimestamp}.{payload}") and checks it
// against every "v1,<base64sig>" entry in svixSignature (Svix sends
// space-separated signatures to support secret rotation — any match is
// valid), after confirming svixTimestamp is within tolerance of now.
func verifySignature(secret string, svixID, svixTimestamp, svixSignature string, payload []byte) error {
	if svixID == "" || svixTimestamp == "" || svixSignature == "" {
		return errInvalidSignature
	}

	ts, err := strconv.ParseInt(svixTimestamp, 10, 64)
	if err != nil {
		return errInvalidSignature
	}
	age := time.Since(time.Unix(ts, 0))
	if age > tolerance || age < -tolerance {
		return errInvalidSignature
	}

	secretBytes, err := decodeSecret(secret)
	if err != nil {
		return errInvalidSignature
	}

	signedContent := svixID + "." + svixTimestamp + "." + string(payload)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(signedContent))
	expected := mac.Sum(nil)

	for _, part := range strings.Fields(svixSignature) {
		version, sig, ok := strings.Cut(part, ",")
		if !ok || version != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(sig)
		if err != nil {
			continue
		}
		if hmac.Equal(expected, got) {
			return nil
		}
	}

	return errInvalidSignature
}

// decodeSecret strips Svix's "whsec_" prefix and base64-decodes the rest
// into the raw HMAC key.
func decodeSecret(secret string) ([]byte, error) {
	secret = strings.TrimPrefix(secret, "whsec_")
	return base64.StdEncoding.DecodeString(secret)
}

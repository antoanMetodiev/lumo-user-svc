package clerkwebhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sign computes a valid "v1,<sig>" value for the given secret/id/timestamp/
// payload, the same way a real Svix webhook sender would.
func sign(t *testing.T, secret, id, timestamp, payload string) string {
	t.Helper()
	secretBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		t.Fatalf("decoding secret: %v", err)
	}
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(id + "." + timestamp + "." + payload))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// TestVerifySignatureKnownVector checks the algorithm against Svix's own
// published documentation example
// (https://docs.svix.com/receiving/verifying-payloads/how-manual), not just
// a signature this same code generated — the point is to catch a subtly
// wrong implementation of the *published* scheme, not just internal
// self-consistency.
func TestVerifySignatureKnownVector(t *testing.T) {
	const (
		secret    = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
		id        = "msg_p5jXN8AQM9LWM0D4loKWxJek"
		timestamp = "1614265330"
		payload   = `{"test": 2432232314}`
		wantSig   = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
	)

	// This fixed timestamp is long outside the 5-minute freshness window, so
	// bypass that check here — it's exercised separately below — to isolate
	// the HMAC computation itself against the known vector.
	origTolerance := tolerance
	tolerance = 1000000 * time.Hour // effectively unlimited for this one test
	defer func() { tolerance = origTolerance }()

	if err := verifySignature(secret, id, timestamp, wantSig, []byte(payload)); err != nil {
		t.Fatalf("known-good Svix vector rejected: %v", err)
	}
}

func TestVerifySignatureFreshRoundTrip(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	id := "msg_test123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	payload := `{"type":"user.created","data":{"id":"user_abc"}}`

	sig := sign(t, secret, id, timestamp, payload)

	if err := verifySignature(secret, id, timestamp, sig, []byte(payload)); err != nil {
		t.Fatalf("freshly-signed payload rejected: %v", err)
	}
}

func TestVerifySignatureMultipleSignatures(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	id := "msg_test123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	payload := `{"type":"user.created","data":{"id":"user_abc"}}`

	realSig := sign(t, secret, id, timestamp, payload)
	// A second, bogus signature alongside the real one — mirrors Svix
	// sending multiple space-separated signatures during secret rotation;
	// any single match must be accepted.
	header := "v1,bm90dGhlcmlnaHRzaWc= " + realSig

	if err := verifySignature(secret, id, timestamp, header, []byte(payload)); err != nil {
		t.Fatalf("valid signature among multiple candidates rejected: %v", err)
	}
}

func TestVerifySignatureRejectsTamperedPayload(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	id := "msg_test123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	payload := `{"type":"user.created","data":{"id":"user_abc"}}`

	sig := sign(t, secret, id, timestamp, payload)

	tampered := `{"type":"user.deleted","data":{"id":"user_abc"}}`
	if err := verifySignature(secret, id, timestamp, sig, []byte(tampered)); err == nil {
		t.Fatal("expected tampered payload to be rejected, but it verified")
	}
}

func TestVerifySignatureRejectsWrongSecret(t *testing.T) {
	id := "msg_test123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	payload := `{"type":"user.created","data":{"id":"user_abc"}}`

	sig := sign(t, "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", id, timestamp, payload)

	if err := verifySignature("whsec_differentSecretEntirelyXXXXXXXXXX", id, timestamp, sig, []byte(payload)); err == nil {
		t.Fatal("expected signature signed with a different secret to be rejected, but it verified")
	}
}

func TestVerifySignatureRejectsStaleTimestamp(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	id := "msg_test123"
	staleTimestamp := strconv.FormatInt(time.Now().Add(-1*time.Hour).Unix(), 10)
	payload := `{"type":"user.created","data":{"id":"user_abc"}}`

	sig := sign(t, secret, id, staleTimestamp, payload)

	if err := verifySignature(secret, id, staleTimestamp, sig, []byte(payload)); err == nil {
		t.Fatal("expected a 1-hour-old timestamp to be rejected as stale, but it verified")
	}
}

func TestVerifySignatureRejectsMissingHeaders(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	if err := verifySignature(secret, "", "123", "v1,x", []byte("{}")); err == nil {
		t.Fatal("expected missing svix-id to be rejected")
	}
	if err := verifySignature(secret, "msg_x", "123", "", []byte("{}")); err == nil {
		t.Fatal("expected missing svix-signature to be rejected")
	}
}

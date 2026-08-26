package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type clerkEmailAddress struct {
	ID           string `json:"id"`
	EmailAddress string `json:"email_address"`
}

type clerkUserData struct {
	ID                    string              `json:"id"`
	FirstName             string              `json:"first_name"`
	LastName              string              `json:"last_name"`
	ImageURL              string              `json:"image_url"`
	PrimaryEmailAddressID string              `json:"primary_email_address_id"`
	EmailAddresses        []clerkEmailAddress `json:"email_addresses"`
}

type clerkWebhookEvent struct {
	Type string        `json:"type"`
	Data clerkUserData `json:"data"`
}

func main() {
	secret := os.Getenv("CLERK_WEBHOOK_SECRET")
	if secret == "" {
		fmt.Println("CLERK_WEBHOOK_SECRET not set locally")
		os.Exit(1)
	}

	testClerkID := "user_diagtest_" + strconv.FormatInt(time.Now().Unix(), 10)
	event := clerkWebhookEvent{
		Type: "user.created",
		Data: clerkUserData{
			ID:                    testClerkID,
			FirstName:             "Diag",
			LastName:              "Test",
			ImageURL:              "https://img.clerk.com/test.png",
			PrimaryEmailAddressID: "idn_1",
			EmailAddresses:        []clerkEmailAddress{{ID: "idn_1", EmailAddress: "diagtest@example.com"}},
		},
	}
	payload, _ := json.Marshal(event)

	svixID := "msg_diagtest"
	svixTimestamp := strconv.FormatInt(time.Now().Unix(), 10)

	secretBytes, err := base64.StdEncoding.DecodeString(secret[len("whsec_"):])
	if err != nil {
		fmt.Println("secret decode error:", err)
		os.Exit(1)
	}
	signedContent := svixID + "." + svixTimestamp + "." + string(payload)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(signedContent))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	svixSignature := "v1," + sig

	req, _ := http.NewRequest(http.MethodPost, "https://lumo-new-user-svc.onrender.com/webhooks/clerk", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("svix-id", svixID)
	req.Header.Set("svix-timestamp", svixTimestamp)
	req.Header.Set("svix-signature", svixSignature)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("request error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Println("webhook POST status:", resp.StatusCode, "body:", string(body))

	// give the (fire-and-forget-free, this handler is synchronous) DB write
	// a brief moment then check
	time.Sleep(1 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Println("connect error:", err)
		os.Exit(1)
	}
	defer pool.Close()

	var exists bool
	pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE clerk_id = $1)", testClerkID).Scan(&exists)
	fmt.Println("row created in DB for test clerkId:", exists)

	if exists {
		pool.Exec(ctx, "DELETE FROM users WHERE clerk_id = $1", testClerkID)
		fmt.Println("cleaned up test row")
	}
}

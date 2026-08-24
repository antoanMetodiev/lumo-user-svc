package clerkwebhook

import "strings"

// ClerkEmailAddress mirrors dtos.clerk.ClerkEmailAddress.
type ClerkEmailAddress struct {
	ID           string `json:"id"`
	EmailAddress string `json:"email_address"`
}

// ClerkUserData mirrors dtos.clerk.ClerkUserData.
type ClerkUserData struct {
	ID                    string              `json:"id"`
	FirstName             string              `json:"first_name"`
	LastName              string              `json:"last_name"`
	ImageURL              string              `json:"image_url"`
	PrimaryEmailAddressID string              `json:"primary_email_address_id"`
	EmailAddresses        []ClerkEmailAddress `json:"email_addresses"`
}

// PrimaryEmail mirrors ClerkUserData.primaryEmail(): the address matching
// primary_email_address_id, falling back to the first address in the list,
// or "" if there are none at all.
func (d ClerkUserData) PrimaryEmail() string {
	for _, addr := range d.EmailAddresses {
		if addr.ID != "" && d.PrimaryEmailAddressID != "" && addr.ID == d.PrimaryEmailAddressID {
			return addr.EmailAddress
		}
	}
	if len(d.EmailAddresses) == 0 {
		return ""
	}
	return d.EmailAddresses[0].EmailAddress
}

// FullName mirrors ClerkUserData.fullName(): trimmed "first last", or "" if
// both are blank.
func (d ClerkUserData) FullName() string {
	first := strings.TrimSpace(d.FirstName)
	last := strings.TrimSpace(d.LastName)
	return strings.TrimSpace(first + " " + last)
}

// ClerkWebhookEvent mirrors dtos.clerk.ClerkWebhookEvent.
type ClerkWebhookEvent struct {
	Type string        `json:"type"`
	Data ClerkUserData `json:"data"`
}

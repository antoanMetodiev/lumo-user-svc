package clerkwebhook

import "testing"

func TestPrimaryEmailMatchesPrimaryID(t *testing.T) {
	d := ClerkUserData{
		PrimaryEmailAddressID: "idB",
		EmailAddresses: []ClerkEmailAddress{
			{ID: "idA", EmailAddress: "a@example.com"},
			{ID: "idB", EmailAddress: "b@example.com"},
		},
	}

	if got := d.PrimaryEmail(); got != "b@example.com" {
		t.Fatalf("PrimaryEmail() = %q, want %q", got, "b@example.com")
	}
}

func TestPrimaryEmailFallsBackWhenNoIDsMatch(t *testing.T) {
	d := ClerkUserData{
		PrimaryEmailAddressID: "idZ", // matches nothing in the list below
		EmailAddresses: []ClerkEmailAddress{
			{ID: "idA", EmailAddress: "a@example.com"},
			{ID: "idB", EmailAddress: "b@example.com"},
		},
	}

	if got := d.PrimaryEmail(); got != "a@example.com" {
		t.Fatalf("PrimaryEmail() = %q, want first address %q", got, "a@example.com")
	}
}

func TestPrimaryEmailFallsBackWhenIDsBlank(t *testing.T) {
	d := ClerkUserData{
		PrimaryEmailAddressID: "",
		EmailAddresses: []ClerkEmailAddress{
			{ID: "", EmailAddress: "a@example.com"},
		},
	}

	if got := d.PrimaryEmail(); got != "a@example.com" {
		t.Fatalf("PrimaryEmail() = %q, want %q", got, "a@example.com")
	}
}

func TestPrimaryEmailEmptyWhenNoAddresses(t *testing.T) {
	d := ClerkUserData{PrimaryEmailAddressID: "idA"}

	if got := d.PrimaryEmail(); got != "" {
		t.Fatalf("PrimaryEmail() = %q, want empty string", got)
	}
}

func TestFullNameJoinsFirstAndLast(t *testing.T) {
	d := ClerkUserData{FirstName: "Ada", LastName: "Lovelace"}

	if got := d.FullName(); got != "Ada Lovelace" {
		t.Fatalf("FullName() = %q, want %q", got, "Ada Lovelace")
	}
}

func TestFullNameEmptyWhenBothBlank(t *testing.T) {
	d := ClerkUserData{}

	if got := d.FullName(); got != "" {
		t.Fatalf("FullName() = %q, want empty string", got)
	}
}

func TestFullNameOnlyFirstName(t *testing.T) {
	d := ClerkUserData{FirstName: "Ada"}

	if got := d.FullName(); got != "Ada" {
		t.Fatalf("FullName() = %q, want %q", got, "Ada")
	}
}

func TestFullNameOnlyLastName(t *testing.T) {
	d := ClerkUserData{LastName: "Lovelace"}

	if got := d.FullName(); got != "Lovelace" {
		t.Fatalf("FullName() = %q, want %q", got, "Lovelace")
	}
}

func TestFullNameTrimsWhitespace(t *testing.T) {
	d := ClerkUserData{FirstName: "  Ada  ", LastName: "  Lovelace  "}

	if got := d.FullName(); got != "Ada Lovelace" {
		t.Fatalf("FullName() = %q, want %q", got, "Ada Lovelace")
	}
}

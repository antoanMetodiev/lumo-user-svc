// Package models mirrors the Java entities field-for-field, including their
// exact Jackson JSON property names, so every existing consumer
// (lumo-api-gateway, which decodes some of this — see its own
// internal/users.UserResponse — and proxies the rest raw) keeps working
// unchanged against this Go rewrite.
package models

import (
	"time"

	"github.com/google/uuid"
)

// SubscriptionType mirrors feather.models.SubscriptionType.
type SubscriptionType string

const (
	SubscriptionRegular SubscriptionType = "REGULAR_USER"
	SubscriptionPro     SubscriptionType = "PRO_USER"
)

// Valid reports whether s is one of the two enum constants — mirrors
// Jackson's default enum deserialization rejecting anything else.
func (s SubscriptionType) Valid() bool {
	return s == SubscriptionRegular || s == SubscriptionPro
}

// User mirrors feather.models.User.
type User struct {
	ClerkID          string           `json:"clerkId"`
	Email            string           `json:"email"`
	DisplayName      *string          `json:"displayName"`
	ProfileImageURL  *string          `json:"profileImageURL"`
	SubscriptionType SubscriptionType `json:"subscriptionType"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
}

// WatchlistItem mirrors feather.models.WatchlistItem.
type WatchlistItem struct {
	ID           uuid.UUID `json:"id"`
	ClerkID      string    `json:"clerkId"`
	RecordID     string    `json:"recordId"`
	TmdbID       *string   `json:"tmdbId"`
	Title        *string   `json:"title"`
	PosterImgURL *string   `json:"posterImgURL"`
	TmdbRating   *string   `json:"tmdbRating"`
	Type         *string   `json:"type"`
	VideoID      *string   `json:"videoId"`
	AddedAt      time.Time `json:"addedAt"`
}

// LastViewedItem mirrors feather.models.LastViewedItem.
type LastViewedItem struct {
	ID          uuid.UUID `json:"id"`
	ClerkID     string    `json:"clerkId"`
	Type        string    `json:"type"`
	TmdbID      *string   `json:"tmdbId"`
	SeriesID    *string   `json:"seriesId"`
	PosterURL   *string   `json:"posterURL"`
	Title       string    `json:"title"`
	TmdbRating  *string   `json:"tmdbRating"`
	VideoURL    *string   `json:"videoURL"`
	ReleaseDate *string   `json:"releaseDate"`
	ViewedAt    time.Time `json:"viewedAt"`
}

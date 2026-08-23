package com.example.lumousersvc.feather.models;

import jakarta.persistence.*;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.experimental.Accessors;

import java.time.Instant;

/**
 * Keyed by the Clerk user id (e.g. "user_2abc...") instead of a locally
 * generated UUID — Clerk owns identity/auth now, this table only mirrors the
 * profile fields we need plus app-specific data Clerk doesn't have (subscription).
 * Kept in sync via the Clerk webhook (see ClerkWebhookService), not written to directly.
 */
@Entity
@Table(name = "users")
@Data
@Accessors(chain = true)
@Builder
@AllArgsConstructor
@NoArgsConstructor
public class User {

    @Id
    @Column(name = "clerk_id", nullable = false, updatable = false)
    private String clerkId;

    @Column(nullable = false)
    private String email;

    @Column(name = "display_name")
    private String displayName;

    @Column(name = "profile_image_url")
    private String profileImageURL;

    @Enumerated(EnumType.STRING)
    @Column(name = "subscription_type", nullable = false)
    @Builder.Default
    private SubscriptionType subscriptionType = SubscriptionType.REGULAR_USER;

    @Column(name = "created_at", nullable = false, updatable = false)
    private Instant createdAt;

    @Column(name = "updated_at", nullable = false)
    private Instant updatedAt;
}

package com.example.lumousersvc.feather.services;

import com.example.lumousersvc.feather.dtos.clerk.ClerkUserData;
import com.example.lumousersvc.feather.dtos.clerk.ClerkWebhookEvent;
import com.example.lumousersvc.feather.models.SubscriptionType;
import com.example.lumousersvc.feather.models.User;
import com.example.lumousersvc.feather.repositories.UserRepository;
import org.springframework.stereotype.Service;

import java.time.Instant;

/**
 * Keeps the local `users` table in sync with Clerk, which is the source of
 * truth for identity/auth now. New signups (Google or email/password — Clerk
 * handles both the same way) land here as "user.created", so every account
 * always gets a local row, always starting on SubscriptionType.REGULAR_USER.
 */
@Service
public class ClerkWebhookService {

    private final UserRepository userRepository;
    private final PaymentsSyncService paymentsSyncService;

    public ClerkWebhookService(UserRepository userRepository, PaymentsSyncService paymentsSyncService) {
        this.userRepository = userRepository;
        this.paymentsSyncService = paymentsSyncService;
    }

    public void handle(ClerkWebhookEvent event) {
        if (event == null || event.getType() == null) return;
        ClerkUserData data = event.getData();
        if (data == null || data.getId() == null) return;

        switch (event.getType()) {
            case "user.created", "user.updated" -> upsertUser(data);
            case "user.deleted" -> deleteUser(data.getId());
            default -> { /* not something we track */ }
        }
    }

    private void upsertUser(ClerkUserData data) {
        User user = userRepository.findById(data.getId()).orElseGet(() ->
                User.builder()
                        .clerkId(data.getId())
                        .subscriptionType(SubscriptionType.REGULAR_USER)
                        .createdAt(Instant.now())
                        .build()
        );

        user.setEmail(data.primaryEmail());
        user.setDisplayName(data.fullName());
        user.setProfileImageURL(data.getImageUrl());
        user.setUpdatedAt(Instant.now());

        userRepository.save(user);
    }

    private void deleteUser(String clerkId) {
        // deleteById() throws if the row doesn't exist — which happens for real
        // whenever a user is deleted before their "user.created" ever landed
        // (e.g. it arrived while this service was down or misconfigured), so this
        // has to be a no-op rather than an error in that case.
        if (userRepository.existsById(clerkId)) {
            userRepository.deleteById(clerkId);
        }

        // Unconditional, unlike the local delete above — lumo-payments-svc
        // might have a row even if this service's own one was somehow missing
        // (or vice versa), and its delete is already a no-op for a clerkId it
        // doesn't know about, so there's no need to gate this on the check above.
        paymentsSyncService.deleteSubscription(clerkId);
    }
}

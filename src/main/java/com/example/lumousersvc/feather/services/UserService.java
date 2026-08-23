package com.example.lumousersvc.feather.services;

import com.example.lumousersvc.feather.models.SubscriptionType;
import com.example.lumousersvc.feather.models.User;
import com.example.lumousersvc.feather.repositories.UserRepository;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.Instant;
import java.util.Optional;

@Service
public class UserService {

    private final UserRepository userRepository;

    public UserService(UserRepository userRepository) {
        this.userRepository = userRepository;
    }

    public Optional<User> findByClerkId(String clerkId) {
        return userRepository.findById(clerkId);
    }

    /**
     * Called via /internal/users/{clerkId}/subscription (lumo-payments-svc,
     * through the gateway) after a Stripe webhook changes a plan. Returns
     * false if the Clerk webhook hasn't synced this account into our DB yet —
     * same race lumo-user-svc already tolerates elsewhere.
     */
    @Transactional
    public boolean updateSubscriptionType(String clerkId, SubscriptionType subscriptionType) {
        return userRepository.findById(clerkId).map(user -> {
            user.setSubscriptionType(subscriptionType).setUpdatedAt(Instant.now());
            userRepository.save(user);
            return true;
        }).orElse(false);
    }
}

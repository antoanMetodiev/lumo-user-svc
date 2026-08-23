package com.example.lumousersvc.feather.controllers;

import com.example.lumousersvc.feather.dtos.UpdateSubscriptionRequest;
import com.example.lumousersvc.feather.services.UserService;
import jakarta.validation.Valid;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * Called by lumo-api-gateway on behalf of lumo-payments-svc after a Stripe
 * webhook changes a subscription's status — there's no live Clerk session for
 * this call (Stripe triggered it, not a browser), so the target user is a
 * path variable rather than an authenticated principal. Protected by
 * GatewayAuthenticationFilter's internal-only path list — X-Internal-Api-Key
 * only, no X-Clerk-User-Id required — see SecurityConfig.
 */
@RestController
@RequestMapping("/internal/users")
public class InternalUserController {

    private final UserService userService;

    public InternalUserController(UserService userService) {
        this.userService = userService;
    }

    // POST, not PATCH — the gateway's Feign client can't send PATCH at all
    // (its default HTTP client wraps HttpURLConnection, which rejects it).
    @PostMapping("/{clerkId}/subscription")
    public ResponseEntity<Void> updateSubscription(@PathVariable String clerkId, @RequestBody @Valid UpdateSubscriptionRequest request) {
        boolean updated = userService.updateSubscriptionType(clerkId, request.getSubscriptionType());
        return updated ? ResponseEntity.noContent().build() : ResponseEntity.notFound().build();
    }
}

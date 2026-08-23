package com.example.lumousersvc.feather.controllers;

import com.example.lumousersvc.feather.dtos.clerk.ClerkWebhookEvent;
import com.example.lumousersvc.feather.services.ClerkWebhookService;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.svix.Webhook;
import com.svix.exceptions.EmptyWebhookSecretException;
import com.svix.exceptions.WebhookVerificationException;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Receives Clerk's user.created/user.updated/user.deleted events (delivered
 * via Svix). Signature verification uses the raw request body — never a
 * re-serialized one, since that would change the bytes being signed.
 */
@RestController
@RequestMapping("/webhooks")
public class ClerkWebhookController {

    private final Webhook webhook;
    private final ObjectMapper objectMapper;
    private final ClerkWebhookService clerkWebhookService;

    public ClerkWebhookController(
            @Value("${clerk.webhook-secret}") String webhookSecret,
            ObjectMapper objectMapper,
            ClerkWebhookService clerkWebhookService
    ) throws EmptyWebhookSecretException {
        this.webhook = new Webhook(webhookSecret);
        this.objectMapper = objectMapper;
        this.clerkWebhookService = clerkWebhookService;
    }

    @PostMapping("/clerk")
    public ResponseEntity<Void> handleClerkWebhook(HttpServletRequest request) throws IOException {
        String payload = new String(request.getInputStream().readAllBytes(), StandardCharsets.UTF_8);

        Map<String, List<String>> headers = new HashMap<>();
        headers.put("svix-id", Collections.singletonList(request.getHeader("svix-id")));
        headers.put("svix-timestamp", Collections.singletonList(request.getHeader("svix-timestamp")));
        headers.put("svix-signature", Collections.singletonList(request.getHeader("svix-signature")));

        try {
            webhook.verify(payload, headers);
        } catch (WebhookVerificationException e) {
            return ResponseEntity.status(HttpStatus.UNAUTHORIZED).build();
        }

        ClerkWebhookEvent event = objectMapper.readValue(payload, ClerkWebhookEvent.class);
        clerkWebhookService.handle(event);

        return ResponseEntity.ok().build();
    }
}

package com.example.lumousersvc.feather.services;

import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.web.client.RestClient;
import org.springframework.web.client.RestClientException;

/**
 * Tells lumo-payments-svc — via lumo-api-gateway, same as every other
 * cross-service call in this system — to drop its own copy of a Clerk
 * account's billing record once ClerkWebhookService deletes it here.
 * Otherwise lumo-payments-svc would keep an orphaned `subscriptions` row for
 * someone who no longer has an account at all.
 */
@Slf4j
@Service
public class PaymentsSyncService {

    private final RestClient gatewayRestClient;

    public PaymentsSyncService(RestClient gatewayRestClient) {
        this.gatewayRestClient = gatewayRestClient;
    }

    /**
     * Best-effort: the Clerk account is already gone from this service's own
     * `users` table regardless of whether this call succeeds — a failure
     * here just leaves a stale row in lumo-payments-svc (a data-hygiene
     * issue, not a security or billing one), so it's logged, not thrown.
     */
    public void deleteSubscription(String clerkId) {
        try {
            gatewayRestClient.delete()
                    .uri("/internal/subscriptions/{clerkId}", clerkId)
                    .retrieve()
                    .toBodilessEntity();
        } catch (RestClientException e) {
            log.error("Failed to delete lumo-payments-svc's subscription record for deleted clerkId={} — a stale row may remain", clerkId, e);
        }
    }
}

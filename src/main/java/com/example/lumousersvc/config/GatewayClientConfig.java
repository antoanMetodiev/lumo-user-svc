package com.example.lumousersvc.config;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.web.client.RestClient;

/**
 * A single outbound call (see ClerkWebhookService's account-deletion cascade
 * to lumo-payments-svc) doesn't warrant pulling in Feign/Spring-Cloud-OpenFeign
 * the way the gateway does for its many downstream clients — RestClient is
 * already on the classpath via spring-boot-starter-web. Same pattern as
 * lumo-payments-svc's own GatewayClientConfig.
 */
@Configuration
public class GatewayClientConfig {

    @Bean
    public RestClient gatewayRestClient(
            @Value("${gateway.url}") String gatewayUrl,
            @Value("${internal.service-secret}") String internalServiceSecret
    ) {
        return RestClient.builder()
                .baseUrl(gatewayUrl)
                .defaultHeader("X-Internal-Api-Key", internalServiceSecret)
                .build();
    }
}

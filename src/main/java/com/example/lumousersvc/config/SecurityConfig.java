package com.example.lumousersvc.config;

import com.example.lumousersvc.utils.GatewayAuthenticationFilter;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.security.web.authentication.www.BasicAuthenticationFilter;
import org.springframework.web.cors.CorsConfiguration;
import org.springframework.web.cors.CorsConfigurationSource;
import org.springframework.web.cors.UrlBasedCorsConfigurationSource;

import java.util.Arrays;
import java.util.List;

@Configuration
public class SecurityConfig {

    // Path prefixes that require the caller to be lumo-api-gateway, already
    // carrying a verified Clerk user id (see GatewayAuthenticationFilter).
    // "/watchlist" also covers "/watchlist/{recordId}". The webhook is
    // authenticated separately via its own Svix signature, not this filter.
    private static final List<String> USER_SCOPED_PATHS = List.of("/user/me", "/watchlist", "/last-viewed");

    // Service-to-service paths — no live Clerk session behind these calls
    // (e.g. lumo-payments-svc syncing a plan change after a Stripe webhook).
    private static final List<String> INTERNAL_PATHS = List.of("/internal");

    @Bean
    public GatewayAuthenticationFilter gatewayAuthenticationFilter(@Value("${internal.service-secret}") String internalServiceSecret) {
        return new GatewayAuthenticationFilter(internalServiceSecret, USER_SCOPED_PATHS, INTERNAL_PATHS);
    }

    @Bean
    public SecurityFilterChain securityFilterChain(HttpSecurity http, GatewayAuthenticationFilter gatewayAuthenticationFilter) throws Exception {
        http
                .cors(cors -> cors.configurationSource(corsConfigurationSource()))
                .csrf(AbstractHttpConfigurer::disable)
                .sessionManagement(session -> session.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
                .authorizeHttpRequests(auth -> auth.anyRequest().permitAll())
                .addFilterBefore(gatewayAuthenticationFilter, BasicAuthenticationFilter.class);

        return http.build();
    }

    @Bean
    public CorsConfigurationSource corsConfigurationSource() {
        CorsConfiguration configuration = new CorsConfiguration();

        // Nothing calls this service directly from a browser anymore — the
        // Next.js app's server talks to lumo-api-gateway, which talks to this
        // service (server-to-server, no Origin header involved). Kept for
        // local dev / testing this service directly without the gateway.
        configuration.setAllowedOriginPatterns(Arrays.asList("http://localhost:3000"));

        configuration.setAllowedMethods(Arrays.asList("GET", "POST", "PUT", "DELETE", "OPTIONS"));
        configuration.setAllowCredentials(true);

        configuration.setAllowedHeaders(Arrays.asList("Authorization", "Content-Type", "X-Requested-With"));

        UrlBasedCorsConfigurationSource source = new UrlBasedCorsConfigurationSource();
        source.registerCorsConfiguration("/**", configuration);
        return source;
    }
}

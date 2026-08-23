package com.example.lumousersvc.utils;

import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.List;

/**
 * Trusts headers set by lumo-api-gateway instead of verifying a Clerk session
 * JWT itself — the gateway is now the single place that talks to Clerk's
 * JWKS (see its own ClerkAuthenticationFilter). Two kinds of protected paths:
 *
 *  - userScopedPathPrefixes ("/user/me", "/watchlist", "/last-viewed"): a
 *    real end user is asking about themselves. Requires X-Internal-Api-Key
 *    (proves the caller is the gateway) AND X-Clerk-User-Id (the Clerk id
 *    the gateway already verified) — the latter becomes the Authentication
 *    principal.
 *  - internalPathPrefixes ("/internal"): a service-to-service call with no
 *    live end user behind it (e.g. lumo-payments-svc telling us a Stripe
 *    webhook changed someone's plan) — only X-Internal-Api-Key is required;
 *    the target user is a path variable the controller reads directly, not
 *    an Authentication principal.
 *
 * X-Internal-Api-Key is the only thing standing between "trust whatever
 * these headers say" and full impersonation, since this service still has a
 * public URL for now (no private network yet) — a stopgap until it does.
 *
 * Public endpoints (the Clerk webhook, heartbeat) skip this entirely — see
 * SecurityConfig.
 */
public class GatewayAuthenticationFilter extends OncePerRequestFilter {

    public static final String CLERK_USER_ID_HEADER = "X-Clerk-User-Id";
    public static final String INTERNAL_API_KEY_HEADER = "X-Internal-Api-Key";

    private final byte[] internalApiKey;
    private final List<String> userScopedPathPrefixes;
    private final List<String> internalPathPrefixes;

    public GatewayAuthenticationFilter(String internalApiKey, List<String> userScopedPathPrefixes, List<String> internalPathPrefixes) {
        this.internalApiKey = internalApiKey.getBytes(StandardCharsets.UTF_8);
        this.userScopedPathPrefixes = userScopedPathPrefixes;
        this.internalPathPrefixes = internalPathPrefixes;
    }

    @Override
    protected void doFilterInternal(HttpServletRequest request, HttpServletResponse response, FilterChain filterChain)
            throws ServletException, IOException {

        String uri = request.getRequestURI();
        boolean isUserScoped = matches(uri, userScopedPathPrefixes);
        boolean isInternal = matches(uri, internalPathPrefixes);

        if (!isUserScoped && !isInternal) {
            filterChain.doFilter(request, response);
            return;
        }

        if (!isFromGateway(request)) {
            response.setStatus(HttpServletResponse.SC_UNAUTHORIZED);
            return;
        }

        if (isUserScoped) {
            String clerkUserId = request.getHeader(CLERK_USER_ID_HEADER);
            if (clerkUserId == null || clerkUserId.isBlank()) {
                response.setStatus(HttpServletResponse.SC_UNAUTHORIZED);
                return;
            }

            SecurityContextHolder.getContext().setAuthentication(
                    new UsernamePasswordAuthenticationToken(clerkUserId, null, List.of())
            );
        }

        filterChain.doFilter(request, response);
    }

    private boolean matches(String uri, List<String> prefixes) {
        return prefixes.stream().anyMatch(prefix -> uri.equals(prefix) || uri.startsWith(prefix + "/"));
    }

    // Constant-time comparison so a mistyped/guessed key can't be brute-forced
    // faster by timing how quickly String.equals() bails out on the first
    // mismatched byte.
    private boolean isFromGateway(HttpServletRequest request) {
        String provided = request.getHeader(INTERNAL_API_KEY_HEADER);
        if (provided == null) return false;

        return MessageDigest.isEqual(provided.getBytes(StandardCharsets.UTF_8), internalApiKey);
    }
}

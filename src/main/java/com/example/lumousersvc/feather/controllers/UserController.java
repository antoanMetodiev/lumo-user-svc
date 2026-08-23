package com.example.lumousersvc.feather.controllers;

import com.example.lumousersvc.feather.models.User;
import com.example.lumousersvc.feather.services.UserService;
import org.springframework.http.ResponseEntity;
import org.springframework.security.core.Authentication;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/user")
public class UserController {

    private final UserService userService;

    public UserController(UserService userService) {
        this.userService = userService;
    }

    /**
     * Protected by GatewayAuthenticationFilter — `authentication.getName()` is the
     * verified Clerk user id (the token's `sub` claim) set there. 404 here means
     * the Clerk webhook hasn't synced this account into our DB yet, which should
     * be near-instant after sign-up but is technically a race the frontend should
     * tolerate (e.g. retry once) rather than treat as a hard failure.
     */
    @GetMapping("/me")
    public ResponseEntity<User> getCurrentUser(Authentication authentication) {
        return userService.findByClerkId(authentication.getName())
                .map(ResponseEntity::ok)
                .orElseGet(() -> ResponseEntity.notFound().build());
    }
}

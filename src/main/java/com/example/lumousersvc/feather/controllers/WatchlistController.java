package com.example.lumousersvc.feather.controllers;

import com.example.lumousersvc.feather.dtos.WatchlistItemRequest;
import com.example.lumousersvc.feather.models.WatchlistItem;
import com.example.lumousersvc.feather.services.WatchlistService;
import jakarta.validation.Valid;
import org.springframework.http.ResponseEntity;
import org.springframework.security.core.Authentication;
import org.springframework.web.bind.annotation.*;

import java.util.List;

// Every endpoint here requires a valid Clerk session — see
// GatewayAuthenticationFilter / SecurityConfig.PROTECTED_PATHS ("/watchlist").
// authentication.getName() is the verified Clerk user id.
@RestController
@RequestMapping("/watchlist")
public class WatchlistController {

    private final WatchlistService watchlistService;

    public WatchlistController(WatchlistService watchlistService) {
        this.watchlistService = watchlistService;
    }

    @GetMapping
    public List<WatchlistItem> getWatchlist(Authentication authentication) {
        return watchlistService.getWatchlist(authentication.getName());
    }

    @PostMapping
    public WatchlistItem addToWatchlist(Authentication authentication, @RequestBody @Valid WatchlistItemRequest request) {
        return watchlistService.addToWatchlist(authentication.getName(), request);
    }

    @DeleteMapping("/{recordId}")
    public ResponseEntity<Void> removeFromWatchlist(Authentication authentication, @PathVariable String recordId) {
        watchlistService.removeFromWatchlist(authentication.getName(), recordId);
        return ResponseEntity.noContent().build();
    }
}

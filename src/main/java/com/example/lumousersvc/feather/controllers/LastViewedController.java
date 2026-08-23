package com.example.lumousersvc.feather.controllers;

import com.example.lumousersvc.feather.dtos.LastViewedItemRequest;
import com.example.lumousersvc.feather.models.LastViewedItem;
import com.example.lumousersvc.feather.services.LastViewedService;
import jakarta.validation.Valid;
import org.springframework.security.core.Authentication;
import org.springframework.web.bind.annotation.*;

import java.util.List;

// Protected by GatewayAuthenticationFilter — see SecurityConfig.PROTECTED_PATHS ("/last-viewed").
@RestController
@RequestMapping("/last-viewed")
public class LastViewedController {

    private final LastViewedService lastViewedService;

    public LastViewedController(LastViewedService lastViewedService) {
        this.lastViewedService = lastViewedService;
    }

    @GetMapping
    public List<LastViewedItem> getLastViewed(Authentication authentication) {
        return lastViewedService.getLastViewed(authentication.getName());
    }

    @PostMapping
    public LastViewedItem recordView(Authentication authentication, @RequestBody @Valid LastViewedItemRequest request) {
        return lastViewedService.recordView(authentication.getName(), request);
    }
}

package com.example.lumousersvc.feather.dtos.clerk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import lombok.Data;

@Data
@JsonIgnoreProperties(ignoreUnknown = true)
public class ClerkWebhookEvent {

    private String type;

    private ClerkUserData data;
}

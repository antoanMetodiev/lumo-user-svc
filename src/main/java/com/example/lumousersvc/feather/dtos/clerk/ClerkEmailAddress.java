package com.example.lumousersvc.feather.dtos.clerk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Data;

@Data
@JsonIgnoreProperties(ignoreUnknown = true)
public class ClerkEmailAddress {

    private String id;

    @JsonProperty("email_address")
    private String emailAddress;
}

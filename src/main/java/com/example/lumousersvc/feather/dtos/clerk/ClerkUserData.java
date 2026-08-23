package com.example.lumousersvc.feather.dtos.clerk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Data;

import java.util.List;

@Data
@JsonIgnoreProperties(ignoreUnknown = true)
public class ClerkUserData {

    private String id;

    @JsonProperty("first_name")
    private String firstName;

    @JsonProperty("last_name")
    private String lastName;

    @JsonProperty("image_url")
    private String imageUrl;

    @JsonProperty("primary_email_address_id")
    private String primaryEmailAddressId;

    @JsonProperty("email_addresses")
    private List<ClerkEmailAddress> emailAddresses;

    public String primaryEmail() {
        if (emailAddresses == null) return null;
        return emailAddresses.stream()
                .filter(address -> address.getId() != null && address.getId().equals(primaryEmailAddressId))
                .map(ClerkEmailAddress::getEmailAddress)
                .findFirst()
                .orElseGet(() -> emailAddresses.isEmpty() ? null : emailAddresses.get(0).getEmailAddress());
    }

    public String fullName() {
        String first = firstName == null ? "" : firstName.trim();
        String last = lastName == null ? "" : lastName.trim();
        String full = (first + " " + last).trim();
        return full.isEmpty() ? null : full;
    }
}

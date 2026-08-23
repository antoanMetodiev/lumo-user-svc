package com.example.lumousersvc.feather.dtos;

import com.example.lumousersvc.feather.models.SubscriptionType;
import jakarta.validation.constraints.NotNull;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

@Data
@AllArgsConstructor
@NoArgsConstructor
public class UpdateSubscriptionRequest {

    @NotNull
    private SubscriptionType subscriptionType;
}

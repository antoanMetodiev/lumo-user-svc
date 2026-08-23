package com.example.lumousersvc.feather.dtos;

import jakarta.validation.constraints.NotBlank;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.experimental.Accessors;

@Data
@Accessors(chain = true)
@Builder
@AllArgsConstructor
@NoArgsConstructor
public class LastViewedItemRequest {

    @NotBlank
    private String type;

    private String tmdbId;
    private String seriesId;
    private String posterURL;

    @NotBlank
    private String title;

    private String tmdbRating;
    private String videoURL;
    private String releaseDate;
}

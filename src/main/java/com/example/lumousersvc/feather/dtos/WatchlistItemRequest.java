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
public class WatchlistItemRequest {

    @NotBlank
    private String recordId;

    private String tmdbId;
    private String title;
    private String posterImgURL;
    private String tmdbRating;
    private String type;
    private String videoId;
}

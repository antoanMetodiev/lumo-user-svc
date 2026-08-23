package com.example.lumousersvc.feather.models;

import jakarta.persistence.*;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.experimental.Accessors;

import java.time.Instant;
import java.util.UUID;

/**
 * One saved title for one Clerk user. `recordId` is the same identity key the
 * frontend already used for its localStorage watchlist — a movie's own `id`,
 * or a series' `tmdbId` — kept as-is so signed-in and anonymous watchlists
 * behave identically. `type` is left as a plain string ("MOVIE" / "TV-SHOW")
 * rather than a Java enum since "TV-SHOW" isn't a valid enum constant name.
 */
@Entity
@Table(name = "watchlist_items", uniqueConstraints = @UniqueConstraint(columnNames = {"clerk_id", "record_id"}))
@Data
@Accessors(chain = true)
@Builder
@AllArgsConstructor
@NoArgsConstructor
public class WatchlistItem {

    @Id
    @GeneratedValue(strategy = GenerationType.UUID)
    private UUID id;

    @Column(name = "clerk_id", nullable = false)
    private String clerkId;

    @Column(name = "record_id", nullable = false)
    private String recordId;

    @Column(name = "tmdb_id")
    private String tmdbId;

    private String title;

    @Column(name = "poster_img_url")
    private String posterImgURL;

    @Column(name = "tmdb_rating")
    private String tmdbRating;

    private String type;

    @Column(name = "video_id")
    private String videoId;

    @Column(name = "added_at", nullable = false)
    private Instant addedAt;
}

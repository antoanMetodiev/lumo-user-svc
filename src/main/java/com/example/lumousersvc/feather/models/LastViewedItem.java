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
 * One "last viewed" entry for one Clerk user. Deduped by `title` (same key
 * the frontend's localStorage version already used — a movie's `tmdbId` isn't
 * reliably present, so title is the one field guaranteed on every record).
 */
@Entity
@Table(name = "last_viewed_items", uniqueConstraints = @UniqueConstraint(columnNames = {"clerk_id", "title"}))
@Data
@Accessors(chain = true)
@Builder
@AllArgsConstructor
@NoArgsConstructor
public class LastViewedItem {

    @Id
    @GeneratedValue(strategy = GenerationType.UUID)
    private UUID id;

    @Column(name = "clerk_id", nullable = false)
    private String clerkId;

    @Column(nullable = false)
    private String type;

    @Column(name = "tmdb_id")
    private String tmdbId;

    @Column(name = "series_id")
    private String seriesId;

    @Column(name = "poster_url")
    private String posterURL;

    @Column(nullable = false)
    private String title;

    @Column(name = "tmdb_rating")
    private String tmdbRating;

    @Column(name = "video_url")
    private String videoURL;

    @Column(name = "release_date")
    private String releaseDate;

    @Column(name = "viewed_at", nullable = false)
    private Instant viewedAt;
}

package com.example.lumousersvc.feather.services;

import com.example.lumousersvc.feather.dtos.LastViewedItemRequest;
import com.example.lumousersvc.feather.models.LastViewedItem;
import com.example.lumousersvc.feather.repositories.LastViewedRepository;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.Instant;
import java.util.List;

@Service
public class LastViewedService {

    // Mirrors MAX_ITEMS in the frontend's localStorage version.
    private static final int MAX_ITEMS = 20;

    private final LastViewedRepository lastViewedRepository;

    public LastViewedService(LastViewedRepository lastViewedRepository) {
        this.lastViewedRepository = lastViewedRepository;
    }

    public List<LastViewedItem> getLastViewed(String clerkId) {
        return lastViewedRepository.findByClerkIdOrderByViewedAtDesc(clerkId);
    }

    /**
     * Re-viewing something already on the list just bumps it to the top —
     * same "remove the old entry, push a fresh one" behavior the localStorage
     * version had, so re-watching something doesn't create a duplicate row.
     */
    @Transactional
    public LastViewedItem recordView(String clerkId, LastViewedItemRequest request) {
        lastViewedRepository.deleteByClerkIdAndTitle(clerkId, request.getTitle());
        evictOldestIfAtCapacity(clerkId);

        return lastViewedRepository.save(
                LastViewedItem.builder()
                        .clerkId(clerkId)
                        .type(request.getType())
                        .tmdbId(request.getTmdbId())
                        .seriesId(request.getSeriesId())
                        .posterURL(request.getPosterURL())
                        .title(request.getTitle())
                        .tmdbRating(request.getTmdbRating())
                        .videoURL(request.getVideoURL())
                        .releaseDate(request.getReleaseDate())
                        .viewedAt(Instant.now())
                        .build()
        );
    }

    private void evictOldestIfAtCapacity(String clerkId) {
        if (lastViewedRepository.countByClerkId(clerkId) < MAX_ITEMS) return;

        List<LastViewedItem> ordered = lastViewedRepository.findByClerkIdOrderByViewedAtDesc(clerkId);
        LastViewedItem oldest = ordered.get(ordered.size() - 1);
        lastViewedRepository.delete(oldest);
    }
}

package com.example.lumousersvc.feather.services;

import com.example.lumousersvc.feather.dtos.WatchlistItemRequest;
import com.example.lumousersvc.feather.models.WatchlistItem;
import com.example.lumousersvc.feather.repositories.WatchlistRepository;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.Instant;
import java.util.List;

@Service
public class WatchlistService {

    // Mirrors MAX_WATCHLIST_ITEMS in the frontend's localStorage watchlist,
    // so signed-in and anonymous users see the same cap.
    private static final int MAX_ITEMS = 20;

    private final WatchlistRepository watchlistRepository;

    public WatchlistService(WatchlistRepository watchlistRepository) {
        this.watchlistRepository = watchlistRepository;
    }

    public List<WatchlistItem> getWatchlist(String clerkId) {
        return watchlistRepository.findByClerkIdOrderByAddedAtDesc(clerkId);
    }

    @Transactional
    public WatchlistItem addToWatchlist(String clerkId, WatchlistItemRequest request) {
        return watchlistRepository.findByClerkIdAndRecordId(clerkId, request.getRecordId())
                .orElseGet(() -> {
                    evictOldestIfAtCapacity(clerkId);
                    return watchlistRepository.save(
                            WatchlistItem.builder()
                                    .clerkId(clerkId)
                                    .recordId(request.getRecordId())
                                    .tmdbId(request.getTmdbId())
                                    .title(request.getTitle())
                                    .posterImgURL(request.getPosterImgURL())
                                    .tmdbRating(request.getTmdbRating())
                                    .type(request.getType())
                                    .videoId(request.getVideoId())
                                    .addedAt(Instant.now())
                                    .build()
                    );
                });
    }

    @Transactional
    public void removeFromWatchlist(String clerkId, String recordId) {
        watchlistRepository.deleteByClerkIdAndRecordId(clerkId, recordId);
    }

    private void evictOldestIfAtCapacity(String clerkId) {
        if (watchlistRepository.countByClerkId(clerkId) < MAX_ITEMS) return;

        List<WatchlistItem> ordered = watchlistRepository.findByClerkIdOrderByAddedAtDesc(clerkId);
        WatchlistItem oldest = ordered.get(ordered.size() - 1);
        watchlistRepository.delete(oldest);
    }
}

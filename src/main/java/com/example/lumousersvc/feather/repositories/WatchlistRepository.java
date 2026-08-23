package com.example.lumousersvc.feather.repositories;

import com.example.lumousersvc.feather.models.WatchlistItem;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.stereotype.Repository;

import java.util.List;
import java.util.Optional;
import java.util.UUID;

@Repository
public interface WatchlistRepository extends JpaRepository<WatchlistItem, UUID> {

    List<WatchlistItem> findByClerkIdOrderByAddedAtDesc(String clerkId);

    Optional<WatchlistItem> findByClerkIdAndRecordId(String clerkId, String recordId);

    void deleteByClerkIdAndRecordId(String clerkId, String recordId);

    long countByClerkId(String clerkId);
}

package com.example.lumousersvc.feather.repositories;

import com.example.lumousersvc.feather.models.LastViewedItem;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.stereotype.Repository;

import java.util.List;
import java.util.UUID;

@Repository
public interface LastViewedRepository extends JpaRepository<LastViewedItem, UUID> {

    List<LastViewedItem> findByClerkIdOrderByViewedAtDesc(String clerkId);

    void deleteByClerkIdAndTitle(String clerkId, String title);

    long countByClerkId(String clerkId);
}

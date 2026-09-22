-- Normalizes record_id to the TMDB id across the three personal-list tables.
--
-- WHY: record_id used to be whichever table's uuid the frontend happened to
-- be holding — `movies.id` on a details page, but `trending_movies.id` /
-- `trending_series.id` on the homepage hero. The same title saved from two
-- different places therefore produced two rows that neither place could see
-- as duplicates (reported for the series "Reacher"). The frontend now keys
-- every list by TMDB id (see my-app/lib/record-key.ts); this brings rows
-- written before that change into line, so they stay visible and removable.
--
-- Rows whose tmdb_id is NULL are left exactly as they are: there is nothing
-- to migrate them to. They keep working the way they always did — only their
-- own details page can match them, which is where they were saved from.
--
-- Safe to re-run. Wrapped in a transaction: either all three tables migrate
-- or none do.

BEGIN;

-- Step 1 — drop the losers of each duplicate pair BEFORE the update, or the
-- update itself would collide with the unique (clerk_id, record_id) index.
-- "Loser" = the newer row of a set that will collapse onto one record_id;
-- the oldest is kept so the original "saved at" date survives.

DELETE FROM watchlist_items a
      USING watchlist_items b
      WHERE a.tmdb_id IS NOT NULL
        AND a.clerk_id = b.clerk_id
        AND a.tmdb_id  = b.tmdb_id
        AND a.added_at > b.added_at;

DELETE FROM liked_items a
      USING liked_items b
      WHERE a.tmdb_id IS NOT NULL
        AND a.clerk_id = b.clerk_id
        AND a.tmdb_id  = b.tmdb_id
        AND a.liked_at > b.liked_at;

DELETE FROM playlist_items a
      USING playlist_items b
      WHERE a.tmdb_id IS NOT NULL
        AND a.playlist_id = b.playlist_id
        AND a.tmdb_id     = b.tmdb_id
        AND a.added_at    > b.added_at;

-- Step 2 — point every remaining row at its TMDB id.

UPDATE watchlist_items SET record_id = tmdb_id
 WHERE tmdb_id IS NOT NULL AND record_id IS DISTINCT FROM tmdb_id;

UPDATE liked_items SET record_id = tmdb_id
 WHERE tmdb_id IS NOT NULL AND record_id IS DISTINCT FROM tmdb_id;

UPDATE playlist_items SET record_id = tmdb_id
 WHERE tmdb_id IS NOT NULL AND record_id IS DISTINCT FROM tmdb_id;

COMMIT;

-- Verification — every row below should now have record_id = tmdb_id, and
-- the duplicate count should be zero:
--
--   SELECT count(*) FROM watchlist_items WHERE tmdb_id IS NOT NULL AND record_id <> tmdb_id;
--   SELECT clerk_id, tmdb_id, count(*) FROM watchlist_items
--    WHERE tmdb_id IS NOT NULL GROUP BY 1,2 HAVING count(*) > 1;

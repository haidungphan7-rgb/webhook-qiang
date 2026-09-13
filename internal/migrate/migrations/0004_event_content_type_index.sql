-- The content type filter compares `lower(split_part(content_type, ';', 1))`, so a plain
-- index on (inbox_id, content_type) can never be used by that predicate - it was a dead
-- index. Replace it with an expression index that matches the query.

DROP INDEX IF EXISTS event_inbox_ct_idx;

CREATE INDEX IF NOT EXISTS event_inbox_ct_idx
    ON event (inbox_id, lower(split_part(content_type, ';', 1)));

-- The event list is ordered by (created_at DESC, id DESC); make the index match so the
-- tiebreaker does not need an extra sort step inside each timestamp group.
DROP INDEX IF EXISTS event_inbox_created_idx;

CREATE INDEX IF NOT EXISTS event_inbox_created_idx
    ON event (inbox_id, created_at DESC, id DESC);

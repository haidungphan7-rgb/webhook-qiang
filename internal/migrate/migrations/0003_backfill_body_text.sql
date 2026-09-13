-- Backfill the searchable text mirror for rows written before 0002.
--
-- Two hazards make this more careful than a plain UPDATE:
--   1. PostgreSQL text cannot contain NUL, so a body with 0x00 must be skipped. The check
--      is done on the bytea itself (position on bytes) - never on the converted text,
--      because the conversion is what raises.
--   2. Payloads that are not valid UTF-8 make convert_from raise SQLSTATE 22021. The
--      conversion therefore happens in its own block with an exception handler, so one bad
--      row can never abort the backfill (or the startup that runs it).
--
-- Such rows stay unsearchable - they are still viewable and replayable from event.body.
-- The mirror is capped at 64 KiB to match storage.SearchableText.

DO $$
DECLARE
    r   record;
    txt text;
BEGIN
    FOR r IN
        SELECT id, body FROM event
        WHERE coalesce(body_text, '') = '' AND octet_length(body) > 0
    LOOP
        -- Skip binary payloads: NUL can never be represented as text. The needle is
        -- written in bytea hex format ('\x00' = one zero byte); E'\000' would be a text
        -- literal and cannot hold a NUL at all.
        IF position('\x00'::bytea IN r.body) > 0 THEN
            CONTINUE;
        END IF;

        BEGIN
            txt := left(convert_from(r.body, 'UTF8'), 65536);
        EXCEPTION WHEN others THEN
            CONTINUE; -- invalid UTF-8: leave this row unsearchable
        END;

        UPDATE event SET body_text = txt WHERE id = r.id;
    END LOOP;
END $$;

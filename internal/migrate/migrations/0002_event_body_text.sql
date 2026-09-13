-- Searchable text mirror of the request body.
--
-- Why a separate column instead of convert_from(body,'UTF8') at query time:
-- PostgreSQL raises `invalid byte sequence for encoding "UTF8"` (SQLSTATE 22021) when the
-- body is not valid UTF-8, and its text type cannot contain NUL bytes at all. A single
-- binary webhook (gzip, protobuf, a stray 0x00) would then make every keyword search on
-- that inbox fail with a 500 - the event list would become unreadable.
--
-- The application fills body_text at insert time, but only when the body is valid UTF-8
-- and NUL free; otherwise it stays empty and the event is simply not searchable (it is
-- still fully viewable and replayable from the raw body).

CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE event ADD COLUMN IF NOT EXISTS body_text text NOT NULL DEFAULT '';

-- Trigram index so `ILIKE '%x%'` does not have to seq-scan once the table grows.
CREATE INDEX IF NOT EXISTS event_body_text_trgm_idx ON event USING gin (body_text gin_trgm_ops);

-- NOTE: rows written before this migration start with an empty body_text and are
-- therefore not searchable until they are backfilled - see 0003_backfill_body_text.sql,
-- which does exactly that with per-row error handling (it is a separate migration because
-- a single UPDATE would abort on the first invalid row).

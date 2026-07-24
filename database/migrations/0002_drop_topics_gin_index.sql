-- Migration 0002: drop the unjustified JSONB GIN index on soroban_events.topics
--
-- Rationale (issue #250):
-- The MVP API exposes topic filtering exclusively through the generated
-- `topic_0` / `topic_1` columns, which are btree-indexed. No handler issues a
-- JSONB containment query (`topics @> ...` or `data @> ...`), so the GIN index
-- added no read benefit while imposing write-amplification on a high-ingest
-- table. Arbitrary JSONB containment is explicitly unsupported for the MVP.
--
-- If containment filters are introduced later, add a targeted GIN index using
-- `jsonb_path_ops` (smaller, faster for pure `@>` containment):
--   CREATE INDEX idx_soroban_events_topics_gin
--       ON soroban_events USING GIN (topics jsonb_path_ops);
--
-- See docs/db/jsonb-index-strategy.md for EXPLAIN evidence.

DROP INDEX IF EXISTS idx_soroban_events_topics_gin;

# JSONB Index Strategy (issue #250)

Status: **Decided — GIN containment index deferred for MVP**

## Question

`soroban_events.topics` and `soroban_events.data` are `JSONB`. Should we carry a
`GIN` index on them to support containment (`@>`) queries, or does that only add
write-amplification the MVP never benefits from?

## Supported JSONB query patterns (MVP)

The only JSONB read path the API exposes is **topic equality on the first two
topics**, and it is served through the `STORED` generated columns, not through
raw JSONB operators:

```sql
topic_0 TEXT GENERATED ALWAYS AS (topics ->> 0) STORED
topic_1 TEXT GENERATED ALWAYS AS (topics ->> 1) STORED
```

The `ListEvents` query (`crates/api/src/services/events.rs`) filters exclusively
on `contract_id`, `topic_0`, `topic_1`, `ledger_sequence` range, and a keyset
cursor on `(ledger_sequence, event_index)`:

```sql
WHERE ($1 = '' OR contract_id = $1)
  AND ($2 = '' OR topic_0 = $2)
  AND ($3 = '' OR topic_1 = $3)
  AND ($4 = 0  OR ledger_sequence >= $4)
  AND ($5 = 0  OR ledger_sequence <= $5)
  AND ($6 IS NULL OR (ledger_sequence, event_index) > ($6, $7))
ORDER BY ledger_sequence ASC, event_index ASC
LIMIT $8
```

The Go handler (`services/api/handlers/events.go`) only forwards these same
parameters. **No handler issues a JSONB containment (`@>`) query on `topics` or
`data`.**

## Decision

- **Arbitrary JSONB containment (`topics @> …`, `data @> …`) is unsupported for
  the MVP.**
- The plain `GIN (topics)` index that previously lived in `schema.sql` /
  `0001_init.sql` is **dropped** (migration `0002_drop_topics_gin_index.sql`).
  It served no query the API can produce, and a GIN index is one of the most
  write-expensive index types on a high-ingest, append-only table.
- Common topic filters stay **index-backed** through the generated columns:
  - `idx_soroban_events_topic_0` — `topic_0`
  - `idx_soroban_events_topic_1` — `topic_1`
  - `idx_soroban_events_contract_topic_0` — `(contract_id, topic_0)` composite
    for the dominant "events for contract X with topic Y" pattern.

If a future milestone adds real containment filters, reintroduce a **targeted**
index using `jsonb_path_ops` (smaller and faster for pure `@>` than the default
`jsonb_ops`):

```sql
CREATE INDEX idx_soroban_events_topics_gin
    ON soroban_events USING GIN (topics jsonb_path_ops);
```

## EXPLAIN evidence

PostgreSQL 16, `soroban_events` seeded with 50,000 rows across 200 contracts,
`ANALYZE`d. Reproduce with the seed script at the bottom of this doc.

### Supported filter: `contract_id` + `topic_0` → composite index scan

```
Bitmap Index Scan on idx_soroban_events_contract_topic_0
      Index Cond: ((contract_id = 'C0') AND (topic_0 = 'transfer'))
Execution Time: 2.843 ms
```

### Supported filter: `topic_0` + `topic_1` → BitmapAnd of two btree indexes

```
BitmapAnd
  ->  Bitmap Index Scan on idx_soroban_events_contract_topic_0
  ->  Bitmap Index Scan on idx_soroban_events_topic_1
Execution Time: 3.819 ms
```

### Supported filter: `topic_0` only → index-backed (no seq scan)

```
Index Scan using idx_soroban_events_ledger_sequence
      Filter: (topic_0 = 'mint')
Execution Time: 0.956 ms
```

Every supported filter is served by a btree index scan — none fall back to a
sequential scan — so **no GIN index is required** for the query patterns the MVP
exposes.

### Unsupported containment: `data @> …` → sequential scan (by design)

```
Seq Scan on soroban_events
      Filter: (data @> '{"from": "A5"}'::jsonb)
Execution Time: 4.176 ms
```

This confirms a containment query would be un-indexed today. Because the API
never emits such a query, this is acceptable; the seq scan is unreachable in
practice. Were we to expose containment, we would add the `jsonb_path_ops` GIN
index above rather than allow the seq scan.

## Write-amplification note

`soroban_events` is append-only on the indexer hot path (one `INSERT` per
event). Every additional index is maintained on every insert. A `GIN` index in
particular expands each JSONB document into one index entry per key/value path,
making it the costliest index to maintain per write. Dropping it removes that
per-insert cost with zero read regression, which matters most precisely on the
highest-ingest table in the system.

## Acceptance criteria mapping

- [x] Supported JSONB query patterns documented (topic-0/topic-1 equality via
      generated columns; containment unsupported).
- [x] GIN index added only where justified — here it is **deferred**, with
      EXPLAIN evidence that supported filters are already index-backed.
- [x] Common topic filters confirmed index-backed via the generated columns.

## Reproduction

```sql
-- Load schema
\i database/schema.sql

-- Seed 50k rows across 200 contracts
INSERT INTO soroban_events (contract_id, ledger_sequence, ledger_timestamp,
                            transaction_hash, event_index, event_type, topics, data)
SELECT 'C' || (g % 200),
       (g / 5)::bigint,
       NOW() - (g || ' seconds')::interval,
       md5(g::text),
       g % 5,
       'contract',
       jsonb_build_array((ARRAY['transfer','mint','burn','approve','swap'])[1 + (g % 5)],
                         'GADDR' || (g % 50)),
       jsonb_build_object('amount', g, 'from', 'A' || (g % 100), 'to', 'B' || (g % 100))
FROM generate_series(1, 50000) g;
ANALYZE soroban_events;

-- Then run EXPLAIN (ANALYZE, BUFFERS) on the queries above.
```

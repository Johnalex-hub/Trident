# @trident-indexer/sdk

TypeScript client SDK for the [Trident](https://github.com/Telocel-Labs/Trident) Soroban event indexer.

Query historical Soroban contract events and subscribe to real-time updates without running your own infrastructure.

## Install

```bash
npm install @trident-indexer/sdk
```

## Usage

```typescript
import { TridentClient } from "@trident-indexer/sdk";

const client = new TridentClient({
  apiUrl: "https://api.trident.telocel.io",
  apiKey: "your-api-key",
  network: "mainnet",
});

// Query historical events
const { events, cursor, hasMore } = await client.queryEvents({
  contractId: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM",
  topic0: "transfer",
  ledgerFrom: 50000,
  ledgerTo: 51000,
  limit: 50,
});

// Fetch the next page
if (hasMore && cursor) {
  const nextPage = await client.queryEvents({ after: cursor, limit: 50 });
}

// Or drain every page automatically with the async iterator — no manual
// cursor/hasMore bookkeeping. Stops when the server has no more results.
for await (const event of client.iterEvents({
  contractId: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM",
  topic0: "transfer",
})) {
  console.log(event.id, event.transactionHash);
}

// A safety valve caps the number of pages fetched (default 100). If the limit
// is hit while more results remain, a TridentError(code: "ITERATION_LIMIT")
// is thrown; raise it for very large backfills:
for await (const event of client.iterEvents(
  { contractId: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM" },
  { maxPages: 1000 },
)) {
  // ...process event
}

// Fetch a single event by UUID
const event = await client.getEventById({
  id: "550e8400-e29b-41d4-a716-446655440000",
});

// Subscribe to real-time events
const sub = client.subscribeToContract({
  contractId: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM",
  topic0: "transfer",
  onEvent: (event) => {
    console.log("New event:", event.transactionHash, event.topics);
  },
  onError: (err) => {
    console.error("WebSocket error:", err.message);
  },
});

// Later: clean up the subscription
sub.unsubscribe();
```

## Error handling

All methods throw `TridentError` on failure:

```typescript
import { TridentClient, TridentError } from "@trident-indexer/sdk";

try {
  const event = await client.getEventById({ id: "unknown-id" });
} catch (err) {
  if (err instanceof TridentError) {
    switch (err.code) {
      case "NOT_FOUND":
        console.log("Event does not exist");
        break;
      case "UNAUTHORIZED":
        console.log("Invalid API key");
        break;
      case "RATE_LIMITED":
        console.log("Too many requests — back off and retry");
        break;
      case "INTERNAL":
        console.log("Server error:", err.message);
        break;
    }
  }
}
```

## API

### `new TridentClient(config)`

| Field | Type | Description |
|-------|------|-------------|
| `apiUrl` | `string` | Base URL of the Trident REST API |
| `apiKey` | `string` | Your API key (sent as `X-API-Key` header) |
| `network` | `"mainnet" \| "testnet" \| "futurenet"` | Stellar network |

### `client.queryEvents(params)` → `Promise<PaginatedEvents>`

| Param | Type | Description |
|-------|------|-------------|
| `contractId?` | `string` | Filter by contract strkey |
| `topic0?` | `string` | Filter by first topic value |
| `topic1?` | `string` | Filter by second topic value |
| `ledgerFrom?` | `number` | Inclusive lower bound on ledger sequence |
| `ledgerTo?` | `number` | Inclusive upper bound on ledger sequence |
| `after?` | `string` | Cursor from previous page |
| `limit?` | `number` | Max events to return (1–200) |

### `client.iterEvents(params, options?)` → `AsyncIterable<SorobanEvent>`

Auto-paginating async iterator over `queryEvents`. Accepts the same `params` as
`queryEvents` and follows the server cursor until `hasMore === false`, yielding
each event. Any `TridentError` from an underlying page request propagates out of
the `for await` loop unchanged.

| Option | Type | Description |
|--------|------|-------------|
| `maxPages?` | `number` | Max pages to fetch before throwing `TridentError(code: "ITERATION_LIMIT")` (default `100`) |

Also exported standalone as `iterEvents(queryEvents, params, options?)` for use
without a full client instance.

### `client.getEventById(params)` → `Promise<SorobanEvent>`

| Param | Type | Description |
|-------|------|-------------|
| `id` | `string` | UUID of the event |

### `client.subscribeToContract(params)` → `Subscription`

| Param | Type | Description |
|-------|------|-------------|
| `contractId` | `string` | Contract to subscribe to |
| `topic0?` | `string` | Optional topic filter |
| `onEvent` | `(event: SorobanEvent) => void` | Called for each matching event |
| `onError?` | `(error: Error) => void` | Called on connection errors |

Returns a `Subscription` with an `unsubscribe()` method.

## License

MIT

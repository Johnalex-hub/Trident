import { afterEach, describe, expect, it, vi } from "vitest";
import {
  DEFAULT_MAX_PAGES,
  iterEvents,
  TridentClient,
  TridentError,
  type PaginatedEvents,
  type QueryEventsParams,
  type SorobanEvent,
} from "../src/index.js";

function makeEvent(id: number): SorobanEvent {
  return {
    id: `event-${id}`,
    contractId: "CTEST",
    ledgerSequence: id,
    ledgerTimestamp: "2024-01-01T00:00:00Z",
    transactionHash: `tx-${id}`,
    eventIndex: id % 5,
    eventType: "contract",
    topics: ["transfer"],
    data: null,
    createdAt: "2024-01-01T00:00:00Z",
  };
}

/**
 * Build a mocked queryEvents returning `pages` pages of `perPage` events each.
 * Page N returns cursor "cursor-N" and hasMore=true, except the final page
 * which returns hasMore=false.
 */
function pagedQuery(pages: number, perPage: number) {
  return vi.fn(async (params: QueryEventsParams): Promise<PaginatedEvents> => {
    // Derive current page index from the incoming cursor.
    const pageIndex = params.after
      ? Number(params.after.replace("cursor-", ""))
      : 0;
    const base = pageIndex * perPage;
    const events = Array.from({ length: perPage }, (_, i) =>
      makeEvent(base + i),
    );
    const isLast = pageIndex === pages - 1;
    return {
      events,
      cursor: isLast ? null : `cursor-${pageIndex + 1}`,
      hasMore: !isLast,
    };
  });
}

describe("iterEvents", () => {
  it("yields 15 events in order across 3 pages of 5 and stops on has_more=false", async () => {
    const query = pagedQuery(3, 5);

    const collected: SorobanEvent[] = [];
    for await (const event of iterEvents(query, { contractId: "CTEST" })) {
      collected.push(event);
    }

    expect(collected).toHaveLength(15);
    expect(collected.map((e) => e.id)).toEqual(
      Array.from({ length: 15 }, (_, i) => `event-${i}`),
    );
    // 3 page fetches, no more.
    expect(query).toHaveBeenCalledTimes(3);
  });

  it("forwards the next_cursor as `after` on each subsequent call", async () => {
    const query = pagedQuery(3, 5);

    // Drain the iterator.
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    for await (const _event of iterEvents(query, { contractId: "CTEST" })) {
      /* drain */
    }

    const calls = query.mock.calls.map((c) => c[0].after);
    expect(calls).toEqual([undefined, "cursor-1", "cursor-2"]);
  });

  it("propagates a TridentError thrown on page 2 out of the iterator", async () => {
    const query = vi.fn(
      async (params: QueryEventsParams): Promise<PaginatedEvents> => {
        if (params.after === "cursor-1") {
          throw new TridentError("INTERNAL", "boom on page 2");
        }
        return {
          events: [makeEvent(0)],
          cursor: "cursor-1",
          hasMore: true,
        };
      },
    );

    const collected: SorobanEvent[] = [];
    const run = async () => {
      for await (const event of iterEvents(query, {})) {
        collected.push(event);
      }
    };

    await expect(run()).rejects.toMatchObject({
      code: "INTERNAL",
      message: "boom on page 2",
    });
    // Page 1's single event was yielded before the error surfaced.
    expect(collected).toHaveLength(1);
  });

  it("throws TridentError(ITERATION_LIMIT) when maxPages is exceeded", async () => {
    // Never-ending stream: every page reports hasMore.
    const query = vi.fn(
      async (): Promise<PaginatedEvents> => ({
        events: [makeEvent(0)],
        cursor: "cursor-next",
        hasMore: true,
      }),
    );

    const run = async () => {
      for await (const _e of iterEvents(query, {}, { maxPages: 3 })) {
        void _e;
      }
    };

    await expect(run()).rejects.toMatchObject({ code: "ITERATION_LIMIT" });
    expect(query).toHaveBeenCalledTimes(3);
  });

  it("rejects a non-positive maxPages with ITERATION_LIMIT", async () => {
    const query = pagedQuery(1, 1);
    const run = async () => {
      for await (const _e of iterEvents(query, {}, { maxPages: 0 })) {
        void _e;
      }
    };
    await expect(run()).rejects.toMatchObject({ code: "ITERATION_LIMIT" });
    expect(query).not.toHaveBeenCalled();
  });

  it("defaults maxPages to 100", () => {
    expect(DEFAULT_MAX_PAGES).toBe(100);
  });
});

describe("client.iterEvents", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("drains all pages via the HTTP layer using next_cursor", async () => {
    const apiEvent = (id: number) => ({
      id: `event-${id}`,
      contract_id: "CTEST",
      ledger_sequence: id,
      ledger_timestamp: "2024-01-01T00:00:00Z",
      transaction_hash: `tx-${id}`,
      event_index: 0,
      event_type: "contract",
      topics: ["transfer"],
      data: '"null"',
      created_at: "2024-01-01T00:00:00Z",
    });

    const fetchMock = vi.fn((url: string) => {
      const hasCursor = url.includes("cursor=c1");
      const body = hasCursor
        ? { events: [apiEvent(1)], next_cursor: "", has_more: false }
        : { events: [apiEvent(0)], next_cursor: "c1", has_more: true };
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve(body),
        text: () => Promise.resolve(""),
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const client = new TridentClient({
      apiUrl: "http://localhost:3000",
      apiKey: "k",
      network: "testnet",
    });

    const ids: string[] = [];
    for await (const event of client.iterEvents({ contractId: "CTEST" })) {
      ids.push(event.id);
    }

    expect(ids).toEqual(["event-0", "event-1"]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[1][0]).toContain("cursor=c1");
  });
});

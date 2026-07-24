import { TridentError } from "./errors.js";
import type { PaginatedEvents, QueryEventsParams, SorobanEvent } from "./index.js";

/**
 * The single-page query function `iterEvents` chains over. This is exactly the
 * shape of `TridentClient.queryEvents`, so the client method can pass its own
 * bound `queryEvents` straight through.
 */
export type QueryEventsFn = (
  params: QueryEventsParams,
) => Promise<PaginatedEvents>;

export interface IterEventsOptions {
  /**
   * Safety valve: the maximum number of pages to fetch before giving up.
   * Prevents an unbounded loop on a runaway contract that keeps reporting
   * `hasMore`. Defaults to {@link DEFAULT_MAX_PAGES}. When the limit is reached
   * and the server still reports more results, a `TridentError` with code
   * `ITERATION_LIMIT` is thrown.
   */
  maxPages?: number;
}

/** Default `maxPages` safety valve for {@link iterEvents}. */
export const DEFAULT_MAX_PAGES = 100;

/**
 * Auto-paginating async iterator over `queryEvents`.
 *
 * Yields every event across every page, following the server's `next_cursor`
 * automatically until `has_more === false`. Any `TridentError` thrown by the
 * underlying `queryEvents` propagates out of the iterator unchanged, so a
 * `for await...of` loop can be wrapped in a normal `try/catch`.
 *
 * @param queryEvents single-page query function (e.g. a client's `queryEvents`)
 * @param params      filter params for the first page; its `after` cursor, if
 *                    any, is used as the starting point
 * @param options     iteration options ({@link IterEventsOptions})
 *
 * @throws {TridentError} with code `ITERATION_LIMIT` if `maxPages` is exceeded
 *         while the server still reports more results.
 */
export async function* iterEvents(
  queryEvents: QueryEventsFn,
  params: QueryEventsParams,
  options: IterEventsOptions = {},
): AsyncGenerator<SorobanEvent, void, unknown> {
  const maxPages = options.maxPages ?? DEFAULT_MAX_PAGES;
  if (!Number.isInteger(maxPages) || maxPages < 1) {
    throw new TridentError(
      "ITERATION_LIMIT",
      `maxPages must be a positive integer, got ${String(maxPages)}`,
    );
  }

  let cursor: string | null = params.after ?? null;
  let page = 0;

  for (;;) {
    const pageParams: QueryEventsParams =
      cursor === null ? params : { ...params, after: cursor };

    // A TridentError thrown here is intentionally not caught — it propagates
    // straight out of the generator to the caller's try/catch.
    const result = await queryEvents(pageParams);
    page += 1;

    for (const event of result.events) {
      yield event;
    }

    if (!result.hasMore) {
      return;
    }

    if (page >= maxPages) {
      throw new TridentError(
        "ITERATION_LIMIT",
        `iterEvents exceeded maxPages (${maxPages}) while more results remained; ` +
          `raise the maxPages option or narrow the query`,
      );
    }

    // Server reports more but gave no cursor to advance — stop rather than
    // refetch the same page forever.
    if (result.cursor === null) {
      return;
    }
    cursor = result.cursor;
  }
}

import { CicordApiError, createCicordClient, type Page } from "@cicord/client";
import { CicordProvider } from "@cicord/react";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vite-plus/test";
import {
  DataTable,
  pageWindow,
  readTableQuery,
  type DataTableColumn,
  type TableQuery,
} from "../src/components/data-table.tsx";
import { EmptyState } from "../src/components/common.tsx";

const BASE = import.meta.env.BASE_URL;

interface Item {
  id: string;
  name: string;
  size: number;
  kind: "a" | "b";
}

const ITEMS: Item[] = Array.from({ length: 120 }, (_, i) => ({
  id: `i${i + 1}`,
  name: `Item ${String(i + 1).padStart(3, "0")}`,
  size: (i * 37) % 101,
  kind: i % 3 === 0 ? "b" : "a",
}));

const columns: DataTableColumn<Item>[] = [
  { id: "name", header: "Name", sortKey: "name", primary: true, cell: (r) => r.name },
  { id: "size", header: "Size", sortKey: "size", descFirst: false, cell: (r) => String(r.size) },
  { id: "kind", header: "Kind", cell: (r) => r.kind },
];

/** A fake server: filters, sorts and pages ITEMS like the runtime does. */
function server(items: Item[] = ITEMS) {
  const calls: TableQuery[] = [];
  let fail: unknown = null;
  let hold: Promise<void> | null = null;
  const fetch = async (q: TableQuery): Promise<Page<Item>> => {
    calls.push(q);
    if (hold) await hold;
    if (fail) throw fail;
    const words = q.q.toLowerCase().split(/\s+/).filter(Boolean);
    const out = items
      .filter((it) => words.every((w) => it.name.toLowerCase().includes(w)))
      .filter((it) => !q.filters["kind"] || it.kind === q.filters["kind"])
      .sort((a, b) => {
        const c = q.sort === "size" ? a.size - b.size : a.name.localeCompare(b.name);
        return q.order === "desc" ? -c : c;
      });
    return {
      items: out.slice(q.offset, q.offset + q.limit),
      total: out.length,
      offset: q.offset,
      limit: q.limit,
      sort: q.sort,
      order: q.order,
    };
  };
  return {
    calls,
    fetch,
    fail: (e: unknown) => void (fail = e),
    hold: () => {
      let release = () => {};
      hold = new Promise<void>((r) => (release = r));
      return () => {
        hold = null;
        release();
      };
    },
  };
}

function setup(search = "", srv = server(), extra: { empty?: boolean } = {}) {
  window.history.replaceState(null, "", `${BASE}items${search}`);
  const client = createCicordClient({ fetch: async () => new Response("{}") });
  render(
    <CicordProvider client={client}>
      <DataTable<Item>
        label="Items"
        columns={columns}
        queryKey={(q) => ["test", "items", JSON.stringify(q)]}
        fetch={(q) => srv.fetch(q)}
        rowKey={(r) => r.id}
        rowHref={(r) => `/items/${r.id}`}
        defaultSort="name"
        defaultLimit={25}
        search={{ label: "Search items", placeholder: "Search" }}
        filters={[
          {
            id: "kind",
            label: "Kind",
            allLabel: "Any kind",
            options: [
              { value: "a", label: "A" },
              { value: "b", label: "B" },
            ],
          },
        ]}
        empty={<EmptyState title={extra.empty ? "Nothing here yet" : "Empty"} />}
      />
    </CicordProvider>,
  );
  return { srv, user: userEvent.setup() };
}

const header = (name: string) => screen.getByRole("columnheader", { name: new RegExp(name) });
const rows = () =>
  within(screen.getByRole("table", { name: "Items" }))
    .getAllByRole("row")
    .slice(1);
const params = () => new URLSearchParams(window.location.search);

beforeEach(() => window.history.replaceState(null, "", BASE));
afterEach(cleanup);

describe("DataTable", () => {
  test("first request, headers, range and rows as links", async () => {
    const { srv } = setup();
    expect(await screen.findByText("1–25 of 120")).toBeTruthy();
    expect(srv.calls[0]).toEqual({
      q: "",
      sort: "name",
      order: "asc",
      offset: 0,
      limit: 25,
      filters: {},
    });
    expect(header("Name").getAttribute("aria-sort")).toBe("ascending");
    expect(header("Size").getAttribute("aria-sort")).toBe("none");
    expect(header("Kind").hasAttribute("aria-sort")).toBe(false);
    expect(rows()).toHaveLength(25);
    const link = within(rows()[0]!).getByRole("link", { name: "Item 001" });
    expect(link.getAttribute("href")).toBe(`${BASE}items/i1`);
  });

  test("sorting: a click sorts the server-side, a second click reverses; the URL follows", async () => {
    const { srv, user } = setup();
    await screen.findByText("1–25 of 120");
    await user.click(within(header("Size")).getByRole("button", { name: /Size/ }));
    await waitFor(() => expect(header("Size").getAttribute("aria-sort")).toBe("ascending"));
    expect(header("Name").getAttribute("aria-sort")).toBe("none");
    expect(srv.calls.at(-1)).toMatchObject({ sort: "size", order: "asc", offset: 0 });
    expect(params().get("sort")).toBe("size");
    expect(within(rows()[0]!).getByText("0")).toBeTruthy();

    await user.click(within(header("Size")).getByRole("button"));
    await waitFor(() => expect(header("Size").getAttribute("aria-sort")).toBe("descending"));
    expect(srv.calls.at(-1)).toMatchObject({ sort: "size", order: "desc" });
    expect(params().get("order")).toBe("desc");

    // Back to the default sort: the URL is clean again.
    await user.click(within(header("Name")).getByRole("button"));
    await waitFor(() => expect(header("Name").getAttribute("aria-sort")).toBe("ascending"));
    expect(window.location.search).toBe("");
  });

  test("keyboard: the sort buttons and the row links are focusable", async () => {
    const { srv, user } = setup();
    await screen.findByText("1–25 of 120");
    within(header("Size")).getByRole("button").focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(srv.calls.at(-1)).toMatchObject({ sort: "size" }));
    const link = within(rows()[0]!).getByRole("link");
    link.focus();
    await user.keyboard("{Enter}");
    expect(window.location.pathname).toMatch(/\/items\/i\d+$/);
  });

  test("pagination: next, page numbers, last; page size; back/forward restore the page", async () => {
    const { srv, user } = setup();
    await screen.findByText("1–25 of 120");
    const nav = screen.getByRole("navigation", { name: "Pagination" });
    expect(within(nav).getByRole("button", { name: "Previous page" })).toHaveProperty(
      "disabled",
      true,
    );
    expect(within(nav).getByRole("button", { name: "Page 1" }).getAttribute("aria-current")).toBe(
      "page",
    );
    await user.click(within(nav).getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("26–50 of 120")).toBeTruthy();
    expect(srv.calls.at(-1)).toMatchObject({ offset: 25, limit: 25 });
    expect(params().get("page")).toBe("2");

    await user.click(within(nav).getByRole("button", { name: "Last page" }));
    expect(await screen.findByText("101–120 of 120")).toBeTruthy();
    expect(rows()).toHaveLength(20);
    expect(within(nav).getByRole("button", { name: "Next page" })).toHaveProperty("disabled", true);

    // History: back to page 2, forward to page 5.
    act(() => window.history.back());
    expect(await screen.findByText("26–50 of 120")).toBeTruthy();
    act(() => window.history.forward());
    expect(await screen.findByText("101–120 of 120")).toBeTruthy();

    await user.selectOptions(screen.getByLabelText("Rows per page"), "50");
    expect(await screen.findByText("1–50 of 120")).toBeTruthy();
    expect(srv.calls.at(-1)).toMatchObject({ offset: 0, limit: 50 });
    expect(params().get("size")).toBe("50");
    expect(params().get("page")).toBeNull();
  });

  test("search is debounced into one request and resets the page; filters too", async () => {
    const { srv, user } = setup("?page=3");
    expect(await screen.findByText("51–75 of 120")).toBeTruthy();
    await user.type(screen.getByRole("searchbox", { name: "Search items" }), "item 01");
    expect(await screen.findByText("1–12 of 12")).toBeTruthy();
    // One request for the whole word, not one per key stroke.
    expect(srv.calls.filter((c) => c.q !== "").map((c) => c.q)).toEqual(["item 01"]);
    expect(params().get("q")).toBe("item 01");
    expect(params().get("page")).toBeNull();

    await user.selectOptions(screen.getByLabelText("Kind"), "b");
    await waitFor(() => expect(srv.calls.at(-1)).toMatchObject({ filters: { kind: "b" } }));
    expect(params().get("kind")).toBe("b");

    // No results: one button clears search and filters.
    await user.clear(screen.getByRole("searchbox", { name: "Search items" }));
    await user.type(screen.getByRole("searchbox", { name: "Search items" }), "zzz");
    expect(await screen.findByText("No results")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Clear search and filters" }));
    expect(await screen.findByText("1–25 of 120")).toBeTruthy();
    expect(window.location.search).toBe("");
    expect(screen.getByRole("searchbox", { name: "Search items" })).toHaveProperty("value", "");
  });

  test("a shared link restores the state; an unknown sort key is ignored", async () => {
    const { srv } = setup("?q=item&sort=size&order=desc&page=2&size=50&kind=b");
    await waitFor(() =>
      expect(srv.calls[0]).toEqual({
        q: "item",
        sort: "size",
        order: "desc",
        offset: 50,
        limit: 50,
        filters: { kind: "b" },
      }),
    );
    expect(screen.getByRole("searchbox", { name: "Search items" })).toHaveProperty("value", "item");
    expect(screen.getByLabelText("Kind")).toHaveProperty("value", "b");
    cleanup();

    const other = setup("?sort=evil&size=7&page=-2&kind=zzz");
    await waitFor(() =>
      expect(other.srv.calls[0]).toEqual({
        q: "",
        sort: "name",
        order: "asc",
        offset: 0,
        limit: 25,
        filters: {},
      }),
    );
  });

  test("past the last page (the list shrank): goes to the last page", async () => {
    const { srv } = setup("?page=9");
    expect(await screen.findByText("101–120 of 120")).toBeTruthy();
    expect(srv.calls.map((c) => c.offset)).toEqual([200, 100]);
    expect(params().get("page")).toBe("5");
  });

  test("the previous page stays on screen while the next one loads", async () => {
    const { srv, user } = setup();
    await screen.findByText("1–25 of 120");
    const release = srv.hold();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(srv.calls.at(-1)).toMatchObject({ offset: 25 }));
    // Still the first page, dimmed and busy; no skeleton.
    expect(within(rows()[0]!).getByText("Item 001")).toBeTruthy();
    expect(screen.getByRole("table", { name: "Items" }).getAttribute("aria-busy")).toBe("true");
    release();
    expect(await screen.findByText("26–50 of 120")).toBeTruthy();
    expect(within(rows()[0]!).getByText("Item 026")).toBeTruthy();
  });

  test("row clicks follow the link; ⌘-click and middle-click open a new tab", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    try {
      const { user } = setup();
      await screen.findByText("1–25 of 120");
      await user.keyboard("{Meta>}");
      await user.click(within(rows()[1]!).getByText("a"));
      await user.keyboard("{/Meta}");
      expect(open).toHaveBeenCalledWith(`${BASE}items/i2`, "_blank", "noopener");
      await user.pointer({ keys: "[MouseMiddle]", target: within(rows()[2]!).getByText("a") });
      expect(open).toHaveBeenLastCalledWith(`${BASE}items/i3`, "_blank", "noopener");
      expect(window.location.pathname).toBe(`${BASE}items`);
      await user.click(within(rows()[3]!).getByText("b"));
      expect(window.location.pathname).toBe(`${BASE}items/i4`);
    } finally {
      open.mockRestore();
    }
  });

  test("empty and error states", async () => {
    const empty = setup("", server([]), { empty: true });
    expect(await screen.findByText("Nothing here yet")).toBeTruthy();
    expect(empty.srv.calls).toHaveLength(1);
    cleanup();

    const srv = server();
    srv.fail(new CicordApiError({ code: "unavailable", status: 503, message: "runtime is busy" }));
    const { user } = setup("", srv);
    expect(await screen.findByText("Could not load this list")).toBeTruthy();
    srv.fail(null);
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("1–25 of 120")).toBeTruthy();
  });
});

describe("table helpers", () => {
  test("pageWindow keeps the first, the last and the neighbours", () => {
    expect(pageWindow(1, 5)).toEqual([1, 2, 3, 4, 5]);
    expect(pageWindow(1, 20)).toEqual([1, 2, 3, 4, 5, "gap", 20]);
    expect(pageWindow(10, 20)).toEqual([1, "gap", 9, 10, 11, "gap", 20]);
    expect(pageWindow(20, 20)).toEqual([1, "gap", 16, 17, 18, 19, 20]);
  });

  test("readTableQuery validates every parameter", () => {
    const opts = {
      sortKeys: ["name", "size"],
      defaultSort: "name",
      defaultOrder: "asc" as const,
      defaultLimit: 50,
      filters: [{ id: "kind", label: "", allLabel: "", options: [{ value: "a", label: "" }] }],
    };
    expect(readTableQuery(new URLSearchParams("q=%20x%20&page=3&size=25"), opts)).toEqual({
      q: "x",
      sort: "name",
      order: "asc",
      offset: 50,
      limit: 25,
      filters: {},
    });
    expect(readTableQuery(new URLSearchParams("sort=size&order=up&kind=a"), opts)).toMatchObject({
      sort: "size",
      order: "asc",
      filters: { kind: "a" },
    });
  });
});

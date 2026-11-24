/**
 * Server-side data table (spec/api.md "Data tables"): the server searches, sorts and pages;
 * the table state lives in the URL query string (back/forward and shared links restore it).
 *
 *   ?q=<words>&sort=<key>&order=asc|desc&page=<n>&size=<n>&<filter>=<value>
 *
 * Defaults are left out of the URL. Rows are links: the primary cell renders a real anchor
 * (keyboard, middle-click and ⌘-click work natively); a click anywhere else on the row
 * follows it too.
 */
import type { Page, SortOrder } from "@cicord/client";
import { useCicordQuery } from "@cicord/react";
import {
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  ChevronsUpDown,
  Search,
  X,
} from "lucide-react";
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from "react";
import { useTranslation } from "../i18n/index.ts";
import { rememberList } from "../lib/flash.ts";
import { cn } from "../lib/utils.ts";
import { href as hrefOf, Link, navigate, useLocation } from "../router.tsx";
import { EmptyState } from "./common.tsx";
import { ErrorState } from "./errors.tsx";
import { Button } from "./ui/button.tsx";
import { Input } from "./ui/input.tsx";
import { Skeleton } from "./ui/skeleton.tsx";
import { TableCell, TableHead, TableRow } from "./ui/table.tsx";

export interface DataTableColumn<T> {
  id: string;
  header: string;
  /** Server sort key; the column is not sortable without one. */
  sortKey?: string;
  /** First click sorts descending (counts…). */
  descFirst?: boolean;
  cell: (row: T) => ReactNode;
  /** The cell that holds the row's link (one column). */
  primary?: boolean;
  /** Classes of the column's cells (width, alignment). */
  className?: string;
  /** Classes of the skeleton bar shown while loading (width). */
  skeleton?: string;
}

export interface DataTableFilter {
  /** URL parameter and request parameter. */
  id: string;
  label: string;
  /** Label of the "no filter" option. */
  allLabel: string;
  options: { value: string; label: string }[];
}

/** What the table asks the server for. */
export interface TableQuery {
  q: string;
  sort: string;
  order: SortOrder;
  offset: number;
  limit: number;
  /** Active filters by id (absent: no filter). */
  filters: Record<string, string>;
}

const PAGE_SIZES = [25, 50, 100] as const;

export interface DataTableProps<T> {
  /** Accessible name of the table. */
  label: string;
  columns: DataTableColumn<T>[];
  /** Cache key of a page (nest it under the resource's prefix so invalidations reach it). */
  queryKey: (query: TableQuery) => readonly (string | number | boolean | null | undefined)[];
  fetch: (query: TableQuery, signal: AbortSignal) => Promise<Page<T>>;
  rowKey: (row: T) => string;
  rowHref: (row: T) => string;
  defaultSort: string;
  defaultOrder?: SortOrder;
  defaultLimit?: (typeof PAGE_SIZES)[number];
  search?: { label: string; placeholder: string };
  filters?: DataTableFilter[];
  /** Shown when the list is empty without search or filters. */
  empty: ReactNode;
  /** Extra controls at the end of the toolbar. */
  toolbar?: ReactNode;
  /** Search debounce in ms (default 300). */
  debounce?: number;
  /** Remembers the table's URL under this key (see `listUrl`) for the detail pages' back links. */
  rememberAs?: string;
}

const DEFAULT_LIMIT = 50;

/** Reads the table state from the URL query string. */
export function readTableQuery(
  search: URLSearchParams,
  opts: {
    sortKeys: string[];
    defaultSort: string;
    defaultOrder: SortOrder;
    defaultLimit: number;
    filters: DataTableFilter[];
  },
): TableQuery {
  const rawSort = search.get("sort") ?? "";
  // A hand-edited link with an unknown key would get a 400: fall back to the default.
  const sort = opts.sortKeys.includes(rawSort) ? rawSort : opts.defaultSort;
  const rawOrder = search.get("order");
  const order: SortOrder =
    rawOrder === "asc" || rawOrder === "desc"
      ? rawOrder
      : sort === opts.defaultSort
        ? opts.defaultOrder
        : "asc";
  const size = Number(search.get("size"));
  const limit = (PAGE_SIZES as readonly number[]).includes(size) ? size : opts.defaultLimit;
  const page = Math.max(1, Math.floor(Number(search.get("page")) || 1));
  const filters: Record<string, string> = {};
  for (const f of opts.filters) {
    const v = search.get(f.id);
    // Options not loaded yet (a shared link): trust the URL, the server filters anyway.
    if (v && (f.options.length === 0 || f.options.some((o) => o.value === v))) filters[f.id] = v;
  }
  return {
    q: (search.get("q") ?? "").trim(),
    sort,
    order,
    offset: (page - 1) * limit,
    limit,
    filters,
  };
}

/** Pages to offer around the current one: 1 … 4 5 [6] 7 8 … 20. */
export function pageWindow(current: number, last: number): (number | "gap")[] {
  if (last <= 7) return Array.from({ length: last }, (_, i) => i + 1);
  const out: (number | "gap")[] = [1];
  const start = Math.max(2, Math.min(current - 1, last - 4));
  const end = Math.min(last - 1, Math.max(current + 1, 5));
  if (start > 2) out.push("gap");
  for (let p = start; p <= end; p++) out.push(p);
  if (end < last - 1) out.push("gap");
  out.push(last);
  return out;
}

const INTERACTIVE = "a,button,input,select,textarea,label,[role=button],[role=checkbox]";

export function DataTable<T>(props: DataTableProps<T>) {
  const {
    label,
    columns,
    rowKey,
    rowHref,
    defaultSort,
    defaultOrder = "asc",
    defaultLimit = DEFAULT_LIMIT,
    search: searchProps,
    filters = [],
    empty,
    toolbar,
    debounce = 300,
    rememberAs,
  } = props;
  const { t } = useTranslation();
  const ids = useId();
  const { path, search } = useLocation();
  const sortKeys = columns.flatMap((c) => (c.sortKey ? [c.sortKey] : []));
  const searchKey = search.toString();
  const query = useMemo(
    () =>
      readTableQuery(new URLSearchParams(searchKey), {
        sortKeys,
        defaultSort,
        defaultOrder,
        defaultLimit,
        filters,
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- value-stable keys: sortKeys.join() and JSON.stringify(filters) stand for arrays and objects recreated on every render
    [searchKey, sortKeys.join(), defaultSort, defaultOrder, defaultLimit, JSON.stringify(filters)],
  );

  useEffect(() => {
    if (rememberAs) rememberList(rememberAs, searchKey ? `${path}?${searchKey}` : path);
  }, [rememberAs, path, searchKey]);

  /** Writes table parameters to the URL (null / default values are removed). */
  const update = (patch: Record<string, string | number | null>, replace = false) => {
    // The live URL, not this render's: a debounced search must not undo a filter set meanwhile.
    const next = new URLSearchParams(window.location.search);
    for (const [k, v] of Object.entries(patch)) {
      if (v === null || v === "") next.delete(k);
      else next.set(k, String(v));
    }
    const qs = next.toString();
    navigate(qs ? `${path}?${qs}` : path, { replace, keepScroll: true });
  };

  // ── search box: local text, debounced into ?q= (replace: typing is not history)
  const [text, setText] = useState(query.q);
  const typed = useRef(query.q);
  useEffect(() => {
    // Back/forward or a cleared search: show what the URL says.
    if (query.q !== typed.current.trim()) {
      typed.current = query.q;
      setText(query.q);
    }
  }, [query.q]);
  useEffect(() => {
    const value = text.trim();
    if (value === query.q) return undefined;
    const id = window.setTimeout(() => update({ q: value || null, page: null }, true), debounce);
    return () => window.clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- debounced on the typed text only: query and update are read when the timer fires, listing them would restart the debounce on every navigation
  }, [text, debounce]);

  // ── data: keep the previous page on screen while the next one loads
  const result = useCicordQuery(props.queryKey(query), (signal) => props.fetch(query, signal));
  const last = useRef<Page<T> | undefined>(undefined);
  if (result.data) last.current = result.data;
  const page = result.data ?? (result.status === "error" ? undefined : last.current);
  const stale = !result.data && page !== undefined;

  const pages = page ? Math.max(1, Math.ceil(page.total / query.limit)) : 1;
  const current = Math.floor(query.offset / query.limit) + 1;
  // Past the end (the list shrank, a shared link): go to the last page.
  useEffect(() => {
    if (result.data && result.data.items.length === 0 && result.data.total > 0 && current > 1)
      update({ page: pages > 1 ? pages : null }, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- update is recreated on every render; the check only needs to run when the result or the page count changes
  }, [result.data, current, pages]);

  const filtered = query.q !== "" || Object.keys(query.filters).length > 0;
  const clearAll = () => {
    typed.current = "";
    setText("");
    update(Object.fromEntries([["q", null], ["page", null], ...filters.map((f) => [f.id, null])]));
  };

  const sortBy = (col: DataTableColumn<T>) => {
    if (!col.sortKey) return;
    const order: SortOrder =
      query.sort === col.sortKey
        ? query.order === "asc"
          ? "desc"
          : "asc"
        : col.descFirst
          ? "desc"
          : "asc";
    const isDefault = col.sortKey === defaultSort && order === defaultOrder;
    update({
      sort: col.sortKey === defaultSort ? null : col.sortKey,
      order: isDefault ? null : order,
      page: null,
    });
  };
  const goTo = (p: number) => update({ page: p > 1 ? p : null });

  const follow = (event: MouseEvent<HTMLTableRowElement>, to: string) => {
    if (event.defaultPrevented) return;
    const target = event.target as HTMLElement;
    if (target.closest(INTERACTIVE)) return;
    // Selecting text in a row is not a click.
    if (window.getSelection?.()?.toString()) return;
    if (event.button === 1 || event.metaKey || event.ctrlKey) {
      window.open(hrefOf(to), "_blank", "noopener");
      return;
    }
    if (event.button === 0) navigate(to);
  };

  const colCount = columns.length;
  const skeletonRows = Math.min(query.limit, 8);
  const from = page && page.total ? query.offset + 1 : 0;
  const to = page ? Math.min(query.offset + page.items.length, page.total) : 0;

  let body: ReactNode;
  if (!page && result.status === "error") {
    body = (
      <tr>
        <td colSpan={colCount} className="p-4">
          <ErrorState
            error={result.error}
            title={t("table.loadFailed")}
            onRetry={() => void result.refetch()}
          />
        </td>
      </tr>
    );
  } else if (!page) {
    body = Array.from({ length: skeletonRows }, (_, i) => (
      <TableRow key={`skeleton-${i}`} className="hover:bg-transparent" aria-hidden="true">
        {columns.map((c) => (
          <TableCell key={c.id} className={c.className}>
            {c.primary ? (
              <div className="flex flex-col gap-1.5 py-0.5">
                <Skeleton className="h-3.5 w-40 max-w-full" />
                <Skeleton className="h-3 w-56 max-w-full" />
              </div>
            ) : (
              <Skeleton className={cn("h-3.5", c.skeleton ?? "w-16")} />
            )}
          </TableCell>
        ))}
      </TableRow>
    ));
  } else if (page.total === 0) {
    body = (
      <tr>
        <td colSpan={colCount}>
          {filtered ? (
            <EmptyState
              icon={<Search />}
              title={t("table.noResults.title")}
              action={
                <Button variant="outline" size="sm" onClick={clearAll}>
                  {t("table.noResults.clear")}
                </Button>
              }
            >
              {query.q ? t("table.noResults.query", { query: query.q }) : t("table.noResults.text")}
            </EmptyState>
          ) : (
            empty
          )}
        </td>
      </tr>
    );
  } else {
    body = page.items.map((row) => {
      const href = rowHref(row);
      return (
        <TableRow
          key={rowKey(row)}
          className="cursor-pointer has-[a:focus-visible]:bg-surface-hover"
          onClick={(e) => follow(e, href)}
          onAuxClick={(e) => follow(e, href)}
        >
          {columns.map((c) => (
            <TableCell key={c.id} className={cn("align-top", c.className)}>
              {c.primary ? (
                <Link
                  to={href}
                  className="block rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {c.cell(row)}
                </Link>
              ) : (
                c.cell(row)
              )}
            </TableCell>
          ))}
        </TableRow>
      );
    });
  }

  return (
    <div className="flex flex-col gap-3">
      {searchProps || filters.length || toolbar ? (
        <div className="flex flex-wrap items-end gap-3">
          {searchProps ? (
            <div className="relative w-full max-w-sm">
              <Search
                className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-subtle-foreground"
                aria-hidden="true"
              />
              <label htmlFor={`${ids}-q`} className="sr-only">
                {searchProps.label}
              </label>
              <Input
                id={`${ids}-q`}
                type="search"
                value={text}
                onChange={(e) => {
                  typed.current = e.target.value;
                  setText(e.target.value);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Escape" && text) {
                    e.preventDefault();
                    typed.current = "";
                    setText("");
                  }
                }}
                placeholder={searchProps.placeholder}
                className="pl-8"
              />
            </div>
          ) : null}
          {filters.map((f) => (
            <div key={f.id} className="flex flex-col gap-1">
              <label htmlFor={`${ids}-${f.id}`} className="text-xs text-muted-foreground">
                {f.label}
              </label>
              <NativeSelect
                id={`${ids}-${f.id}`}
                value={query.filters[f.id] ?? ""}
                onChange={(v) => update({ [f.id]: v || null, page: null })}
              >
                <option value="">{f.allLabel}</option>
                {f.options.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </NativeSelect>
            </div>
          ))}
          {filtered ? (
            <Button variant="ghost" size="sm" onClick={clearAll}>
              <X />
              {t("table.clear")}
            </Button>
          ) : null}
          {toolbar ? <div className="ml-auto flex items-center gap-2">{toolbar}</div> : null}
        </div>
      ) : null}

      <div className="overflow-hidden rounded-lg border bg-card">
        <div className="relative w-full overflow-x-auto">
          <table
            className="w-full text-[13px]"
            aria-label={label}
            aria-busy={result.isFetching || undefined}
          >
            <thead className="border-b bg-surface-2/50">
              <TableRow className="hover:bg-transparent">
                {columns.map((c) => {
                  const active = !!c.sortKey && query.sort === c.sortKey;
                  return (
                    <TableHead
                      key={c.id}
                      className={c.className}
                      aria-sort={
                        c.sortKey
                          ? active
                            ? query.order === "asc"
                              ? "ascending"
                              : "descending"
                            : "none"
                          : undefined
                      }
                    >
                      {c.sortKey ? (
                        <button
                          type="button"
                          onClick={() => sortBy(c)}
                          className={cn(
                            "-mx-1 inline-flex items-center gap-1 rounded px-1 py-0.5 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
                            active && "text-foreground",
                          )}
                        >
                          {c.header}
                          {active ? (
                            query.order === "asc" ? (
                              <ArrowUp className="size-3" aria-hidden="true" />
                            ) : (
                              <ArrowDown className="size-3" aria-hidden="true" />
                            )
                          ) : (
                            <ChevronsUpDown
                              className="size-3 text-subtle-foreground"
                              aria-hidden="true"
                            />
                          )}
                        </button>
                      ) : (
                        c.header
                      )}
                    </TableHead>
                  );
                })}
              </TableRow>
            </thead>
            <tbody
              className={cn("transition-opacity [&_tr:last-child]:border-0", stale && "opacity-60")}
            >
              {body}
            </tbody>
          </table>
        </div>
        {page && page.total > 0 ? (
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t px-4 py-2 text-xs text-muted-foreground">
            <span role="status" className="tabular">
              {t("table.range", { from, to, total: page.total })}
            </span>
            <div className="flex items-center gap-2">
              <label htmlFor={`${ids}-size`}>{t("table.rowsPerPage")}</label>
              <NativeSelect
                id={`${ids}-size`}
                value={String(query.limit)}
                onChange={(v) =>
                  update({ size: Number(v) === defaultLimit ? null : v, page: null })
                }
                className="h-7 w-auto"
              >
                {PAGE_SIZES.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </NativeSelect>
            </div>
            {pages > 1 ? (
              <nav aria-label={t("table.pagination")} className="ml-auto">
                <ul className="flex items-center gap-0.5">
                  <li>
                    <PageButton
                      label={t("table.first")}
                      disabled={current === 1}
                      onClick={() => goTo(1)}
                    >
                      <ChevronsLeft />
                    </PageButton>
                  </li>
                  <li>
                    <PageButton
                      label={t("table.previous")}
                      disabled={current === 1}
                      onClick={() => goTo(current - 1)}
                    >
                      <ChevronLeft />
                    </PageButton>
                  </li>
                  {pageWindow(current, pages).map((p, i) =>
                    p === "gap" ? (
                      <li key={`gap-${i}`} aria-hidden="true" className="px-1">
                        …
                      </li>
                    ) : (
                      <li key={p}>
                        <PageButton
                          label={t("table.page", { page: p })}
                          current={p === current}
                          onClick={() => goTo(p)}
                        >
                          {p}
                        </PageButton>
                      </li>
                    ),
                  )}
                  <li>
                    <PageButton
                      label={t("table.next")}
                      disabled={current >= pages}
                      onClick={() => goTo(current + 1)}
                    >
                      <ChevronRight />
                    </PageButton>
                  </li>
                  <li>
                    <PageButton
                      label={t("table.last")}
                      disabled={current >= pages}
                      onClick={() => goTo(pages)}
                    >
                      <ChevronsRight />
                    </PageButton>
                  </li>
                </ul>
              </nav>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function PageButton({
  label,
  current,
  disabled,
  onClick,
  children,
}: {
  label: string;
  current?: boolean;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      variant={current ? "outline" : "ghost"}
      size="icon-sm"
      className={cn("tabular text-xs", current && "text-foreground")}
      aria-label={label}
      aria-current={current ? "page" : undefined}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </Button>
  );
}

/** Native select styled like the inputs (keyboard and screen readers get the platform one). */
function NativeSelect({
  id,
  value,
  onChange,
  children,
  className,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
  className?: string;
}) {
  return (
    <select
      id={id}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={cn(
        "h-8 rounded-md border border-input bg-background px-2 text-[13px] text-foreground outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/25 dark:bg-surface-sunken",
        className,
      )}
    >
      {children}
    </select>
  );
}

import type { HumanError, SyncRecord } from "@cicord/client";
import { errorOf } from "./errors.ts";

/** Consecutive identical history records, newest first. */
export interface HistoryGroup {
  /** Stable React key (id of the newest record). */
  key: string;
  target: string;
  status: SyncRecord["status"];
  /** Newest and oldest record of the run. */
  latest: SyncRecord;
  oldest: SyncRecord;
  records: SyncRecord[];
  count: number;
  /** Distinct revisions of the run, newest first. */
  revisions: string[];
  /** The explained error (failed / rejected runs). */
  error: HumanError | null;
}

function signature(r: SyncRecord): string {
  if (r.status === "succeeded") return `ok:${r.message}`;
  const e = r.error;
  return e ? `${e.code}:${e.message}` : `raw:${r.message}`;
}

/**
 * Groups runs of records (newest first) of the same target with the same status and
 * outcome — "Failed 4 times · last 12 min ago" instead of four rows. A run is broken only
 * by a different outcome on the same target: records of other targets in between (a
 * revision activation, a bot reload) keep their own rows. A group sits at the position
 * of its newest record.
 */
export function groupHistory(records: readonly SyncRecord[]): HistoryGroup[] {
  const groups: HistoryGroup[] = [];
  const open = new Map<string, { sig: string; group: HistoryGroup }>();
  for (const r of records) {
    const sig = `${r.status}|${signature(r)}`;
    const run = open.get(r.target);
    if (run && run.sig === sig) {
      const g = run.group;
      g.records.push(r);
      g.count++;
      g.oldest = r;
      if (r.revision && !g.revisions.includes(r.revision)) g.revisions.push(r.revision);
      continue;
    }
    const group: HistoryGroup = {
      key: String(r.id),
      target: r.target,
      status: r.status,
      latest: r,
      oldest: r,
      records: [r],
      count: 1,
      revisions: r.revision ? [r.revision] : [],
      error: r.status === "succeeded" ? null : errorOf(r),
    };
    open.set(r.target, { sig, group });
    groups.push(group);
  }
  return groups;
}

/** "server:ma-commu" → { kind: "server", key: "ma-commu" }. */
export function parseTarget(target: string): {
  kind: "server" | "bot" | "revision" | "other";
  key: string;
} {
  const i = target.indexOf(":");
  const kind = i < 0 ? target : target.slice(0, i);
  const key = i < 0 ? "" : target.slice(i + 1);
  if (kind === "server" || kind === "bot" || kind === "revision") return { kind, key };
  return { kind: "other", key: target };
}

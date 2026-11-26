import { Buffer } from "node:buffer";
import { randomUUID } from "node:crypto";
import type { Json, KV, KVAtomic, Ranking } from "@cicord/sdk";

const order = (a: string, b: string) => Buffer.compare(Buffer.from(a), Buffer.from(b));
const validKey = (key: string) => {
  if (typeof key !== "string" || !key || Buffer.byteLength(key) > 512)
    throw new TypeError("storage keys must be 1–512 bytes");
};
const finite = (n: number) => {
  if (!Number.isFinite(n)) throw new TypeError("expected a finite number");
  return n;
};

// The runtime's plugin storage limits (internal/state/advanced.go:60-74, enforced by the
// triggers of internal/state/*/migrations/0011_plugin_storage_v2.sql).
const RUNTIME_LIMITS = {
  /** KV entries plus ranking members of one installation. */
  entries: 1_000_000,
  /** Key and value bytes of each entry; name and member bytes plus 16 of each ranking member. */
  bytes: 256 << 20,
  /** Members of one ranking. */
  boardMembers: 250_000,
  /** Key and value bytes of one kv.list page (always at least one entry). */
  listBytes: 4 << 20,
};
const QUOTA = "plugin storage quota exceeded";
const utf8 = (s: string) => Buffer.byteLength(s);

export function testStorage(
  data: Map<string, Json>,
  need: () => void,
  limits: Readonly<typeof RUNTIME_LIMITS> = RUNTIME_LIMITS,
): KV {
  const expiry = new Map<string, number>();
  const versions = new Map<string, string>();
  const rankings = new Map<string, Map<string, number>>();
  let members = 0;
  // Bytes like the database counts them. `weights` follows `data`; entries a test seeds
  // directly in the raw map are weighed on the next write.
  const weights = new Map<string, number>();
  let kvBytes = 0,
    rankBytes = 0;
  const weigh = () => {
    if (weights.size === data.size) return;
    weights.clear();
    kvBytes = 0;
    for (const [k, v] of data) {
      const w = utf8(k) + utf8(JSON.stringify(v));
      weights.set(k, w);
      kvBytes += w;
    }
  };
  const erase = (key: string) => {
    weigh();
    kvBytes -= weights.get(key) ?? 0;
    weights.delete(key);
    data.delete(key);
    versions.delete(key);
    expiry.delete(key);
  };
  const version = (key: string) => {
    validKey(key);
    if ((expiry.get(key) ?? Infinity) <= Date.now()) erase(key);
    return data.has(key) ? (versions.get(key) ?? "legacy") : undefined;
  };
  const read = (key: string) => {
    const v = version(key);
    return v === undefined ? undefined : { version: v, value: structuredClone(data.get(key)!) };
  };
  const checkQuota = () => {
    if (data.size + members > limits.entries || kvBytes + rankBytes > limits.bytes)
      throw new Error(QUOTA);
  };
  const put = (key: string, value: Json, ttl?: number) => {
    validKey(key);
    const raw = JSON.stringify(value);
    if (raw === undefined || utf8(raw) > 1 << 20) throw new TypeError("invalid KV value");
    if (ttl !== undefined && (!Number.isFinite(ttl) || ttl < 0 || ttl >= 9223372036.854776))
      throw new TypeError("invalid TTL");
    weigh();
    // Like the insert and update triggers: the write that crosses a quota fails.
    const weight = utf8(key) + utf8(raw),
      before = weights.get(key) ?? 0;
    if (!data.has(key) && data.size + members >= limits.entries) throw new Error(QUOTA);
    if (kvBytes - before + weight + rankBytes > limits.bytes) throw new Error(QUOTA);
    kvBytes += weight - before;
    weights.set(key, weight);
    data.set(key, JSON.parse(raw));
    versions.set(key, randomUUID());
    if (ttl) expiry.set(key, Date.now() + ttl * 1000);
    else expiry.delete(key);
  };
  const increment = (key: string, by: number) => {
    const e = read(key),
      until = expiry.get(key);
    if (e && typeof e.value !== "number") throw new TypeError("stored value is not a number");
    const next = finite(((e?.value as number | undefined) ?? 0) + finite(by));
    put(key, next);
    if (until !== undefined) expiry.set(key, until);
    return next;
  };
  const rankWrite = (name: string, member: string, score?: number) => {
    validKey(name);
    validKey(member);
    let board = rankings.get(name);
    const weight = utf8(name) + utf8(member) + 16;
    if (score === undefined) {
      if (board?.delete(member)) {
        members--;
        rankBytes -= weight;
      }
      if (board?.size === 0) rankings.delete(name);
      return;
    }
    finite(score);
    if (!board?.has(member)) {
      weigh();
      if ((board?.size ?? 0) >= limits.boardMembers)
        throw new Error("ranking member limit exceeded");
      if (data.size + members >= limits.entries || kvBytes + rankBytes + weight > limits.bytes)
        throw new Error(QUOTA);
      members++;
      rankBytes += weight;
    }
    if (!board) {
      board = new Map();
      rankings.set(name, board);
    }
    board.set(member, score);
  };
  return {
    async get<T extends Json>(key: string) {
      need();
      return read(key)?.value as T | undefined;
    },
    getEntry: (async (key: string) => {
      need();
      return read(key);
    }) as KV["getEntry"],
    async getMany<T extends Json>(keys: readonly string[]) {
      need();
      if (keys.length > 128) throw new TypeError("kv.getMany: at most 128 keys");
      const values = keys.map((k) => read(k)?.value as T | undefined);
      if (Buffer.byteLength(JSON.stringify(values)) > 4 << 20)
        throw new TypeError("kv.getMany: result exceeds 4 MiB");
      return values;
    },
    async set(key, value, opts) {
      need();
      put(key, value, opts?.ttlSeconds);
    },
    async delete(key) {
      need();
      validKey(key);
      erase(key);
    },
    async increment(key, by = 1) {
      need();
      return increment(key, by);
    },
    async list<T extends Json>(opts: { prefix?: string; limit?: number; cursor?: string } = {}) {
      need();
      const limit = opts.limit ?? 100;
      if (!Number.isInteger(limit) || limit < 1 || limit > 1000)
        throw new TypeError("invalid page size");
      const keys = [...data.keys()]
        .filter(
          (k) => k.startsWith(opts.prefix ?? "") && order(k, opts.cursor ?? "") > 0 && version(k),
        )
        .toSorted(order);
      // Cut by count and by bytes like the runtime (internal/state/internal/advanced/
      // space.go:340-360): the page ends before the entry that would cross 4 MiB.
      const items: { key: string; value: T }[] = [];
      let size = 0,
        more = false;
      for (const key of keys) {
        const value = read(key)!.value as T;
        const weight = utf8(key) + utf8(JSON.stringify(value));
        if (items.length === limit || (items.length > 0 && size + weight > limits.listBytes)) {
          more = true;
          break;
        }
        size += weight;
        items.push({ key, value });
      }
      return more ? { items, cursor: items.at(-1)!.key } : { items };
    },
    atomic() {
      const checks: Array<() => boolean> = [],
        writes: Array<() => void> = [];
      const touched = new Set<string>(),
        boards = new Set<string>();
      let done = false;
      const admit = () => {
        // sdk.js and space.go:186: at most 128 checks and mutations per transaction.
        if (done || checks.length + writes.length >= 128)
          throw new TypeError("kv.atomic: closed or full transaction");
      };
      const mutate = (key: string, op: () => void, board?: string) => {
        admit();
        if (board === undefined) touched.add(key);
        else boards.add(board);
        writes.push(op);
        return builder;
      };
      const builder: KVAtomic = {
        check(key, expected) {
          admit();
          checks.push(() => version(key) === (expected ?? undefined));
          return this;
        },
        set(key, value, opts) {
          return mutate(key, () => put(key, value, opts?.ttlSeconds));
        },
        delete(key) {
          return mutate(key, () => {
            validKey(key);
            erase(key);
          });
        },
        increment(key, by = 1) {
          return mutate(key, () => increment(key, by));
        },
        rankSet(name, member, score) {
          return mutate(member, () => rankWrite(name, member, finite(score)), name);
        },
        rankRemove(name, member) {
          return mutate(member, () => rankWrite(name, member), name);
        },
        async commit() {
          need();
          if (done) throw new TypeError("transaction already committed");
          done = true;
          if (!checks.every((check) => check())) return { ok: false };
          weigh();
          const before = [...touched].map(
            (k) => [k, data.get(k), versions.get(k), expiry.get(k), weights.get(k)] as const,
          );
          const previous = [...boards].map((n) => [n, new Map(rankings.get(n))] as const);
          const count = members,
            savedKv = kvBytes,
            savedRank = rankBytes;
          try {
            for (const op of writes) op();
            checkQuota();
          } catch (error) {
            for (const [k, v, ver, exp, w] of before) {
              erase(k);
              if (v !== undefined) data.set(k, v);
              if (w !== undefined) weights.set(k, w);
              if (ver !== undefined) versions.set(k, ver);
              if (exp !== undefined) expiry.set(k, exp);
            }
            for (const [n, b] of previous) {
              if (b.size) rankings.set(n, b);
              else rankings.delete(n);
            }
            members = count;
            kvBytes = savedKv;
            rankBytes = savedRank;
            throw error;
          }
          return { ok: true };
        },
      };
      return builder;
    },
    ranking(name): Ranking {
      validKey(name);
      const list = () =>
        [...(rankings.get(name) ?? [])]
          .toSorted(([a, x], [b, y]) => y - x || order(a, b))
          .map(([member, score], i) => ({ member, score, rank: i + 1 }));
      return {
        async set(member, score) {
          need();
          rankWrite(name, member, finite(score));
          return score;
        },
        async add(member, delta) {
          need();
          const score = finite((rankings.get(name)?.get(member) ?? 0) + finite(delta));
          rankWrite(name, member, score);
          return score;
        },
        async remove(member) {
          need();
          rankWrite(name, member);
        },
        async count() {
          need();
          return rankings.get(name)?.size ?? 0;
        },
        async score(member) {
          need();
          validKey(member);
          return rankings.get(name)?.get(member);
        },
        async rank(member) {
          need();
          validKey(member);
          return list().find((x) => x.member === member)?.rank;
        },
        async top({ limit = 100, offset = 0 } = {}) {
          need();
          if (
            !Number.isInteger(limit) ||
            limit < 0 ||
            limit > 1000 ||
            !Number.isInteger(offset) ||
            offset < 0 ||
            offset > 2147483647
          )
            throw new TypeError("invalid page");
          return list().slice(offset, offset + limit);
        },
        async around(member, radius) {
          need();
          validKey(member);
          if (!Number.isInteger(radius) || radius < 0 || radius > 499)
            throw new TypeError("invalid radius");
          const all = list(),
            i = all.findIndex((x) => x.member === member);
          return i < 0 ? [] : all.slice(Math.max(0, i - radius), i + radius + 1);
        },
      };
    },
  };
}

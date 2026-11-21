import { CodiApiError, type SyncRecord } from "@codi/client";
import { describe, expect, test } from "vite-plus/test";
import en from "../src/i18n/en.json";
import { formatRelative, i18n } from "../src/i18n/index.ts";
import { cleanMessage, describeError, errorOf, explainRaw } from "../src/lib/errors.ts";
import { groupHistory, parseTarget } from "../src/lib/history.ts";

const RAW =
  'reconcile ma-commu: 1 change(s) failed: create channel news: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2}."}]}})';

describe("i18n", () => {
  test("plurals, interpolation and relative time", () => {
    expect(i18n.t("sync.toast.applied", { count: 1 })).toBe("Applied 1 change");
    expect(i18n.t("sync.toast.applied", { count: 4 })).toBe("Applied 4 changes");
    expect(i18n.t("history.repeated", { status: "Failed", count: 4 })).toBe("Failed 4 times");
    expect(i18n.t("switcher.label", { name: "Ma Commu" })).toBe("Server: Ma Commu. Switch server");
    const now = Date.parse("2026-11-21T10:00:00Z");
    expect(formatRelative("2026-11-21T09:48:00Z", now)).toBe("12 minutes ago");
    expect(formatRelative("2026-11-21T09:59:58Z", now)).toBe("just now");
    expect(formatRelative("2026-11-20T10:00:00Z", now)).toBe("yesterday");
    // A server clock slightly ahead of the browser is not "in 11 seconds".
    expect(formatRelative("2026-11-21T10:00:11Z", now)).toBe("just now");
    expect(formatRelative("2026-11-21T12:00:00Z", now)).toBe("in 2 hours");
  });

  test("every leaf is a non-empty string and plural pairs are complete", () => {
    const leaves: [string, unknown][] = [];
    const walk = (o: Record<string, unknown>, p: string) => {
      for (const [k, v] of Object.entries(o)) {
        if (v && typeof v === "object") walk(v as Record<string, unknown>, `${p}${k}.`);
        else leaves.push([`${p}${k}`, v]);
      }
    };
    walk(en, "");
    expect(leaves.length).toBeGreaterThan(300);
    for (const [k, v] of leaves) {
      expect(typeof v === "string" && v.length > 0, k).toBe(true);
      if (k.endsWith("_one"))
        expect(
          leaves.some(([o]) => o === `${k.slice(0, -4)}_other`),
          k,
        ).toBe(true);
    }
  });
});

describe("error mapping", () => {
  test("raw Discord errors are cleaned for the main text, kept in details", () => {
    expect(cleanMessage(RAW)).toBe("Create channel news: Invalid Form Body");
    const e = explainRaw(RAW);
    expect(e.message).not.toMatch(/[{}]/);
    expect(e.details).toBe(RAW);
    expect(errorOf({ message: "" })).toBeNull();
    expect(errorOf({ message: RAW, error: { code: "x", message: "Explained" } })?.message).toBe(
      "Explained",
    );
  });

  test("API errors map to distinct, friendly messages", () => {
    const network = describeError(
      new CodiApiError({ code: "network", status: 0, message: "fetch failed" }),
    );
    expect(network.kind).toBe("network");
    expect(network.title).toBe("Can't reach the runtime");

    expect(
      describeError(
        new CodiApiError({ code: "unauthenticated", status: 401, message: "login required" }),
      ).title,
    ).toBe("Your session expired");
    const forbidden = describeError(
      new CodiApiError({
        code: "forbidden",
        status: 403,
        message: "requires the admin role on server ma-commu",
      }),
    );
    expect(forbidden.title).toBe("Not available with your role");
    expect(forbidden.message).toBe("requires the admin role on server ma-commu");
    expect(forbidden.hint).toMatch(/spec.access/);

    expect(
      describeError(new CodiApiError({ code: "conflict", status: 409, message: "changed" })).hint,
    ).toMatch(/Reload/);
    const invalid = describeError(
      new CodiApiError({
        code: "invalid",
        status: 422,
        message: "the configuration is invalid",
        diagnostics: [
          { severity: "error", message: "x" },
          { severity: "error", message: "y" },
        ],
      }),
    );
    expect(invalid.title).toBe("2 values are invalid");

    // A plan / sync failure explained by the runtime.
    const sync = describeError(
      new CodiApiError({
        code: "unavailable",
        status: 503,
        message: "Announcement and stage channels need the Community feature",
        hint: "Enable Community",
        details: `sync: ${RAW}`,
        reason: "community_required",
      }),
    );
    expect(sync.title).toBe("Discord refused the change");
    expect(sync.message).toBe("Announcement and stage channels need the Community feature");
    expect(sync.hint).toBe("Enable Community");
    expect(sync.details).toBe(`sync: ${RAW}`);

    // An older runtime sending the raw text: no JSON in the main text.
    const old = describeError(
      new CodiApiError({ code: "unavailable", status: 503, message: `sync: ${RAW}` }),
    );
    expect(old.message).not.toMatch(/[{}]/);
    expect(old.details).toBe(`sync: ${RAW}`);

    const internal = describeError(
      new CodiApiError({ code: "internal", status: 500, message: "internal error" }),
    );
    expect(internal.message).toMatch(/bug in CODI/);
    expect(describeError(new Error("boom")).message).toBe("Boom");
  });
});

describe("history grouping", () => {
  const rec = (
    id: number,
    status: SyncRecord["status"],
    message: string,
    target = "server:s",
    revision = "r1",
  ): SyncRecord => ({
    id,
    time: new Date(Date.UTC(2026, 10, 21, 10, 60 - id)).toISOString(),
    revision,
    target,
    status,
    message,
  });

  test("records of other targets in between do not break a run", () => {
    const groups = groupHistory([
      rec(9, "failed", RAW),
      rec(8, "succeeded", "working tree", "revision"),
      rec(7, "failed", RAW),
    ]);
    expect(groups.map((g) => [g.target, g.count])).toEqual([
      ["server:s", 2],
      ["revision", 1],
    ]);
  });

  test("consecutive identical results are grouped, a different outcome breaks the run", () => {
    const groups = groupHistory([
      rec(9, "failed", RAW, "server:s", "r2"),
      rec(8, "failed", RAW),
      rec(7, "failed", RAW),
      rec(6, "succeeded", "3 change(s) applied"),
      rec(5, "failed", RAW),
      rec(4, "succeeded", "bot reloaded", "bot:b"),
      rec(3, "succeeded", "bot reloaded", "bot:b"),
    ]);
    expect(groups.map((g) => [g.status, g.count])).toEqual([
      ["failed", 3],
      ["succeeded", 1],
      ["failed", 1],
      ["succeeded", 2],
    ]);
    expect(groups[0]!.revisions).toEqual(["r2", "r1"]);
    expect(groups[0]!.latest.id).toBe(9);
    expect(groups[0]!.oldest.id).toBe(7);
    expect(groups[0]!.error?.message).not.toMatch(/[{}]/);
  });

  test("different explained errors are not merged", () => {
    const a = { ...rec(2, "failed", "x"), error: { code: "missing_permissions", message: "A" } };
    const b = { ...rec(1, "failed", "x"), error: { code: "missing_permissions", message: "B" } };
    expect(groupHistory([a, b])).toHaveLength(2);
    expect(parseTarget("server:ma-commu")).toEqual({ kind: "server", key: "ma-commu" });
    expect(parseTarget("revision")).toEqual({ kind: "revision", key: "" });
  });
});

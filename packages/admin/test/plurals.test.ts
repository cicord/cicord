import { expect, test } from "vite-plus/test";
import en from "../src/i18n/en.json";
import { i18n } from "../src/i18n/index.ts";

test("0.2.0 counts use plural forms", () => {
  const counts = (n: number) =>
    ["records", "rankings", "index", "tasks"]
      .map((k) => i18n.t(`orphanedData.${k}` as "orphanedData.tasks", { count: n }))
      .join(", ");
  expect(counts(1)).toBe("1 record, 1 ranking entry, 1 index document, 1 task");
  expect(counts(2)).toBe("2 records, 2 ranking entries, 2 index documents, 2 tasks");
  expect(i18n.t("diagnostics.attempts", { count: 1 })).toBe("1 attempt");
  expect(i18n.t("diagnostics.attempts", { count: 3 })).toBe("3 attempts");
  expect(
    i18n.t("store.update.secretBudgetValue", { provider: "openai", count: 1, field: "budget" }),
  ).toBe("openai, 1 token per day (budget)");
});

// A count followed by a plural noun needs `_one`/`_other` forms. Fixed limits that are
// never 1 are listed here.
const FIXED = new Set(["analytics.custom.errors.tooLong"]);

test("no English count string lacks its plural forms", () => {
  const missing: string[] = [];
  const walk = (node: unknown, path: string[]) => {
    if (typeof node === "string") {
      const key = path.join(".");
      const plural = /_(zero|one|two|few|many|other)$/.test(key);
      if (!plural && !FIXED.has(key) && /\{\{count\}\} [a-z]+s\b/i.test(node)) missing.push(key);
      return;
    }
    if (node && typeof node === "object")
      for (const [k, v] of Object.entries(node)) walk(v, [...path, k]);
  };
  walk(en, []);
  expect(missing).toEqual([]);
});

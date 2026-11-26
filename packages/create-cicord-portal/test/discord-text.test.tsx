import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vite-plus/test";
import { DiscordText } from "../src/components/DiscordText.tsx";

afterEach(cleanup);
const fallback = { user: "user", role: "role", channel: "channel" };
const names = {
  users: new Map([["111111111111111111", "June"]]),
  roles: new Map([["222222222222222222", "Support"]]),
};
const show = (content: string) =>
  render(
    <p>
      <DiscordText content={content} names={names} fallback={fallback} />
    </p>,
  ).container.firstElementChild as HTMLElement;

test("mentions are named chips, and unknown ones never show their ID", () => {
  const p = show(
    "<@111111111111111111> asked <@&222222222222222222>, <@!333333333333333333>, <@&444444444444444444> in <#555555555555555555> :) <:wave:666666666666666666> </ticket open:777777777777777777>",
  );
  const chips = [...p.querySelectorAll(".mention")].map((c) => c.textContent);
  expect(chips).toEqual(["@June", "@Support", "@user", "@role", "#channel", "/ticket open"]);
  expect(p.textContent).toContain(":wave:");
  expect(p.textContent).not.toMatch(/\d{5,}|<|>/);
});

test("minimal markdown and line breaks, with HTML kept as text", () => {
  const p = show(
    "**Hello** *there* _you_ __under__ `x **y**`\nline two <img src=x onerror=alert(1)>\n```js\n**raw** <@111111111111111111>\n```",
  );
  expect(p.querySelector("strong")?.textContent).toBe("Hello");
  expect([...p.querySelectorAll("em")].map((e) => e.textContent)).toEqual(["there", "you"]);
  expect(p.querySelector("u")?.textContent).toBe("under");
  expect(p.querySelector("code")?.textContent).toBe("x **y**");
  expect(p.querySelectorAll("br")).toHaveLength(2);
  expect(p.querySelector("img")).toBeNull();
  expect(p.textContent).toContain("<img src=x onerror=alert(1)>");
  const block = p.querySelector(".code-block");
  expect(block?.textContent).toBe("**raw** <@111111111111111111>");
  expect(block?.querySelector(".mention, strong")).toBeNull();
});

test("bold wraps a mention", () => {
  const p = show("**ping <@111111111111111111>**");
  expect(p.querySelector("strong .mention")?.textContent).toBe("@June");
});

test("an unclosed marker stays literal and stays fast", () => {
  const start = performance.now();
  const p = show("*".repeat(20000) + "_".repeat(20000) + "`".repeat(20000));
  expect(performance.now() - start).toBeLessThan(2000);
  expect(p.textContent?.length).toBeGreaterThan(0);
});

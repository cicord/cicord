import type { Json, ServerResources } from "@codi/client";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, test, vi } from "vite-plus/test";
import onboardingSchema from "../../../examples/basic/plugins/onboarding/config.schema.json";
import welcomeSchema from "../../../examples/basic/plugins/welcome/config.schema.json";
import {
  changedTopLevel,
  cleanMessage,
  collectPointers,
  configPointer,
  diffConfig,
  editableFor,
  initialValue,
  isDuration,
  Markdown,
  mapDiagnostics,
  SchemaForm,
  setIn,
  validate,
  type JsonObject,
  type JsonSchema,
} from "../src/schema-form/index.ts";

// The real example schemas (examples/basic/plugins/*/config.schema.json).
const welcome = welcomeSchema as unknown as JsonSchema;
const onboarding = onboardingSchema as unknown as JsonSchema;

const resources: ServerResources = {
  roles: [
    { key: "admin", id: "1", name: "Admin", color: "#e74c3c", managed: true },
    { key: "member", id: "2", name: "Membre", color: "#3498db", managed: true },
    { key: "minecraft", id: "3", name: "Minecraft", color: "#5b8731", managed: true },
    { key: "pc", id: "4", name: "PC", color: "#95a5a6", managed: true },
    { key: "", id: "9", name: "Old VIP", color: "#ad1457", managed: false },
  ],
  categories: [
    { key: "info", id: "c1", name: "Infos", managed: true },
    { key: "community", id: "c2", name: "Communauté", managed: true },
  ],
  channels: [
    { key: "rules", id: "10", name: "règlement", type: "text", category: "info", managed: true },
    {
      key: "announcements",
      id: "11",
      name: "annonces",
      type: "announcement",
      category: "info",
      managed: true,
    },
    {
      key: "general",
      id: "12",
      name: "général",
      type: "text",
      category: "community",
      managed: true,
    },
    { key: "", id: "13", name: "memes", type: "text", category: "community", managed: false },
  ],
};

const welcomeConfig: JsonObject = {
  channel: "general",
  message: "Bienvenue {member} sur {server} !",
  autoRole: "member",
  dmOnboarding: true,
};

const onboardingConfig: JsonObject = {
  title: "Bienvenue !",
  intro: "",
  steps: [
    {
      id: "jeux",
      title: "À quoi tu joues ?",
      multiple: true,
      options: [{ role: "minecraft", emoji: "⛏️" }],
    },
    { id: "plateforme", title: "Plateforme", required: true, options: [{ role: "pc" }] },
  ],
  completedRole: "member",
  successMessage: "OK",
  linkMessage: "Go",
  buttonLabel: "Open",
};

afterEach(cleanup);

/** Controlled harness that records every change. */
function Harness(props: {
  schema: JsonSchema;
  initial: JsonObject;
  editable?: string[];
  errors?: Record<string, string[]>;
  onValue?: (v: JsonObject) => void;
}) {
  const [value, setValue] = useState(props.initial);
  return (
    <SchemaForm
      schema={props.schema}
      value={value}
      baseline={props.initial}
      onChange={(v) => {
        setValue(v);
        props.onValue?.(v);
      }}
      resources={resources}
      {...(props.editable ? { editable: props.editable } : {})}
      {...(props.errors ? { errors: props.errors } : {})}
    />
  );
}

describe("rendering the welcome schema", () => {
  test("renders every property with its widget, description and required marker", () => {
    render(<Harness schema={welcome} initial={welcomeConfig} />);
    const channel = screen.getByRole("combobox", { name: "Channel" });
    expect(channel.textContent).toContain("général");
    expect(screen.getByText("Channel where welcome messages are posted.")).toBeTruthy();
    // x-codi-widget: textarea
    const message = screen.getByRole("textbox", { name: "Message" });
    expect(message.tagName).toBe("TEXTAREA");
    expect((message as HTMLTextAreaElement).value).toBe("Bienvenue {member} sur {server} !");
    expect(screen.getByRole("combobox", { name: "Auto role" }).textContent).toContain("Membre");
    expect(screen.getByRole("switch", { name: "Dm onboarding" }).getAttribute("aria-checked")).toBe(
      "true",
    );
    // "channel" is required, "autoRole" is not.
    const channelField = channel.closest(".sf-field")!;
    expect(within(channelField as HTMLElement).getByText("Required")).toBeTruthy();
  });

  test("channel picker lists managed channels by category and stores the key", async () => {
    const user = userEvent.setup();
    const onValue = vi.fn();
    render(<Harness schema={welcome} initial={welcomeConfig} onValue={onValue} />);
    await user.click(screen.getByRole("combobox", { name: "Channel" }));
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    // Unmanaged channels have no key: not selectable.
    expect(options.some((o) => o?.includes("memes"))).toBe(false);
    expect(screen.getByText("Infos")).toBeTruthy();
    await user.click(screen.getByRole("option", { name: /annonces/ }));
    expect(onValue).toHaveBeenLastCalledWith({ ...welcomeConfig, channel: "announcements" });
    expect(screen.getByRole("combobox", { name: "Channel" }).textContent).toContain("annonces");
  });

  test("role picker shows colors, filters by name and can be cleared when optional", async () => {
    const user = userEvent.setup();
    const onValue = vi.fn();
    render(<Harness schema={welcome} initial={welcomeConfig} onValue={onValue} />);
    await user.click(screen.getByRole("combobox", { name: "Auto role" }));
    await user.type(screen.getByRole("textbox", { name: "Filter options" }), "mine");
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]!.querySelector(".sf-swatch")).toBeTruthy();
    await user.keyboard("{Enter}");
    expect(onValue).toHaveBeenLastCalledWith({ ...welcomeConfig, autoRole: "minecraft" });

    await user.click(screen.getByRole("combobox", { name: "Auto role" }));
    await user.click(screen.getByRole("option", { name: "None" }));
    // Optional field without default: the key is removed.
    const last = onValue.mock.lastCall![0] as JsonObject;
    expect("autoRole" in last).toBe(false);
  });

  test("fields outside `editable` are read-only (moderator view)", () => {
    render(<Harness schema={welcome} initial={welcomeConfig} editable={["message"]} />);
    expect((screen.getByRole("combobox", { name: "Channel" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(
      (screen.getByRole("switch", { name: "Dm onboarding" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).disabled).toBe(
      false,
    );
    expect(screen.getAllByText("Admins only")).toHaveLength(3);
  });

  test("editableFor mirrors x-codi-access", () => {
    expect(editableFor(welcome, "admin")).toEqual([
      "autoRole",
      "channel",
      "dmOnboarding",
      "message",
    ]);
    expect(editableFor(welcome, "moderator")).toEqual(["message"]);
    expect(editableFor(onboarding, "moderator")).toEqual([
      "buttonLabel",
      "intro",
      "linkMessage",
      "steps",
      "successMessage",
      "title",
    ]);
  });

  test("marks modified fields and shows field errors", async () => {
    const user = userEvent.setup();
    render(
      <Harness schema={welcome} initial={welcomeConfig} errors={{ "/message": ["Too long"] }} />,
    );
    expect(screen.getByRole("alert").textContent).toBe("Too long");
    expect(screen.queryByText("Modified")).toBeNull();
    await user.click(screen.getByRole("switch", { name: "Dm onboarding" }));
    expect(screen.getByText("Modified")).toBeTruthy();
  });
});

describe("rendering the onboarding schema (arrays of objects)", () => {
  test("steps[].options[] can be expanded, added, reordered and removed", async () => {
    const user = userEvent.setup();
    let value: JsonObject = onboardingConfig;
    render(<Harness schema={onboarding} initial={onboardingConfig} onValue={(v) => (value = v)} />);

    // Collapsed headers summarise each item.
    const step1 = screen.getByRole("button", { name: /Step 1 À quoi tu joues \?/ });
    expect(screen.getByRole("button", { name: /Step 2 Plateforme/ })).toBeTruthy();
    await user.click(step1);
    expect(step1.getAttribute("aria-expanded")).toBe("true");
    // The single option of step 1 is open and shows a role picker resolved to its name.
    expect(screen.getByRole("combobox", { name: "Role" }).textContent).toContain("Minecraft");

    await user.click(screen.getByRole("button", { name: "Add option" }));
    const steps = value["steps"] as JsonObject[];
    expect((steps[0]!["options"] as Json[]).length).toBe(2);
    // New items get their required properties.
    expect((steps[0]!["options"] as Json[])[1]).toEqual({ role: "" });

    await user.click(screen.getByRole("button", { name: "Move step 1 down" }));
    expect((value["steps"] as JsonObject[]).map((s) => s["id"])).toEqual(["plateforme", "jeux"]);

    await user.click(screen.getByRole("button", { name: "Remove step 2" }));
    expect((value["steps"] as JsonObject[]).map((s) => s["id"])).toEqual(["plateforme"]);
    // minItems: 1 → the last step cannot be removed.
    expect(
      (screen.getByRole("button", { name: "Remove step 1" }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  test("adding a step creates the required structure", () => {
    const item = initialValue(onboarding.properties!["steps"]!.items!);
    expect(item).toEqual({
      id: "",
      title: "",
      description: "",
      multiple: false,
      required: false,
      options: [{ role: "" }],
    });
  });

  test("string defaults are placeholders and counters appear near maxLength", () => {
    render(
      <Harness schema={onboarding} initial={{ ...onboardingConfig, title: "x".repeat(90) }} />,
    );
    expect(screen.getByText("90/100")).toBeTruthy();
  });
});

describe("diff", () => {
  test("reports leaf changes with pointers", () => {
    const next = setIn(
      setIn(onboardingConfig, ["steps", 0, "title"], "Tu joues à quoi ?"),
      ["intro"],
      "Hello",
    ) as JsonObject;
    const changes = diffConfig(onboardingConfig, next);
    expect(changes.map((c) => [c.pointer, c.kind, c.from, c.to])).toEqual([
      ["/intro", "changed", "", "Hello"],
      ["/steps/0/title", "changed", "À quoi tu joues ?", "Tu joues à quoi ?"],
    ]);
    expect(changedTopLevel(changes)).toEqual(["intro", "steps"]);
  });

  test("added / removed keys and array items", () => {
    const next: JsonObject = {
      ...onboardingConfig,
      steps: [(onboardingConfig["steps"] as Json[])[0]!],
    };
    delete next["completedRole"];
    const changes = diffConfig(onboardingConfig, next);
    expect(changes.map((c) => [c.pointer, c.kind])).toEqual([
      ["/steps/1", "removed"],
      ["/completedRole", "removed"],
    ]);
    expect(diffConfig({ a: 1 }, { a: 1, b: [1] }).map((c) => [c.pointer, c.kind])).toEqual([
      ["/b", "added"],
    ]);
  });

  test("arrays of primitives are one change; equal values none", () => {
    expect(diffConfig({ a: ["x"] }, { a: ["x", "y"] })).toEqual([
      { pointer: "/a", path: ["a"], kind: "changed", from: ["x"], to: ["x", "y"] },
    ]);
    expect(diffConfig(onboardingConfig, structuredClone(onboardingConfig))).toEqual([]);
  });
});

describe("validation", () => {
  test("catches required, pattern, minItems and unknown references", () => {
    const bad = {
      ...onboardingConfig,
      steps: [{ id: "Bad Id", title: "", options: [{ role: "xbox" }] }],
      completedRole: "nope",
    } as JsonObject;
    const errors = validate(onboarding, bad, resources);
    const byPointer = Object.fromEntries(errors.map((e) => [e.pointer, e.message]));
    expect(byPointer["/steps/0/id"]).toMatch(/must match/i);
    expect(byPointer["/steps/0/title"]).toBe("Required");
    expect(byPointer["/steps/0/options/0/role"]).toBe('Unknown role "xbox"');
    expect(byPointer["/completedRole"]).toBe('Unknown role "nope"');
    expect(validate(onboarding, { ...onboardingConfig, steps: [] }, resources)).toEqual([
      { pointer: "/steps", message: "Add at least one item" },
    ]);
    expect(validate(welcome, { message: "x", dmOnboarding: true }, resources)).toEqual([
      { pointer: "/channel", message: "Required" },
    ]);
    expect(validate(onboarding, onboardingConfig, resources)).toEqual([]);
  });

  test("duration widget", () => {
    expect(isDuration("1h30m")).toBe(true);
    expect(isDuration("10 minutes")).toBe(false);
  });
});

describe("diagnostics mapping", () => {
  test("strips bot.yaml prefixes", () => {
    expect(configPointer("/spec/plugins/1/overrides/ma-commu/config/steps/0/title")).toBe(
      "/steps/0/title",
    );
    expect(configPointer("/spec/plugins/0/config")).toBe("");
    expect(configPointer("/channel")).toBe("/channel");
    expect(cleanMessage("[server ma-commu] unknown role")).toBe("unknown role");
  });

  test("maps to rendered fields, climbing to the closest known one", () => {
    const known = collectPointers(onboarding, onboardingConfig);
    expect(known.has("/steps/1/options/0/role")).toBe(true);
    const mapped = mapDiagnostics(
      [
        {
          severity: "error",
          path: "/spec/plugins/1/overrides/ma-commu/config/steps/1/options/0/role",
          message: '[server ma-commu] unknown role "xbox"',
        },
        { severity: "error", path: "/spec/plugins/1/config/steps/0/nope", message: "extra field" },
        { severity: "error", path: "/spec/plugins/1/config", message: "missing steps" },
        { severity: "warning", path: "/spec/plugins/1/config/title", message: "ignored" },
        { severity: "error", message: "general failure" },
      ],
      known,
    );
    expect(mapped.byField).toEqual({
      "/steps/1/options/0/role": ['unknown role "xbox"'],
      "/steps/0": ["extra field"],
    });
    expect(mapped.general).toEqual(["missing steps", "general failure"]);
  });
});

describe("markdown", () => {
  test("renders formatting, mentions and only safe links", () => {
    render(
      <Markdown
        source={
          "**Hi** <@42> in <#7>\n[portal](https://example.org/onboarding?codi_token=x) [bad](javascript:alert(1)) {member}"
        }
        placeholders
        resolveMention={(kind, id) =>
          kind === "user" && id === "42" ? "Alice" : kind === "channel" ? "général" : undefined
        }
      />,
    );
    expect(screen.getByText("Hi").tagName).toBe("STRONG");
    expect(screen.getByText("Alice", { exact: false }).textContent).toBe("@Alice");
    expect(screen.getByText("général", { exact: false }).textContent).toBe("#général");
    const link = screen.getByRole("link", { name: "portal" });
    expect(link.getAttribute("href")).toBe("https://example.org/onboarding?codi_token=x");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(screen.queryByRole("link", { name: "bad" })).toBeNull();
    expect(screen.getByText("{member}").className).toBe("md-placeholder");
  });
});

describe("every widget kind (synthetic schema)", () => {
  const kitchen: JsonSchema = {
    type: "object",
    required: ["count"],
    properties: {
      count: { type: "integer", minimum: 1, maximum: 10, description: "How many." },
      ratio: { type: "number" },
      mode: { type: "string", enum: ["fast", "slow"] },
      level: { type: "string", enum: ["one", "two", "three", "four", "five"] },
      parent: { type: "string", "x-codi-ref": "category" },
      accent: { type: "string", "x-codi-widget": "color" },
      body: { type: "string", "x-codi-widget": "markdown" },
      delay: { type: "string", "x-codi-widget": "duration" },
      limits: {
        type: "object",
        description: "Nested object.",
        properties: { perUser: { type: "integer" }, enabled: { type: "boolean" } },
      },
      tags: { type: "array", items: { type: "string" } },
      raw: { oneOf: [{ type: "string" }, { type: "number" }] },
    },
  };

  test("renders and edits each kind", async () => {
    const user = userEvent.setup();
    let value: JsonObject = { count: 3, ratio: 0.5, body: "**hi** {member}" };
    render(<Harness schema={kitchen} initial={value} onValue={(v) => (value = v)} />);

    // integer: min/max attributes, emptied optional number is removed
    const count = screen.getByRole("spinbutton", { name: "Count" }) as HTMLInputElement;
    expect(count.min).toBe("1");
    expect(count.max).toBe("10");
    await user.clear(count);
    await user.type(count, "7");
    expect(value["count"]).toBe(7);
    await user.clear(screen.getByRole("spinbutton", { name: "Ratio" }));
    expect("ratio" in value).toBe(false);

    // enum: segmented radios (≤ 4 short options) vs <select>
    await user.click(screen.getByRole("radio", { name: "slow" }));
    expect(value["mode"]).toBe("slow");
    await user.selectOptions(screen.getByRole("combobox", { name: "Level" }), "four");
    expect(value["level"]).toBe("four");

    // x-codi-ref: category
    await user.click(screen.getByRole("combobox", { name: "Parent" }));
    await user.click(screen.getByRole("option", { name: /Communauté/ }));
    expect(value["parent"]).toBe("community");

    // color: hex text input next to the native picker
    await user.type(screen.getByRole("textbox", { name: "Accent" }), "#ff0000");
    expect(value["accent"]).toBe("#ff0000");

    // markdown: write / preview tabs
    await user.click(screen.getByRole("tab", { name: "Preview" }));
    expect(screen.getByText("hi").tagName).toBe("STRONG");
    expect(screen.getByText("{member}").className).toBe("md-placeholder");
    await user.click(screen.getByRole("tab", { name: "Write" }));
    expect(screen.getByRole("textbox", { name: "Body" })).toBeTruthy();

    // duration
    await user.type(screen.getByRole("textbox", { name: "Delay" }), "1h30m");
    expect(value["delay"]).toBe("1h30m");

    // nested object, created from undefined
    expect(screen.getByRole("group", { name: /Limits/ })).toBeTruthy();
    await user.type(screen.getByRole("spinbutton", { name: "Per user" }), "4");
    await user.click(screen.getByRole("switch", { name: "Enabled" }));
    expect(value["limits"]).toEqual({ perUser: 4, enabled: true });

    // array of primitives
    await user.click(screen.getByRole("button", { name: "Add tag" }));
    await user.type(screen.getByRole("textbox", { name: "Tags 1" }), "x");
    expect(value["tags"]).toEqual(["x"]);

    // JSON fallback for oneOf without consts
    const raw = screen.getByRole("textbox", { name: "Raw" });
    await user.click(raw);
    await user.paste("42");
    await user.tab();
    expect(value["raw"]).toBe(42);
  });

  test("validates bounds, colors and durations", () => {
    const errors = validate(kitchen, { count: 11, accent: "red", delay: "soon" }, resources);
    expect(errors).toEqual([
      { pointer: "/count", message: "Maximum 10" },
      { pointer: "/accent", message: "Use a hex color such as #5865f2" },
      { pointer: "/delay", message: "Use a duration such as 30s, 10m or 1h30m" },
    ]);
    expect(validate(kitchen, { count: 2.5 }, resources)).toEqual([
      { pointer: "/count", message: "Must be a whole number" },
    ]);
    expect(validate(kitchen, {}, resources)).toEqual([{ pointer: "/count", message: "Required" }]);
  });
});

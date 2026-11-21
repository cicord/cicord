import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, test, vi } from "vite-plus/test";
import {
  Avatar,
  Button,
  ChoiceGroup,
  ChoiceTile,
  contrast,
  MemberCard,
  parseHex,
  Stepper,
  ThemeProvider,
  themeVariables,
} from "../src/index.ts";

afterEach(cleanup);

describe("theme", () => {
  test("maps the server theme to CSS variables with readable foregrounds", () => {
    const vars = themeVariables({ primary: "#fee75c", accent: "#123" }, "dark");
    expect(vars["--codi-primary"]).toBe("#fee75c");
    expect(vars["--codi-primary-fg"]).not.toBe("#ffffff"); // yellow needs dark text
    expect(vars["--codi-accent"]).toBe("#112233");
    // A very dark accent is lightened enough to be legible on the dark surface.
    const text = parseHex(vars["--codi-accent-text"])!;
    expect(contrast(text, [27, 29, 38])).toBeGreaterThanOrEqual(4.5);
  });

  test("falls back to defaults on missing or invalid colors", () => {
    expect(themeVariables(undefined, "light")["--codi-primary"]).toBe("#5865f2");
    expect(themeVariables({ primary: "blue" }, "light")["--codi-primary"]).toBe("#5865f2");
  });

  test("resolves mode, including system", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({
      matches: q.includes("light"),
      addEventListener() {},
      removeEventListener() {},
    }));
    const { container, rerender } = render(
      <ThemeProvider theme={{ mode: "system" }}>x</ThemeProvider>,
    );
    expect(container.firstElementChild?.getAttribute("data-codi-mode")).toBe("light");
    rerender(<ThemeProvider theme={{ mode: "dark" }}>x</ThemeProvider>);
    expect(container.firstElementChild?.getAttribute("data-codi-mode")).toBe("dark");
    vi.unstubAllGlobals();
  });

  test("global applies the mode to <html>", () => {
    const { unmount } = render(
      <ThemeProvider theme={{ mode: "light", primary: "#ff0000" }} global />,
    );
    expect(document.documentElement.getAttribute("data-codi-mode")).toBe("light");
    expect(document.documentElement.style.getPropertyValue("--codi-primary")).toBe("#ff0000");
    unmount();
    expect(document.documentElement.style.getPropertyValue("--codi-primary")).toBe("");
  });
});

describe("components", () => {
  test("Button loading disables it", () => {
    render(<Button loading>Envoyer</Button>);
    const button = screen.getByRole("button", { name: /Envoyer/ }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(button.getAttribute("aria-busy")).toBe("true");
  });

  test("ChoiceTile exposes checkbox / radio semantics", async () => {
    const onToggle = vi.fn();
    render(
      <ChoiceGroup label="Jeux" multiple>
        <ChoiceTile label="Minecraft" description="Survie" selected onToggle={onToggle} multiple />
        <ChoiceTile label="Valorant" selected={false} onToggle={onToggle} multiple />
      </ChoiceGroup>,
    );
    expect(screen.getByRole("group", { name: "Jeux" })).toBeTruthy();
    const minecraft = screen.getByRole("checkbox", { name: "Minecraft" });
    expect(minecraft.getAttribute("aria-checked")).toBe("true");
    expect(minecraft.getAttribute("aria-describedby")).toBeTruthy();
    await userEvent.click(screen.getByRole("checkbox", { name: "Valorant" }));
    expect(onToggle).toHaveBeenCalledOnce();
  });

  test("radio groups use a single tab stop and arrow keys", async () => {
    const user = userEvent.setup();
    function Radios() {
      const [value, setValue] = useState<string | null>(null);
      return (
        <ChoiceGroup label="Plateforme">
          {["PC", "Console", "Mobile"].map((v) => (
            <ChoiceTile key={v} label={v} selected={value === v} onToggle={() => setValue(v)} />
          ))}
        </ChoiceGroup>
      );
    }
    render(<Radios />);
    const [pc, consoleTile, mobile] = screen.getAllByRole("radio");
    expect([pc, consoleTile, mobile].map((r) => r?.tabIndex)).toEqual([0, -1, -1]);
    await user.tab();
    expect(document.activeElement).toBe(pc);
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(consoleTile);
    expect(consoleTile?.getAttribute("aria-checked")).toBe("true");
    expect([pc, consoleTile, mobile].map((r) => r?.tabIndex)).toEqual([-1, 0, -1]);
    await user.keyboard("{ArrowUp}{ArrowUp}");
    expect(mobile?.getAttribute("aria-checked")).toBe("true");
  });

  test("Stepper marks the current step", () => {
    render(
      <Stepper
        steps={[
          { id: "a", label: "Jeux" },
          { id: "b", label: "Plateforme" },
        ]}
        current={1}
      />,
    );
    expect(screen.getByText("Plateforme").closest("li")?.getAttribute("aria-current")).toBe("step");
  });

  test("Avatar falls back to initials", () => {
    render(<Avatar name="Alice Martin" />);
    expect(screen.getByRole("img", { name: "Alice Martin" }).textContent).toBe("AM");
    cleanup();
    render(<Avatar name="Léa" />);
    expect(screen.getByRole("img", { name: "Léa" }).textContent).toBe("L");
  });

  test("MemberCard lists roles", () => {
    render(
      <MemberCard
        displayName="Alice"
        username="alice"
        roles={[{ key: "pc", name: "PC", color: "#999" }]}
        rolesLabel="Rôles"
      />,
    );
    expect(screen.getByRole("heading", { name: "Alice" })).toBeTruthy();
    expect(screen.getByText("PC")).toBeTruthy();
  });
});

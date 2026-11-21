import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vite-plus/test";
import { LinkIcon, Markdown, parseMarkdown } from "../src/index.ts";

afterEach(cleanup);

describe("Markdown", () => {
  test("blocks", () => {
    expect(
      parseMarkdown("# Title\n\nHello\nworld\n\n- a\n- b\n  continued\n1. one\n> quoted\n"),
    ).toEqual([
      { type: "heading", level: 1, text: "Title" },
      { type: "paragraph", text: "Hello world" },
      { type: "list", ordered: false, items: ["a", "b continued"] },
      { type: "list", ordered: true, items: ["one"] },
      { type: "quote", text: "quoted" },
    ]);
  });

  test("renders inline formatting and safe links only", () => {
    const { container } = render(
      <Markdown
        source={
          "Play **together** and *have fun* with `code`. [Rules](https://x.example/r) [bad](javascript:alert(1)) <img src=x onerror=alert(1)>\n\n## Next"
        }
      />,
    );
    expect(container.querySelector("strong")?.textContent).toBe("together");
    expect(container.querySelector("em")?.textContent).toBe("have fun");
    expect(container.querySelector("code")?.textContent).toBe("code");
    const links = container.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]?.getAttribute("href")).toBe("https://x.example/r");
    expect(links[0]?.getAttribute("rel")).toBe("noopener noreferrer");
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(container.textContent).toContain("bad");
    expect(container.querySelector("h3")?.textContent).toBe("Next");
  });

  test("empty source renders nothing", () => {
    const { container } = render(<Markdown source={"  \n"} />);
    expect(container.innerHTML).toBe("");
  });
});

test("LinkIcon falls back to the link glyph", () => {
  const { container } = render(
    <>
      <LinkIcon name="youtube" />
      <LinkIcon name="unknown" />
    </>,
  );
  const icons = container.querySelectorAll("svg");
  expect(icons[0]?.getAttribute("data-icon")).toBe("youtube");
  expect(icons[1]?.getAttribute("data-icon")).toBe("link");
});

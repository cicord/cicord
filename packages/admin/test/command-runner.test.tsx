import type { Json, SimCommand, SimServer } from "@cicord/client";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vite-plus/test";
import { CommandRunner, expandCommands } from "../src/sim/CommandRunner.tsx";

const server: SimServer = {
  id: "1",
  name: "S",
  ownerId: "9",
  channels: [],
  roles: [],
  members: [],
};

const polls: SimCommand = {
  bot: "cicord",
  name: "polls",
  description: "Manage polls",
  options: [
    {
      name: "close",
      description: "Close a poll",
      type: "subcommand",
      required: false,
      options: [
        {
          name: "poll",
          description: "Poll",
          type: "string",
          required: true,
          autocomplete: true,
          maxLength: 100,
        },
      ],
    },
    {
      name: "admin",
      description: "Admin",
      type: "subcommandGroup",
      required: false,
      options: [
        {
          name: "purge",
          description: "Purge all",
          type: "subcommand",
          required: false,
          options: [],
        },
      ],
    },
  ],
};
const ping: SimCommand = { bot: "cicord", name: "ping", description: "Ping", options: [] };

describe("CommandRunner subcommands", () => {
  test("expandCommands lists one entry per subcommand path", () => {
    expect(
      expandCommands([polls, ping]).map((c) => [c.name, c.description, c.options.length]),
    ).toEqual([
      ["polls close", "Close a poll", 1],
      ["polls admin purge", "Purge all", 0],
      ["ping", "Ping", 0],
    ]);
  });

  test("picks a subcommand, suggests values and runs it with its path", async () => {
    const user = userEvent.setup();
    const onRun = vi.fn(async (_c: SimCommand, _o: { [key: string]: Json }) => {});
    const onAutocomplete = vi.fn(async (_c: SimCommand, _option: string, value: string) => [
      { name: `#1 ${value}`, value: "1" },
    ]);
    render(
      <CommandRunner
        commands={[polls, ping]}
        server={server}
        onRun={onRun}
        onAutocomplete={onAutocomplete}
      />,
    );
    const select = screen.getByLabelText(/command/i);
    expect([...(select as HTMLSelectElement).options].map((o) => o.textContent)).toEqual([
      "polls close",
      "polls admin purge",
      "ping",
    ]);
    const input = screen.getByRole("combobox", { name: /poll/ });
    await user.type(input, "pi");
    await waitFor(() =>
      expect(screen.getByTestId("opt-poll-suggestions").querySelectorAll("option")).toHaveLength(1),
    );
    expect(onAutocomplete).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: "polls close" }),
      "poll",
      "pi",
    );
    await user.click(screen.getByRole("button", { name: /polls close/ }));
    expect(onRun).toHaveBeenCalledWith(
      expect.objectContaining({ name: "polls close", bot: "cicord" }),
      {
        poll: "pi",
      },
    );

    await user.selectOptions(select, "cicord/polls admin purge");
    await user.click(screen.getByRole("button", { name: /polls admin purge/ }));
    expect(onRun).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: "polls admin purge" }),
      {},
    );
  });
});

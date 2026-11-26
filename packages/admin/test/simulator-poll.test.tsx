import type { Message, User } from "@cicord/client";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vite-plus/test";
import { MessageList } from "../src/sim/MessageView.tsx";

const alice: User = { id: "alice", username: "alice", displayName: "Alice", avatarUrl: "" };
const bob: User = { id: "bob", username: "bob", displayName: "Bob", avatarUrl: "" };
const resolver = { user: () => undefined, channel: () => undefined, role: () => undefined };
function sample(selected: number[], multiselect = false, finalized = false): Message {
  return {
    id: "poll",
    channelId: "lobby",
    serverId: "guild",
    createdAt: "2026-01-01T12:00:00Z",
    author: alice,
    content: "Friday lunch",
    embeds: [],
    components: [],
    ephemeral: false,
    poll: {
      question: "Where shall we meet?",
      multiselect,
      finalized,
      answers: [
        { id: 1, text: "Garden", emoji: "🌿", votes: 4 },
        { id: 2, text: "Studio", votes: 1 },
      ].map((a) => ({ ...a, ...(selected.includes(a.id) ? { meVoted: true } : {}) })),
    },
  };
}
test("each actor's selected answer is independent of totals and voting sends desired membership", async () => {
  const vote = vi.fn().mockResolvedValue(undefined),
    user = userEvent.setup();
  const tree = (viewer: User, chosen: number[]) => (
    <MessageList
      messages={[sample(chosen)]}
      resolver={resolver}
      viewer={viewer}
      onPollVote={vote}
    />
  );
  const ui = render(tree(alice, [2]));
  const poll = within(screen.getByRole("region", { name: "Where shall we meet?" }));
  expect(poll.getByRole("heading", { name: "Where shall we meet?" })).toBeTruthy();
  expect(poll.getByText("Choose one answer")).toBeTruthy();
  expect(poll.getByRole("button", { name: /Garden.*4 votes/ }).getAttribute("aria-pressed")).toBe(
    "false",
  );
  const studio = poll.getByRole("button", { name: /Studio.*1 vote/ });
  expect(studio.getAttribute("aria-pressed")).toBe("true");
  await user.click(studio);
  expect(vote).toHaveBeenLastCalledWith({
    userId: "alice",
    channelId: "lobby",
    messageId: "poll",
    answerId: 2,
    voted: false,
  });
  ui.rerender(tree(bob, []));
  const garden = screen.getByRole("button", { name: /Garden/ });
  expect(screen.queryAllByRole("button", { pressed: true })).toHaveLength(0);
  await user.click(garden);
  expect(vote).toHaveBeenLastCalledWith({
    userId: "bob",
    channelId: "lobby",
    messageId: "poll",
    answerId: 1,
    voted: true,
  });
});
test("a multiselect poll can show several choices, and a finalized or read-only poll cannot vote", async () => {
  const user = userEvent.setup(),
    vote = vi.fn().mockResolvedValue(undefined);
  const ui = render(
    <MessageList
      messages={[sample([1, 2], true)]}
      resolver={resolver}
      viewer={alice}
      onPollVote={vote}
    />,
  );
  expect(screen.getByText("Choose one or more answers")).toBeTruthy();
  expect(screen.getAllByRole("button", { pressed: true })).toHaveLength(2);
  ui.rerender(
    <MessageList
      messages={[sample([1, 2], true, true)]}
      resolver={resolver}
      viewer={alice}
      onPollVote={vote}
    />,
  );
  expect(screen.getByText("Voting ended")).toBeTruthy();
  for (const button of screen.getAllByRole("button")) {
    expect((button as HTMLButtonElement).disabled).toBe(true);
    await user.click(button);
  }
  ui.rerender(<MessageList messages={[sample([])]} resolver={resolver} />);
  expect(screen.getByRole("button", { name: /Garden/ })).toHaveProperty("disabled", true);
  expect(vote).not.toHaveBeenCalled();
});

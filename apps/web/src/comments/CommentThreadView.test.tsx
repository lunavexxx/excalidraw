import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import {
  EditorJotaiProvider,
  editorJotaiStore,
} from "@excalidraw/excalidraw/editor-jotai";

import { appJotaiStore, Provider } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";
import { canvasIdAtom, canvasRoleAtom } from "../canvas/atoms";

import { CommentThreadView } from "./CommentThreadView";
import { commentApi, getCommentThread } from "./api";

import type { CommentDetail, CommentMessage } from "./types";

vi.mock("./api", () => ({
  commentApi: vi.fn(),
  getCommentThread: vi.fn(),
  mentionCandidates: vi.fn(),
  commentError: () => "Failed",
  CommentApiError: class extends Error {},
}));
const message = (seq: number): CommentMessage => ({
  id: `message-${seq}`,
  seq,
  author: { id: "owner", name: "Owner", avatar: "" },
  body: `Message ${seq} 👀`,
  is_root: seq === 1,
  mentions: [],
  reactions: [],
  created_at: "2026-10-12T00:00:00Z",
  edited_at: null,
  deleted_at: null,
});
const fixture: CommentDetail = {
  thread: {
    id: "thread",
    canvas_id: "canvas",
    author: { id: "owner", name: "Owner", avatar: "" },
    anchor: { x: 10, y: 20 },
    version: 8,
    unread_version: 3,
    is_unread: true,
    resolved_at: null,
    created_at: "2026-10-12T00:00:00Z",
    activity_at: "2026-10-12T00:00:00Z",
    preview: "Message 1",
    root_deleted: false,
    reply_count: 2,
    can_resolve: false,
    can_delete: false,
  },
  comments: [message(1), message(3)],
  next_before: 3,
};
const mount = () =>
  render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <CommentThreadView
          canvasId="canvas"
          threadId="thread"
          onClose={() => {}}
        />
      </EditorJotaiProvider>
    </Provider>,
  );
beforeEach(() => {
  vi.clearAllMocks();
  appJotaiStore.set(canvasIdAtom, "canvas");
  appJotaiStore.set(canvasRoleAtom, "guest");
  appJotaiStore.set(currentUserAtom, null);
  vi.mocked(getCommentThread).mockResolvedValue(fixture);
  vi.mocked(commentApi).mockResolvedValue(null);
});
it("lets an anonymous guest read and page older replies without exposing mutation controls", async () => {
  mount();
  await screen.findByText("Message 3 👀");
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.queryByRole("button", { name: "Add reaction" })).toBeNull();
  expect(commentApi).not.toHaveBeenCalled();
  vi.mocked(getCommentThread).mockResolvedValueOnce({
    ...fixture,
    comments: [message(1), message(2)],
    next_before: 0,
  });
  fireEvent.click(screen.getByRole("button", { name: "Load earlier replies" }));
  await screen.findByText("Message 2 👀");
  expect(screen.getByText("Message 3 👀")).toBeVisible();
  expect(screen.getAllByText("Message 1 👀")).toHaveLength(1);
  expect(getCommentThread).toHaveBeenCalledTimes(2);
});
it("allows a logged-in viewer to reply and react, submits only the displayed read versions", async () => {
  appJotaiStore.set(canvasRoleAtom, "viewer");
  appJotaiStore.set(currentUserAtom, {
    id: "viewer",
    nickname: "Viewer",
    phone_masked: "138****0000",
    created_at: "2026-10-12T00:00:00Z",
  });
  mount();
  await screen.findByRole("textbox");
  expect(screen.queryByRole("button", { name: "Resolve" })).toBeNull();
  expect(screen.getAllByRole("button", { name: "Add reaction" })).toHaveLength(
    2,
  );
  await waitFor(() =>
    expect(commentApi).toHaveBeenCalledWith(
      "canvas",
      "comment-threads/thread/read",
      "POST",
      { version: 8, unread_version: 3 },
    ),
  );
  await waitFor(() => expect(getCommentThread).toHaveBeenCalledTimes(2));
  expect(commentApi).toHaveBeenCalledTimes(1);
});

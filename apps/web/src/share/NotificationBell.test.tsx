import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import {
  EditorJotaiProvider,
  editorJotaiStore,
} from "@excalidraw/excalidraw/editor-jotai";

import { appJotaiStore, Provider } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";
import { canvasIdAtom } from "../canvas/atoms";
import { commentsRefreshAtom, commentSelectionAtom } from "../comments/atoms";

import { NotificationBell } from "./NotificationBell";
import { accessApi, notifications } from "./accessApi";
import { shareDialogStateAtom } from "./ShareDialog";

const socket = vi.hoisted(() => ({
  handlers: new Map<string, (data: any) => void>(),
  on: vi.fn(),
  close: vi.fn(),
  connect: vi.fn(),
}));
vi.mock("socket.io-client", () => ({ default: () => socket }));
vi.mock("./accessApi", () => ({ accessApi: vi.fn(), notifications: vi.fn() }));
vi.mock("../auth/api", () => ({ refreshSession: vi.fn() }));
beforeEach(() => {
  vi.clearAllMocks();
  socket.handlers.clear();
  socket.on.mockImplementation((event, handler) => {
    socket.handlers.set(event, handler);
  });
  appJotaiStore.set(currentUserAtom, {
    id: "viewer",
    nickname: "Viewer",
    phone_masked: "138****0000",
    created_at: "2026-10-12T00:00:00Z",
  });
  appJotaiStore.set(canvasIdAtom, "canvas");
  appJotaiStore.set(shareDialogStateAtom, { isOpen: false });
  vi.mocked(notifications).mockResolvedValue({
    items: [
      {
        id: 1,
        type: "comment_mentioned",
        canvas_id: "canvas",
        canvas_name: "Team canvas",
        entity_id: "thread",
        actor_name: "Owner",
        source_version: 2,
        read_at: null,
        created_at: "2026-10-12T00:00:00Z",
      },
    ],
    unread_count: 1,
  });
  vi.mocked(accessApi).mockResolvedValue(null);
});
const mount = () =>
  render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <NotificationBell />
      </EditorJotaiProvider>
    </Provider>,
  );
it("opens a comment notification at the thread and keeps access management closed", async () => {
  const view = mount();
  const ownerWindow = view.container.ownerDocument.defaultView!;
  ownerWindow.history.replaceState(null, "", "/c/canvas");
  const bell = await screen.findByRole("button", { name: "Notifications (1)" });
  await act(async () => fireEvent.click(bell));
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: /Team canvas/ })),
  );
  await waitFor(() =>
    expect(appJotaiStore.get(commentSelectionAtom)).toEqual({
      threadId: "thread",
      focus: true,
      anchor: null,
      placing: false,
      movingId: null,
    }),
  );
  expect(accessApi).toHaveBeenCalledWith("/notifications/read", "POST", {
    id: 1,
  });
  expect(appJotaiStore.get(shareDialogStateAtom).isOpen).toBe(false);
  ownerWindow.history.replaceState(null, "", "/");
});
it("updates the current canvas unread indicators from a personal read event", async () => {
  mount();
  await waitFor(() =>
    expect(socket.handlers.has("comment-reads-changed")).toBe(true),
  );
  const version = appJotaiStore.get(commentsRefreshAtom);
  await act(async () =>
    socket.handlers.get("comment-reads-changed")?.({ canvas_id: "canvas" }),
  );
  expect(appJotaiStore.get(commentsRefreshAtom)).toBe(version + 1);
});

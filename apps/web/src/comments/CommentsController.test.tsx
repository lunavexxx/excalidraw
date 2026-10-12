import {
  fireEvent,
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import { ExcalidrawAPIContext } from "@excalidraw/excalidraw";
import { getDefaultAppState } from "@excalidraw/excalidraw/appState";
import {
  EditorJotaiProvider,
  editorJotaiStore,
} from "@excalidraw/excalidraw/editor-jotai";
import { API } from "@excalidraw/excalidraw/tests/helpers/api";

import type {
  ExcalidrawImperativeAPI,
  AppState,
} from "@excalidraw/excalidraw/types";

import { appJotaiStore, Provider } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";
import {
  canvasIdAtom,
  canvasRoleAtom,
  canvasTransitionAtom,
} from "../canvas/atoms";

import { CommentsController, CommentModeButton } from "./CommentsController";
import { CommentsPanel } from "./CommentsPanel";
import { commentApi, getCommentThread, listCommentThreads } from "./api";
import { selectComment } from "./atoms";

import type { CommentThread } from "./types";

vi.mock("./api", () => ({
  commentApi: vi.fn(),
  getCommentThread: vi.fn(),
  listCommentThreads: vi.fn(),
  mentionCandidates: vi.fn(),
  commentError: () => "Failed",
  CommentApiError: class extends Error {},
}));
const thread: CommentThread = {
  id: "thread",
  canvas_id: "canvas",
  author: { id: "owner", name: "Owner", avatar: "" },
  anchor: { x: 100, y: 100, element_id: "rect", u: 0.5, v: 0.5 },
  resolved_at: null,
  version: 1,
  unread_version: 1,
  is_unread: true,
  created_at: "2026-10-12T00:00:00Z",
  activity_at: "2026-10-12T00:00:00Z",
  preview: "Hello 👀",
  root_deleted: false,
  reply_count: 0,
  can_resolve: false,
  can_delete: false,
};
beforeEach(() => {
  vi.clearAllMocks();
  appJotaiStore.set(canvasIdAtom, "canvas");
  appJotaiStore.set(canvasRoleAtom, "viewer");
  appJotaiStore.set(canvasTransitionAtom, false);
  appJotaiStore.set(currentUserAtom, {
    id: "viewer",
    nickname: "Viewer",
    phone_masked: "138****0000",
    created_at: "2026-10-12T00:00:00Z",
  });
  vi.mocked(listCommentThreads).mockResolvedValue({
    items: [thread],
    next_cursor: "",
    total: 1,
    unread: 1,
  });
  vi.mocked(getCommentThread).mockResolvedValue({
    thread,
    comments: [],
    next_before: 0,
  });
  vi.mocked(commentApi).mockResolvedValue(null);
});
const mount = (mobile = false) => {
  let scene = [
    API.createElement({
      type: "rectangle",
      id: "rect",
      x: 80,
      y: 80,
      width: 40,
      height: 40,
    }),
  ];
  const listeners = new Set<() => void>();
  const state = {
    ...getDefaultAppState(),
    width: mobile ? 390 : 1200,
    height: 800,
    offsetLeft: 0,
    offsetTop: 0,
    scrollX: 0,
    scrollY: 0,
    zoom: { value: 1 },
  } as AppState;
  const api = {
    getAppState: () => state,
    getSceneElements: () => scene,
    getSceneElementsIncludingDeleted: () => scene,
    onChange: (fn: () => void) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    onScrollChange: () => () => {},
    onStateChange: () => () => {},
    updateScene: vi.fn(),
    setActiveTool: vi.fn(),
  } as unknown as ExcalidrawImperativeAPI;
  const view = render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <ExcalidrawAPIContext.Provider value={api}>
          <div className="excalidraw">
            <CommentModeButton />
            <CommentsController />
            <CommentsPanel />
          </div>
        </ExcalidrawAPIContext.Provider>
      </EditorJotaiProvider>
    </Provider>,
  );
  const root = view.container.querySelector<HTMLElement>(".excalidraw")!;
  vi.spyOn(root, "getBoundingClientRect").mockReturnValue({
    x: 0,
    y: 0,
    left: 0,
    top: 0,
    right: state.width,
    bottom: 800,
    width: state.width,
    height: 800,
    toJSON: () => ({}),
  });
  return {
    ...view,
    api,
    root,
    move: async () => {
      scene = [
        API.createElement({
          type: "rectangle",
          id: "rect",
          x: 180,
          y: 180,
          width: 40,
          height: 40,
        }),
      ];
      await act(async () => listeners.forEach((fn) => fn()));
    },
    remove: async () => {
      scene = [];
      await act(async () => listeners.forEach((fn) => fn()));
    },
  };
};
it("mounts the layer, opens a viewer thread, and preserves the last scene position when an element disappears", async () => {
  const view = mount();
  const pin = await screen.findByRole("button", {
    name: "Open comment by Owner",
  });
  expect(pin.style.left).toBe("100px");
  await view.move();
  await waitFor(() => expect(pin.style.left).toBe("200px"));
  await view.remove();
  await waitFor(() => expect(pin.style.left).toBe("200px"));
  fireEvent.click(pin);
  await screen.findByRole("dialog", { name: "Comments" });
  expect(screen.getByRole("textbox")).toBeVisible();
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "Close" })),
  );
  expect(pin).toHaveFocus();
});
it("opens focused comments on a phone without leaving the sidebar over the thread", async () => {
  const view = mount(true);
  await screen.findByRole("button", { name: "Open comment by Owner" });
  await act(async () => selectComment("thread", true));
  await waitFor(() =>
    expect(view.api.updateScene).toHaveBeenCalledWith(
      expect.objectContaining({
        appState: expect.objectContaining({ openSidebar: null }),
      }),
    ),
  );
  await screen.findByRole("dialog", { name: "Comments" });
});
it("does not let an anonymous guest enter placement mode", async () => {
  appJotaiStore.set(canvasRoleAtom, "guest");
  appJotaiStore.set(currentUserAtom, null);
  const view = mount();
  await screen.findByRole("button", { name: "Open comment by Owner" });
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "Comments" })),
  );
  expect(view.root.querySelector(".CommentPlacement")).toBeNull();
  expect(view.api.setActiveTool).not.toHaveBeenCalled();
});

it("discards late list and pin responses after switching canvases", async () => {
  let finish!: (page: Awaited<ReturnType<typeof listCommentThreads>>) => void;
  const oldPage = new Promise<Awaited<ReturnType<typeof listCommentThreads>>>(
    (resolve) => {
      finish = resolve;
    },
  );
  vi.mocked(listCommentThreads).mockImplementation((id) =>
    id === "canvas"
      ? oldPage
      : Promise.resolve({
          items: [
            {
              ...thread,
              id: "new-thread",
              canvas_id: "other",
              preview: "New canvas comment",
            },
          ],
          next_cursor: "",
          total: 1,
          unread: 1,
        }),
  );
  mount();
  await act(async () => appJotaiStore.set(canvasIdAtom, "other"));
  await screen.findByText("New canvas comment");
  await act(async () =>
    finish({ items: [thread], next_cursor: "", total: 1, unread: 1 }),
  );
  expect(screen.queryByText("Hello 👀")).toBeNull();
  expect(screen.getByText("New canvas comment")).toBeVisible();
});

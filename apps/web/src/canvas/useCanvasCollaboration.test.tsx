import { act, render } from "@testing-library/react";
import { useRef } from "react";

import type { ExcalidrawImperativeAPI } from "@excalidraw/excalidraw/types";

import { appJotaiStore } from "../app-jotai";

import { canvasIdAtom, canvasTransitionAtom } from "./atoms";
import { autoJoinCollab } from "./load";
import { useCanvasCollaboration } from "./useCanvasCollaboration";

import type { CollabAPI } from "../collab/Collab";

vi.mock("./load", () => ({
  autoJoinCollab: vi.fn().mockResolvedValue(undefined),
}));

let loading = false;
let sceneChanged: () => void;
const unsubscribe = vi.fn();
const api = {
  isDestroyed: false,
  getAppState: () => ({ isLoading: loading }),
  onChange: (cb: () => void) => {
    sceneChanged = cb;
    return unsubscribe;
  },
} as unknown as ExcalidrawImperativeAPI;
const collab = {} as CollabAPI;
const Canvas = ({ id }: { id: string | null }) => {
  const rootRef = useRef<HTMLDivElement>(null);
  useCanvasCollaboration({
    excalidrawAPI: api,
    collabAPI: collab,
    canvasId: id,
    rootRef,
  });
  return <div ref={rootRef} />;
};
beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  loading = false;
  appJotaiStore.set(canvasIdAtom, "canvas-a");
  appJotaiStore.set(canvasTransitionAtom, false);
});
afterEach(() => {
  vi.useRealTimers();
});

it("connects an idle online canvas without an edit or share action and retries", () => {
  render(<Canvas id="canvas-a" />);
  act(() => {
    vi.advanceTimersByTime(0);
  });
  expect(autoJoinCollab).toHaveBeenCalledWith("canvas-a");
  act(() => {
    vi.advanceTimersByTime(3000);
  });
  expect(autoJoinCollab).toHaveBeenCalledTimes(2);
});
it("waits for scene loading and canvas transitions", () => {
  loading = true;
  render(<Canvas id="canvas-a" />);
  act(() => {
    vi.advanceTimersByTime(0);
  });
  expect(autoJoinCollab).not.toHaveBeenCalled();
  loading = false;
  act(() => {
    appJotaiStore.set(canvasTransitionAtom, true);
    sceneChanged();
    vi.advanceTimersByTime(3000);
  });
  expect(autoJoinCollab).not.toHaveBeenCalled();
  act(() => {
    appJotaiStore.set(canvasTransitionAtom, false);
    vi.advanceTimersByTime(0);
  });
  expect(autoJoinCollab).toHaveBeenCalledWith("canvas-a");
});
it("connects only the new canvas and cancels retry work when unmounted", () => {
  const { rerender, unmount } = render(<Canvas id="canvas-a" />);
  appJotaiStore.set(canvasIdAtom, "canvas-b");
  rerender(<Canvas id="canvas-b" />);
  act(() => {
    vi.advanceTimersByTime(0);
  });
  expect(autoJoinCollab).toHaveBeenCalledTimes(1);
  expect(autoJoinCollab).toHaveBeenCalledWith("canvas-b");
  unmount();
  act(() => {
    vi.advanceTimersByTime(6000);
  });
  expect(autoJoinCollab).toHaveBeenCalledTimes(1);
  expect(unsubscribe).toHaveBeenCalledTimes(2);
});
it("keeps an unsaved local draft out of server rooms", () => {
  appJotaiStore.set(canvasIdAtom, null);
  render(<Canvas id={null} />);
  act(() => {
    vi.advanceTimersByTime(6000);
  });
  expect(autoJoinCollab).not.toHaveBeenCalled();
});

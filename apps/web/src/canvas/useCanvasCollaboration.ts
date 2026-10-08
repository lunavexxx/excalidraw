import { useEffect } from "react";

import type { ExcalidrawImperativeAPI } from "@excalidraw/excalidraw/types";

import { appJotaiStore } from "../app-jotai";

import { canvasIdAtom, canvasTransitionAtom } from "./atoms";
import { autoJoinCollab } from "./load";

import type { RefObject } from "react";

import type { CollabAPI } from "../collab/Collab";

/** Keep online canvases connected without editing or opening sharing. */
export const useCanvasCollaboration = ({
  excalidrawAPI,
  collabAPI,
  canvasId,
  rootRef,
}: {
  excalidrawAPI: ExcalidrawImperativeAPI | null;
  collabAPI: CollabAPI | null;
  canvasId: string | null;
  rootRef: RefObject<HTMLDivElement | null>;
}) => {
  useEffect(() => {
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (!ownerWindow || !excalidrawAPI || !collabAPI || !canvasId) {
      return;
    }
    const connect = () => {
      if (
        appJotaiStore.get(canvasIdAtom) === canvasId &&
        !appJotaiStore.get(canvasTransitionAtom) &&
        !excalidrawAPI.isDestroyed &&
        !excalidrawAPI.getAppState().isLoading
      ) {
        void autoJoinCollab(canvasId);
      }
    };
    let pending: number | undefined;
    const schedule = () => {
      if (pending === undefined) {
        pending = ownerWindow.setTimeout(() => {
          pending = undefined;
          connect();
        }, 0);
      }
    };
    const unsubscribeScene = excalidrawAPI.onChange(schedule);
    const unsubscribeTransition = appJotaiStore.sub(
      canvasTransitionAtom,
      schedule,
    );
    // Retry socket setup and rejected handshakes; transport outages are also
    // handled by Socket.IO's own reconnect loop.
    const retry = ownerWindow.setInterval(connect, 3000);
    ownerWindow.addEventListener("online", schedule);
    schedule();
    return () => {
      unsubscribeScene();
      unsubscribeTransition();
      ownerWindow.removeEventListener("online", schedule);
      ownerWindow.clearInterval(retry);
      ownerWindow.clearTimeout(pending);
    };
  }, [excalidrawAPI, collabAPI, canvasId, rootRef]);
};

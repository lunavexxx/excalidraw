import React from "react";

import { KEYS } from "@excalidraw/common";
import { isFrameLikeElement } from "@excalidraw/element";

import {
  useExcalidrawAPI,
  useExcalidrawElements,
} from "@excalidraw/excalidraw/components/App";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";
import {
  chevronLeftIcon,
  chevronRight,
  CloseIcon,
} from "@excalidraw/excalidraw/components/icons";
import { useI18n } from "@excalidraw/excalidraw/i18n";

import type { AppState } from "@excalidraw/excalidraw/types";

import type {
  ExcalidrawFrameLikeElement,
  NonDeleted,
} from "@excalidraw/element/types";

import { atom, useAtom } from "../app-jotai";

import "./PresentationMode.scss";

export const presentationModeAtom = atom(false);

const SLIDE_NAV_DURATION = 400;
// keep in sync with the control-bar footprint in PresentationMode.scss
const SLIDE_BOTTOM_OFFSET = 88;

const doubleRaf = (ownerWindow: Window & typeof globalThis) =>
  new Promise<void>((resolve) =>
    ownerWindow.requestAnimationFrame(() =>
      ownerWindow.requestAnimationFrame(() => resolve()),
    ),
  );

type PresentationFrame = NonDeleted<ExcalidrawFrameLikeElement>;

type SavedViewState = {
  scrollX: number;
  scrollY: number;
  zoom: AppState["zoom"];
  frameRendering: AppState["frameRendering"];
  openSidebar: AppState["openSidebar"];
};

export const PresentationMode = () => {
  const { t } = useI18n();
  const excalidrawAPI = useExcalidrawAPI();
  const elements = useExcalidrawElements();
  const [isActive, setPresentationMode] = useAtom(presentationModeAtom);

  const frames = React.useMemo(
    () =>
      elements.filter((element): element is PresentationFrame =>
        isFrameLikeElement(element),
      ),
    [elements],
  );

  const [index, setIndex] = React.useState(0);
  const rootRef = React.useRef<HTMLDivElement>(null);
  const savedRef = React.useRef<SavedViewState | null>(null);
  // mirrors isActive for handlers that must not rebind mid-presentation
  const isActiveRef = React.useRef(false);
  // latest index/frame for handlers that must not rebind on every slide
  const indexRef = React.useRef(0);
  const currentFrameIdRef = React.useRef<string | null>(null);

  const goToIndex = React.useCallback(
    (next: number, opts?: { animation?: boolean; force?: boolean }) => {
      if (!excalidrawAPI || !isActiveRef.current || frames.length === 0) {
        return;
      }
      const clamped = Math.max(0, Math.min(next, frames.length - 1));
      if (clamped === indexRef.current && !opts?.force) {
        return;
      }
      indexRef.current = clamped;
      currentFrameIdRef.current = frames[clamped].id;
      setIndex(clamped);
      excalidrawAPI.setViewport({
        target: frames[clamped],
        fit: "contain",
        lock: { scroll: true, zoom: true, overscroll: false },
        animation:
          opts?.animation === false ? false : { duration: SLIDE_NAV_DURATION },
        offsets: { top: 0, right: 0, left: 0, bottom: SLIDE_BOTTOM_OFFSET },
      });
    },
    [excalidrawAPI, frames],
  );

  const exit = React.useCallback(async () => {
    if (!excalidrawAPI || !isActiveRef.current) {
      return;
    }
    const saved = savedRef.current;
    savedRef.current = null;
    isActiveRef.current = false;
    const ownerDocument = rootRef.current?.ownerDocument;
    if (!ownerDocument) {
      setPresentationMode(false);
      return;
    }

    rootRef.current
      ?.closest(".excalidraw")
      ?.classList.remove("excalidraw--presenting");

    try {
      if (ownerDocument.fullscreenElement) {
        await ownerDocument.exitFullscreen();
      }
    } catch {
      // restore regardless
    }

    // clear the scroll lock first, else it re-clamps the restored scroll
    excalidrawAPI.setViewport(null);
    if (saved) {
      excalidrawAPI.updateScene({
        appState: {
          scrollX: saved.scrollX,
          scrollY: saved.scrollY,
          zoom: saved.zoom,
          frameRendering: saved.frameRendering,
          openSidebar: saved.openSidebar,
        },
      });
    }
    setPresentationMode(false);
  }, [excalidrawAPI, setPresentationMode]);

  const exitRef = React.useRef(exit);
  React.useEffect(() => {
    exitRef.current = exit;
  });

  React.useEffect(() => {
    if (!isActive || !excalidrawAPI) {
      return;
    }
    if (savedRef.current) {
      // already started (StrictMode remount / dep re-run)
      return;
    }
    const container = rootRef.current?.closest(".excalidraw");
    const appState = excalidrawAPI.getAppState();
    const ownerDocument = rootRef.current?.ownerDocument;
    const ownerWindow = ownerDocument?.defaultView;
    if (!ownerDocument || !ownerWindow) {
      return;
    }
    // getAppState() values are mutated in place — copy
    savedRef.current = {
      scrollX: appState.scrollX,
      scrollY: appState.scrollY,
      zoom: { value: appState.zoom.value },
      frameRendering: { ...appState.frameRendering },
      openSidebar: appState.openSidebar ? { ...appState.openSidebar } : null,
    };
    isActiveRef.current = true;
    indexRef.current = 0;
    currentFrameIdRef.current = null;
    setIndex(0);

    excalidrawAPI.updateScene({ appState: { openSidebar: null } });
    excalidrawAPI.updateFrameRendering({
      name: false,
      outline: false,
      clip: true,
    });
    container?.classList.add("excalidraw--presenting");

    const start = async () => {
      try {
        if (container && !ownerDocument.fullscreenElement) {
          await container.requestFullscreen();
        }
        // let the fullscreen resize propagate before viewport math
        await doubleRaf(ownerWindow);
      } catch {
        // fullscreen unavailable (e.g. iframe without allowfullscreen) —
        // present windowed
      }
      if (isActiveRef.current) {
        goToIndex(0, { animation: false, force: true });
      }
    };
    void start();
  }, [isActive, excalidrawAPI, goToIndex]);

  React.useEffect(() => {
    if (!isActive) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) {
        return;
      }
      switch (event.key) {
        case KEYS.ARROW_RIGHT:
        case KEYS.ARROW_DOWN:
        case KEYS.PAGE_DOWN:
        case KEYS.SPACE:
          event.preventDefault();
          event.stopPropagation();
          goToIndex(indexRef.current + 1);
          break;
        case KEYS.ARROW_LEFT:
        case KEYS.ARROW_UP:
        case KEYS.PAGE_UP:
          event.preventDefault();
          event.stopPropagation();
          goToIndex(indexRef.current - 1);
          break;
        case KEYS.ESCAPE:
          // in fullscreen the browser exits fullscreen itself (and may not
          // even deliver the keydown) — the fullscreenchange handler exits
          if (!rootRef.current?.ownerDocument.fullscreenElement) {
            event.preventDefault();
            event.stopPropagation();
            void exitRef.current();
          }
          break;
      }
    };
    // capture on window beats the editor's document-level bubble listener
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    ownerWindow.addEventListener("keydown", onKeyDown, { capture: true });
    return () => {
      ownerWindow.removeEventListener("keydown", onKeyDown, { capture: true });
    };
  }, [isActive, goToIndex, excalidrawAPI]);

  React.useEffect(() => {
    if (!isActive) {
      return;
    }
    const ownerDocument = rootRef.current?.ownerDocument;
    if (!ownerDocument) {
      return;
    }
    const onFullscreenChange = () => {
      if (!ownerDocument.fullscreenElement && isActiveRef.current) {
        void exitRef.current();
      }
    };
    ownerDocument.addEventListener("fullscreenchange", onFullscreenChange);
    return () => {
      ownerDocument.removeEventListener("fullscreenchange", onFullscreenChange);
    };
  }, [isActive, excalidrawAPI]);

  React.useEffect(() => {
    if (!isActive) {
      return;
    }
    if (frames.length === 0) {
      void exitRef.current();
      return;
    }
    const clamped = Math.min(index, frames.length - 1);
    if (clamped !== index || frames[clamped].id !== currentFrameIdRef.current) {
      goToIndex(clamped, { animation: false, force: true });
    }
  }, [isActive, frames, index, goToIndex]);

  React.useEffect(() => {
    return () => {
      if (isActiveRef.current) {
        void exitRef.current();
      }
    };
  }, []);

  return (
    <div className="presentation-mode" ref={rootRef}>
      {isActive && frames.length > 0 && (
        <div className="presentation-mode__controls" data-viewport-ui="bottom">
          <FilledButton
            variant="icon"
            color="muted"
            label={t("presentationPanel.prevSlide")}
            disabled={index === 0}
            onClick={() => goToIndex(index - 1)}
            icon={chevronLeftIcon}
          />
          <span className="presentation-mode__counter">
            {t("presentationPanel.slideCounter", {
              current: index + 1,
              total: frames.length,
            })}
          </span>
          <FilledButton
            variant="icon"
            color="muted"
            label={t("presentationPanel.nextSlide")}
            disabled={index === frames.length - 1}
            onClick={() => goToIndex(index + 1)}
            icon={chevronRight}
          />
          <div className="presentation-mode__controls-divider" />
          <FilledButton
            variant="icon"
            color="muted"
            label={t("presentationPanel.exit")}
            onClick={() => void exitRef.current()}
            icon={CloseIcon}
          />
        </div>
      )}
    </div>
  );
};

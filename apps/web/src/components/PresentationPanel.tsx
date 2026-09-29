import React from "react";

import {
  getElementsOverlappingFrame,
  getFrameLikeTitle,
  isFrameLikeElement,
} from "@excalidraw/element";
import { arrayToMap } from "@excalidraw/common";
import { THEME } from "@excalidraw/excalidraw";

import {
  useExcalidrawAPI,
  useExcalidrawElements,
} from "@excalidraw/excalidraw/components/App";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";
import { playerPlayIcon } from "@excalidraw/excalidraw/components/icons";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { useUIAppState } from "@excalidraw/excalidraw/context/ui-appState";
import { exportToCanvas } from "@excalidraw/utils";

import type {
  ExcalidrawFrameLikeElement,
  NonDeleted,
} from "@excalidraw/element/types";

import { useAtom } from "../app-jotai";

import { presentationModeAtom } from "./PresentationMode";

const THUMBNAIL_MAX_SIZE = 560;
const THUMBNAIL_EXPORT_SCALE = 2;
const THUMBNAIL_DEBOUNCE_MS = 250;

export const PresentationPanel = () => {
  const { t } = useI18n();
  const excalidrawAPI = useExcalidrawAPI();
  const elements = useExcalidrawElements();
  const { theme, exportBackground, viewBackgroundColor, exportWithDarkMode } =
    useUIAppState();
  const [, setPresentationMode] = useAtom(presentationModeAtom);
  const panelRef = React.useRef<HTMLDivElement>(null);

  const frames = React.useMemo(
    () =>
      elements.filter(
        (element): element is NonDeleted<ExcalidrawFrameLikeElement> =>
          isFrameLikeElement(element),
      ),
    [elements],
  );

  const hostsRef = React.useRef<Map<string, HTMLDivElement>>(new Map());
  const hostCallbacksRef = React.useRef<
    Map<string, (node: HTMLDivElement | null) => void>
  >(new Map());
  const signaturesRef = React.useRef<Map<string, string>>(new Map());
  const requestIdsRef = React.useRef<Map<string, number>>(new Map());
  const requestCounterRef = React.useRef(0);
  const globalSigRef = React.useRef<string | null>(null);

  // element edits (drag/resize/rename) mutate elements in place, so the
  // elements context value keeps its identity — subscribe to scene changes
  // directly to re-render and re-run the thumbnail effect
  const [sceneTick, setSceneTick] = React.useState(0);
  React.useEffect(() => {
    if (!excalidrawAPI) {
      return;
    }
    return excalidrawAPI.onChange(() => setSceneTick((tick) => tick + 1));
  }, [excalidrawAPI]);

  const setItemHost = React.useCallback((frameId: string) => {
    let callback = hostCallbacksRef.current.get(frameId);
    if (!callback) {
      // brace-bodied on purpose: a returned function would be treated as a
      // ref cleanup by React 19 and the node argument would never be null
      callback = (node: HTMLDivElement | null) => {
        if (node) {
          hostsRef.current.set(frameId, node);
        } else {
          hostsRef.current.delete(frameId);
        }
      };
      hostCallbacksRef.current.set(frameId, callback);
    }
    return callback;
  }, []);

  React.useEffect(() => {
    if (!excalidrawAPI) {
      return;
    }

    const ownerWindow = panelRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const timer = ownerWindow.setTimeout(() => {
      const appState = excalidrawAPI.getAppState();
      const files = excalidrawAPI.getFiles();

      const globalSig = `${exportBackground}|${viewBackgroundColor}|${exportWithDarkMode}`;
      if (globalSigRef.current !== globalSig) {
        globalSigRef.current = globalSig;
        signaturesRef.current.clear();
      }

      const elementsMap = arrayToMap(elements);
      const frameIds = new Set(frames.map((frame) => frame.id));

      for (const frameId of signaturesRef.current.keys()) {
        if (!frameIds.has(frameId)) {
          signaturesRef.current.delete(frameId);
          requestIdsRef.current.delete(frameId);
          hostCallbacksRef.current.delete(frameId);
        }
      }

      const exportPromises: Promise<void>[] = [];

      for (const frame of frames) {
        const host = hostsRef.current.get(frame.id);
        if (!host) {
          continue;
        }

        const overlapping = getElementsOverlappingFrame(
          elements,
          frame,
          elementsMap,
        );
        const sig = `${frame.version}:${overlapping
          .map((element) => `${element.id}:${element.version}`)
          .join(",")}`;

        if (signaturesRef.current.get(frame.id) === sig && host.firstChild) {
          continue;
        }

        const requestId = ++requestCounterRef.current;
        requestIdsRef.current.set(frame.id, requestId);

        exportPromises.push(
          exportToCanvas({
            elements,
            appState: { ...appState, exportScale: THUMBNAIL_EXPORT_SCALE },
            files,
            exportingFrame: frame,
            maxWidthOrHeight: THUMBNAIL_MAX_SIZE,
          })
            .then((canvas) => {
              if (
                requestIdsRef.current.get(frame.id) === requestId &&
                hostsRef.current.get(frame.id)
              ) {
                host.replaceChildren(canvas);
                signaturesRef.current.set(frame.id, sig);
              }
            })
            .catch(() => {
              // retry on the next run
              signaturesRef.current.delete(frame.id);
            }),
        );
      }

      void Promise.all(exportPromises);
    }, THUMBNAIL_DEBOUNCE_MS);

    return () => {
      ownerWindow.clearTimeout(timer);
    };
  }, [
    frames,
    elements,
    excalidrawAPI,
    sceneTick,
    exportBackground,
    viewBackgroundColor,
    exportWithDarkMode,
  ]);

  const focusFrame = (frame: NonDeleted<ExcalidrawFrameLikeElement>) => {
    excalidrawAPI?.setViewport({
      target: frame,
      fit: "contain",
      animation: true,
      offsets: { ui: true },
    });
  };

  return (
    <div className="presentation-panel" ref={panelRef}>
      {frames.length === 0 ? (
        <div className="app-sidebar-promo-container presentation-panel__empty">
          <div
            className="app-sidebar-promo-image"
            style={{
              ["--image-source" as any]: `url(/sidebar-presentation-promo-${
                theme === THEME.DARK ? "dark" : "light"
              }.jpg)`,
              opacity: 0.9,
            }}
          />
          <div className="presentation-panel__empty-text">
            {t("presentationPanel.empty")}
          </div>
        </div>
      ) : (
        <>
          <div className="presentation-panel__list">
            {frames.map((frame) => (
              <div
                key={frame.id}
                className="presentation-panel__item"
                onClick={() => focusFrame(frame)}
                title={t("presentationPanel.focusHint")}
              >
                <div
                  className="presentation-panel__item-frame"
                  ref={setItemHost(frame.id)}
                />
                <div className="presentation-panel__item-footer">
                  <span className="presentation-panel__item-title">
                    {getFrameLikeTitle(frame)}
                  </span>
                </div>
              </div>
            ))}
          </div>
          <div className="presentation-panel__footer">
            <FilledButton
              color="primary"
              size="medium"
              fullWidth
              icon={playerPlayIcon}
              onClick={() => setPresentationMode(true)}
            >
              {t("presentationPanel.start")}
            </FilledButton>
          </div>
        </>
      )}
    </div>
  );
};

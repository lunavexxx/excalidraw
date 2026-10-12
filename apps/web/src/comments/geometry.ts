import { hitElementItself } from "@excalidraw/element/collision";
import { pointFrom } from "@excalidraw/math";

import type { ExcalidrawElement } from "@excalidraw/element/types";

import type { CommentAnchor, CommentDraft } from "./types";

export const anchorPosition = (
  anchor: CommentAnchor,
  elements: readonly ExcalidrawElement[],
) => {
  const element = anchor.element_id
    ? elements.find((item) => item.id === anchor.element_id)
    : null;
  if (!element) {
    return { x: anchor.x, y: anchor.y, detached: !!anchor.element_id };
  }
  const dx = ((anchor.u ?? 0.5) - 0.5) * element.width;
  const dy = ((anchor.v ?? 0.5) - 0.5) * element.height;
  return {
    x:
      element.x +
      element.width / 2 +
      Math.cos(element.angle) * dx -
      Math.sin(element.angle) * dy,
    y:
      element.y +
      element.height / 2 +
      Math.sin(element.angle) * dx +
      Math.cos(element.angle) * dy,
    detached: element.isDeleted,
  };
};
export const createAnchor = (
  point: { x: number; y: number },
  elements: readonly ExcalidrawElement[],
  threshold = 5,
): CommentAnchor => {
  const elementsMap = new Map(elements.map((element) => [element.id, element]));
  for (const element of [...elements].reverse()) {
    if (
      element.isDeleted ||
      !hitElementItself({
        point: pointFrom(point.x, point.y),
        element,
        threshold,
        elementsMap,
        overrideShouldTestInside: !["line", "arrow", "freedraw"].includes(
          element.type,
        ),
      })
    ) {
      continue;
    }
    const dx = point.x - element.x - element.width / 2;
    const dy = point.y - element.y - element.height / 2;
    const u = element.width
      ? Math.max(
          0,
          Math.min(
            1,
            (Math.cos(element.angle) * dx + Math.sin(element.angle) * dy) /
              element.width +
              0.5,
          ),
        )
      : 0.5;
    const v = element.height
      ? Math.max(
          0,
          Math.min(
            1,
            (-Math.sin(element.angle) * dx + Math.cos(element.angle) * dy) /
              element.height +
              0.5,
          ),
        )
      : 0.5;
    return { ...point, element_id: element.id, u, v };
  }
  return point;
};

// Preserve only mentions untouched by an edit, using textarea UTF-16 offsets.
export const updateDraftText = (
  draft: CommentDraft,
  body: string,
): CommentDraft => {
  let start = 0;
  while (
    start < draft.body.length &&
    start < body.length &&
    draft.body[start] === body[start]
  ) {
    start++;
  }
  let oldEnd = draft.body.length;
  let newEnd = body.length;
  while (
    oldEnd > start &&
    newEnd > start &&
    draft.body[oldEnd - 1] === body[newEnd - 1]
  ) {
    oldEnd--;
    newEnd--;
  }
  const shift = newEnd - oldEnd;
  return {
    body,
    requestId: null,
    mentions: draft.mentions.flatMap((mention) => {
      if (mention.end <= start) {
        return [mention];
      }
      if (mention.start >= oldEnd) {
        return [
          {
            ...mention,
            start: mention.start + shift,
            end: mention.end + shift,
          },
        ];
      }
      return [];
    }),
  };
};

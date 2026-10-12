import { API } from "@excalidraw/excalidraw/tests/helpers/api";

import { newElementWith } from "@excalidraw/element";

import type { ExcalidrawElement } from "@excalidraw/element/types";

import { anchorPosition, createAnchor, updateDraftText } from "./geometry";

it("keeps a rotated local anchor through moving, resizing, deletion and fallback", () => {
  const rectangle = API.createElement({
    type: "rectangle",
    id: "rect",
    x: 100,
    y: 200,
    width: 80,
    height: 40,
    angle: Math.PI / 2,
  });
  const point = { x: 150, y: 200 };
  const anchor = createAnchor(point, [rectangle]);
  expect(anchor.element_id).toBe("rect");
  expect(anchor.u).toBeCloseTo(0.25);
  expect(anchor.v).toBeCloseTo(0.25);
  const moved = newElementWith(rectangle as ExcalidrawElement, {
    x: 300,
    width: 160,
    isDeleted: true,
  });
  const at = anchorPosition(anchor, [moved]);
  expect(at.x).toBeCloseTo(390);
  expect(at.y).toBeCloseTo(180);
  expect(at.detached).toBe(true);
  expect(anchorPosition({ ...anchor, x: at.x, y: at.y }, [])).toEqual(at);
});

it("picks the top visible shape and does not bind outside an ellipse or diamond", () => {
  const rect = API.createElement({
    type: "rectangle",
    id: "rect",
    width: 100,
    height: 100,
  });
  const ellipse = API.createElement({
    type: "ellipse",
    id: "ellipse",
    width: 100,
    height: 100,
  });
  expect(createAnchor({ x: 50, y: 50 }, [rect, ellipse]).element_id).toBe(
    "ellipse",
  );
  expect(createAnchor({ x: 2, y: 2 }, [ellipse])).toEqual({ x: 2, y: 2 });
  const diamond = API.createElement({
    type: "diamond",
    width: 100,
    height: 100,
  });
  expect(createAnchor({ x: 2, y: 2 }, [diamond])).toEqual({ x: 2, y: 2 });
});

it("shifts UTF-16 mention offsets on emoji insertion and removes edited mentions", () => {
  const draft = {
    body: "hello @Ada!",
    mentions: [{ user_id: "ada", start: 6, end: 10 }],
    requestId: "sent",
  };
  const inserted = updateDraftText(draft, "👨‍👩‍👧‍👦 hello @Ada!");
  expect(inserted.mentions).toEqual([{ user_id: "ada", start: 18, end: 22 }]);
  expect(inserted.requestId).toBeNull();
  expect(updateDraftText(inserted, "👨‍👩‍👧‍👦 hello @Alan!").mentions).toEqual([]);
  expect(updateDraftText(draft, "hello @Ada! 👍🏽").mentions).toEqual(
    draft.mentions,
  );
});

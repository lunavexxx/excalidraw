import { isInvisiblySmallElement } from "@excalidraw/element";

import type { UserIdleState } from "@excalidraw/common";
import type { SceneBounds } from "@excalidraw/element";
import type { OrderedExcalidrawElement } from "@excalidraw/element/types";
import type { AppState, SocketId } from "@excalidraw/excalidraw/types";
import type { MakeBrand } from "@excalidraw/common/utility-types";

import { DELETED_ELEMENT_TIMEOUT } from "../app_constants";

import type { WS_SUBTYPES } from "../app_constants";

export type SyncableExcalidrawElement = OrderedExcalidrawElement &
  MakeBrand<"SyncableExcalidrawElement">;

export const isSyncableElement = (
  element: OrderedExcalidrawElement,
): element is SyncableExcalidrawElement => {
  if (element.isDeleted) {
    if (element.updated > Date.now() - DELETED_ELEMENT_TIMEOUT) {
      return true;
    }
    return false;
  }
  return !isInvisiblySmallElement(element);
};

export const getSyncableElements = (
  elements: readonly OrderedExcalidrawElement[],
) =>
  elements.filter((element) =>
    isSyncableElement(element),
  ) as SyncableExcalidrawElement[];

export type SocketUpdateDataSource = {
  INVALID_RESPONSE: {
    type: WS_SUBTYPES.INVALID_RESPONSE;
  };
  SCENE_INIT: {
    type: WS_SUBTYPES.INIT;
    payload: {
      elements: readonly OrderedExcalidrawElement[];
    };
  };
  SCENE_UPDATE: {
    type: WS_SUBTYPES.UPDATE;
    payload: {
      elements: readonly OrderedExcalidrawElement[];
    };
    /** 落库 seq(room persist-then-relay 注入);客户端游标据此推进 */
    seq?: number;
  };
  MOUSE_LOCATION: {
    type: WS_SUBTYPES.MOUSE_LOCATION;
    payload: {
      socketId: SocketId;
      pointer: { x: number; y: number; tool: "pointer" | "laser" };
      button: "down" | "up";
      selectedElementIds: AppState["selectedElementIds"];
      username: string;
    };
  };
  USER_VISIBLE_SCENE_BOUNDS: {
    type: WS_SUBTYPES.USER_VISIBLE_SCENE_BOUNDS;
    payload: {
      socketId: SocketId;
      username: string;
      sceneBounds: SceneBounds;
    };
  };
  IDLE_STATUS: {
    type: WS_SUBTYPES.IDLE_STATUS;
    payload: {
      socketId: SocketId;
      userState: UserIdleState;
      username: string;
    };
  };
};

export type SocketUpdateDataIncoming =
  SocketUpdateDataSource[keyof SocketUpdateDataSource];

export type SocketUpdateData =
  SocketUpdateDataSource[keyof SocketUpdateDataSource] & {
    _brand: "socketUpdateData";
  };

// sync-request ack(协议 v2,Semantic 对应 CRDT 的 Sync Step 2 载荷):
// room 回调 Go API scene-diff,返回游标之后的事件折叠差异。
export type SceneSyncPatch = {
  elements: readonly OrderedExcalidrawElement[];
  cursor: number;
  has_more: boolean;
};

export type SceneSyncAck = SceneSyncPatch | { error: string };

// 协作房间标识 = 画布 id(/c/:id),不再使用 #room=<id>,<key> hash 链接
// (上游"链接即钥匙"模式已由服务端 ACL + 明文广播取代)。

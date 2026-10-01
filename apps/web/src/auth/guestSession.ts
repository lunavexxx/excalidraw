// guest 会话:匿名分享链接访问者。share token 存 sessionStorage(按画布
// 隔离,换得短期 guest JWT);JWT 过期后可用 share token 重新换票。
// share token 等同访问凭证,不落 localStorage、不进 git/日志。
const tokenKey = (canvasId: string) => `excalidraw-guest-token-${canvasId}`;
const shareKey = (canvasId: string) => `excalidraw-guest-share-${canvasId}`;

export const setGuestToken = (canvasId: string, token: string | null) => {
  try {
    if (token) {
      sessionStorage.setItem(tokenKey(canvasId), token);
    } else {
      sessionStorage.removeItem(tokenKey(canvasId));
    }
  } catch {
    // storage 不可用(隐私窗口等)时静默降级为仅内存会话
  }
};

export const getGuestToken = (canvasId: string): string | null => {
  try {
    return sessionStorage.getItem(tokenKey(canvasId));
  } catch {
    return null;
  }
};

export const setGuestShareToken = (
  canvasId: string,
  shareToken: string | null,
) => {
  try {
    if (shareToken) {
      sessionStorage.setItem(shareKey(canvasId), shareToken);
    } else {
      sessionStorage.removeItem(shareKey(canvasId));
    }
  } catch {
    // ignore
  }
};

export const getGuestShareToken = (canvasId: string): string | null => {
  try {
    return sessionStorage.getItem(shareKey(canvasId));
  } catch {
    return null;
  }
};

/** 离开 guest 画布(切换/登出)时清理整个 guest 会话。 */
export const clearGuestSession = (canvasId: string) => {
  setGuestToken(canvasId, null);
  setGuestShareToken(canvasId, null);
};

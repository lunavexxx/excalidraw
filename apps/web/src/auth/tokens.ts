// access token 只存内存,不落 localStorage——XSS 至多偷到 15 分钟短令牌;
// refresh token 存 localStorage,随 Authorization 头回传续期(header 认证、
// 不用 cookie,持久化交给客户端存储)。
const REFRESH_STORAGE_KEY = "excalidraw-refresh-token";

let accessToken: string | null = null;

export const setAccessToken = (token: string | null) => {
  accessToken = token;
};

export const getAccessToken = () => accessToken;

export const setRefreshToken = (token: string | null) => {
  try {
    if (token) {
      localStorage.setItem(REFRESH_STORAGE_KEY, token);
    } else {
      localStorage.removeItem(REFRESH_STORAGE_KEY);
    }
  } catch {
    // storage 不可用(隐私窗口等)时静默降级:仅当前会话内存有效
  }
};

export const getRefreshToken = (): string | null => {
  try {
    return localStorage.getItem(REFRESH_STORAGE_KEY);
  } catch {
    return null;
  }
};

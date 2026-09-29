import {
  getAccessToken,
  getRefreshToken,
  setAccessToken,
  setRefreshToken,
} from "./tokens";

import type { ApiBody, AuthErrorCode, User } from "./types";

// 生产同源(经 Caddy /api 转发);默认相对路径,本地 docker 经 build arg 覆盖。
export const API_URL: string = import.meta.env.VITE_APP_API_URL || "/api/v1";

export class ApiError extends Error {
  code: AuthErrorCode;

  constructor(code: AuthErrorCode, message: string) {
    super(message);
    this.code = code;
  }
}

type TokenData = {
  user: User;
  access_token: string;
  token_type: string;
  expires_in: number;
  refresh_token: string;
};

// 后端契约:HTTP 恒为 200,业务错误由 body.code 表达(0=成功)。
// 非 JSON 响应(网关 502 等)兜底为 50000。
const parseBody = async (res: Response): Promise<ApiBody> => {
  try {
    return await res.json();
  } catch {
    return { code: 50000, message: res.statusText, data: null, timestamp: 0 };
  }
};

const bodyError = (body: ApiBody): ApiError =>
  new ApiError(body.code as AuthErrorCode, body.message);

// 单飞:并发 40100 重试只触发一次 /refresh,避免 refresh 轮换互相吊销。
let refreshInFlight: Promise<User | null> | null = null;

const rawRefresh = async (): Promise<User | null> => {
  const refreshToken = getRefreshToken();
  if (!refreshToken) {
    setAccessToken(null);
    return null;
  }
  const res = await fetch(`${API_URL}/auth/refresh`, {
    method: "POST",
    headers: { Authorization: `Bearer ${refreshToken}` },
  });
  const body = await parseBody(res);
  if (body.code !== 0 || !body.data) {
    // refresh token 失效(过期/已轮换):清空本地,回到匿名态
    setAccessToken(null);
    setRefreshToken(null);
    return null;
  }
  const data = body.data as TokenData;
  setAccessToken(data.access_token);
  setRefreshToken(data.refresh_token);
  return data.user;
};

/** 用 refresh token 换新会话;失败返回 null(保持匿名)。 */
export const refreshSession = (): Promise<User | null> => {
  if (!refreshInFlight) {
    refreshInFlight = rawRefresh().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
};

// 供其他资源模块(canvas 等)复用:自动附带 Bearer 并在 40100 时静默续期重试。
export const fetchJson = async (
  path: string,
  init: RequestInit,
): Promise<ApiBody> => {
  const doFetch = () =>
    fetch(`${API_URL}${path}`, {
      ...init,
      headers: {
        ...(init.body ? { "Content-Type": "application/json" } : null),
        ...(getAccessToken()
          ? { Authorization: `Bearer ${getAccessToken()}` }
          : null),
      },
    });

  let body = await parseBody(await doFetch());
  if (body.code === 40100) {
    // access 过期或缺失(刷新页面后内存 token 为空):静默续期后重试一次。
    // 无 refresh token 时 refreshSession 立即返回 null,匿名请求不多打一枪。
    if (await refreshSession()) {
      body = await parseBody(await doFetch());
    }
  }
  return body;
};

const authRequest = async (path: string, body: unknown): Promise<User> => {
  const data = await parseBody(
    await fetch(`${API_URL}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
  if (data.code !== 0 || !data.data) {
    throw bodyError(data);
  }
  const token = data.data as TokenData;
  setAccessToken(token.access_token);
  setRefreshToken(token.refresh_token);
  return token.user;
};

export const register = (phone: string, password: string, nickname?: string) =>
  authRequest("/auth/register", { phone, password, nickname });

export const login = (phone: string, password: string) =>
  authRequest("/auth/login", { phone, password });

export const logout = async (): Promise<void> => {
  try {
    // 后端只按 Bearer 头里的 refresh token 吊销会话
    const refreshToken = getRefreshToken();
    const body = await parseBody(
      await fetch(`${API_URL}/auth/logout`, {
        method: "POST",
        ...(refreshToken
          ? { headers: { Authorization: `Bearer ${refreshToken}` } }
          : null),
      }),
    );
    if (body.code !== 0) {
      throw bodyError(body);
    }
  } finally {
    setAccessToken(null);
    setRefreshToken(null);
  }
};

export const fetchMe = async (): Promise<User> => {
  const body = await fetchJson("/me", { method: "GET" });
  if (body.code !== 0 || !body.data) {
    throw bodyError(body);
  }
  return (body.data as { user: User }).user;
};

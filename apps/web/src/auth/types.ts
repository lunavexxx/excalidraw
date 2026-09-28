export type User = {
  id: string;
  nickname: string;
  /** 服务端已脱敏(138****5678),明文手机号永不进前端 */
  phone_masked: string;
  created_at: string;
};

/**
 * 业务码与 apps/api/internal/apiresp 保持一致:
 * HTTP 恒为 200,错误全部由 code 表达(0=成功)。
 */
export type AuthErrorCode =
  | 40000 // invalid_request
  | 40100 // unauthorized
  | 40101 // invalid_credentials
  | 40900 // phone_taken
  | 50000; // internal

export type ApiBody<T = unknown> = {
  code: number;
  message: string;
  data: T;
  timestamp: number;
};

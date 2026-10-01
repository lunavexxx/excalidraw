// 配置:全部来自环境变量(compose 注入;敏感值与 apps/api 同源)。
export interface Config {
  port: number;
  redisUrl: string;
  databaseUrl: string;
  jwtSecret: string;
  goApiUrl: string; // 形如 http://api:8080/api/v1
  internalToken: string;
  corsOrigins: string[]; // dev 跨端口直连时需要;生产同源(经 Caddy)无需
  maxHttpBufferSize: number;
  rateLimitPerSecond: number;
  perIdentityConnectionLimit: number;
  persistQueueMax: number;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const required = (key: string): string => {
    const v = env[key];
    if (!v) {
      throw new Error(`missing required env ${key}`);
    }
    return v;
  };
  const int = (key: string, fallback: number): number => {
    const v = env[key];
    if (!v) return fallback;
    const n = Number.parseInt(v, 10);
    return Number.isFinite(n) && n > 0 ? n : fallback;
  };
  return {
    port: int("PORT", 3002),
    redisUrl: required("REDIS_URL"),
    databaseUrl: required("DATABASE_URL"),
    jwtSecret: required("JWT_SECRET"),
    goApiUrl: required("GO_API_URL").replace(/\/+$/, ""),
    internalToken: required("INTERNAL_TOKEN"),
    corsOrigins: (env.CORS_ORIGINS ?? "")
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean),
    maxHttpBufferSize: int("MAX_HTTP_BUFFER_SIZE", 6 * 1024 * 1024),
    rateLimitPerSecond: int("RATE_LIMIT_PER_SECOND", 150),
    perIdentityConnectionLimit: int("PER_USER_CONNECTION_LIMIT", 50),
    persistQueueMax: int("PERSIST_QUEUE_MAX", 10000),
  };
}

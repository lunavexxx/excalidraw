import { fetchJson } from "../auth/api";
import { CanvasApiError } from "../canvas/api";

import type {
  AccessEntry,
  AccessRequest,
  CanvasCapabilities,
  CanvasErrorCode,
  CanvasMember,
  CollaborationNotification,
  Invitation,
} from "../canvas/types";

export const accessApi = async <T>(
  path: string,
  method = "GET",
  body?: unknown,
  entry?: string,
): Promise<T> => {
  const result = await fetchJson(path, {
    method,
    ...(body ? { body: JSON.stringify(body) } : {}),
    ...(entry ? { headers: { "X-Access-Entry": entry } } : {}),
  });
  if (result.code !== 0) {
    throw new CanvasApiError(result.code as CanvasErrorCode, result.message);
  }
  return result.data as T;
};
export const members = (id: string, cursor = "", search = "") =>
  accessApi<{
    items: CanvasMember[];
    next_cursor: string;
    capabilities: CanvasCapabilities;
  }>(`/canvases/${id}/members?${new URLSearchParams({ cursor, search })}`);
export const invitations = (id: string) =>
  accessApi<{ items: Invitation[] }>(`/canvases/${id}/invitations`);
export const inviteByPhone = (id: string, phone: string, role: string) =>
  accessApi<{ invitation: Invitation; token: string }>(
    `/canvases/${id}/invitations`,
    "POST",
    { phone, role },
  );
export const accessEntries = (id: string) =>
  accessApi<{ items: AccessEntry[] }>(`/canvases/${id}/access-entries`);
export const createAccessEntry = (id: string) =>
  accessApi<{ entry: AccessEntry; token: string }>(
    `/canvases/${id}/access-entries`,
    "POST",
  );
export const requests = (id: string, entry?: string) =>
  accessApi<{ items: AccessRequest[] }>(
    `/canvases/${id}/access-requests`,
    "GET",
    undefined,
    entry,
  );
export const requestAccess = (
  id: string,
  role: string,
  reason: string,
  entry?: string,
) =>
  accessApi<AccessRequest>(
    `/canvases/${id}/access-requests`,
    "POST",
    { role, reason },
    entry,
  );
export const notifications = (before = 0) =>
  accessApi<{ items: CollaborationNotification[]; unread_count: number }>(
    `/notifications?before=${before}`,
  );

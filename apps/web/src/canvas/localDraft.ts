import { STORAGE_KEYS } from "../app_constants";

import { updateBrowserStateVersion } from "../data/tabSync";

/** 本地草稿(匿名时代的场景快照)是否有实际内容。 */
export const localDraftNonEmpty = () => {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.LOCAL_STORAGE_ELEMENTS);
    if (!raw) {
      return false;
    }
    const elements = JSON.parse(raw);
    return Array.isArray(elements) && elements.some((e: any) => !e?.isDeleted);
  } catch {
    return false;
  }
};

/** 清除本地草稿;须先 LocalData.flushSave(),防 debounce 陈旧参数复活草稿。 */
export const clearLocalDraft = () => {
  try {
    localStorage.removeItem(STORAGE_KEYS.LOCAL_STORAGE_ELEMENTS);
    localStorage.removeItem(STORAGE_KEYS.LOCAL_STORAGE_APP_STATE);
    localStorage.removeItem(STORAGE_KEYS.LOCAL_STORAGE_DRAFT_UPDATED_AT);
    updateBrowserStateVersion(STORAGE_KEYS.VERSION_DATA_STATE);
  } catch {
    // 无痕模式等 localStorage 不可用:忽略,清除已完成
  }
};

/** 本地草稿最后写入时间(毫秒时间戳);无记录返回 null。 */
export const localDraftUpdatedAt = (): number | null => {
  try {
    const raw = localStorage.getItem(
      STORAGE_KEYS.LOCAL_STORAGE_DRAFT_UPDATED_AT,
    );
    if (!raw) {
      return null;
    }
    const ts = Number(raw);
    return Number.isFinite(ts) && ts > 0 ? ts : null;
  } catch {
    return null;
  }
};

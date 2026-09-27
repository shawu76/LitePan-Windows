import { computed, ref } from "vue";
import { fetchSettings, saveSettings } from "@/api/settings";
import { toast } from "@/composables/useToast";

/**
 * 信息条（仪表带）在四个页面的开合偏好。
 *
 * 存储：落到后端设置（键见下方 SETTINGS_KEY），因此换浏览器/换设备一致，
 * 并且随系统「设置备份」一起导出、导入后还原。
 * 首帧先用 localStorage 缓存避免闪烁；服务端快照返回后**一律以服务端为准**，
 * 本地缓存只被服务端值覆盖，永不反向覆盖服务端。
 * 写入时本地乐观更新，失败回滚并提示。
 */

const SETTINGS_KEY: Record<string, string> = {
  strm: "ui_band_hidden_strm",
  cache: "ui_band_hidden_cache",
  organize: "ui_band_hidden_organize",
  fuse: "ui_band_hidden_fuse",
};

const CACHE_PREFIX = "litepan:admin:band:";

/** 服务端快照（按页面键）。 */
const serverValue = ref<Record<string, boolean>>({});
const loaded = ref(false);
let inflight: Promise<void> | null = null;

function cacheKey(page: string): string {
  return `${CACHE_PREFIX}${page}:hidden`;
}

function readCache(page: string): boolean {
  try {
    return localStorage.getItem(cacheKey(page)) === "1";
  } catch {
    return false;
  }
}

function writeCache(page: string, value: boolean): void {
  try {
    localStorage.setItem(cacheKey(page), value ? "1" : "0");
  } catch {
    /* 隐私模式等场景忽略 */
  }
}

/** 拉一次设置快照并按页面键刷新缓存/内存值；同一会话内只请求一次。 */
export function ensureBandSettings(): Promise<void> {
  if (inflight) return inflight;
  inflight = fetchSettings({ includeHidden: true })
    .then((payload) => {
      const items = payload.items ?? [];
      const next: Record<string, boolean> = {};
      for (const [page, key] of Object.entries(SETTINGS_KEY)) {
        const item = items.find((entry) => entry.key === key);
        // 只认服务端：本地缓存仅用于首帧占位，绝不能反过来覆盖服务端
        // （否则在 A 浏览器刷新会把 B 浏览器改过的状态推回去）。
        const value = String(item?.value ?? "false") === "true";
        next[page] = value;
        writeCache(page, value);
      }
      serverValue.value = next;
      loaded.value = true;
    })
    .catch(() => {
      // 读取失败时保留本地缓存值，并在下一次调用时允许重试。
      inflight = null;
    });
  return inflight;
}

export function useBandVisible(pageKey: string | { value: string }) {
  const pageOf = (): string => String(typeof pageKey === "string" ? pageKey : pageKey.value);
  void ensureBandSettings();

  const hidden = computed<boolean>({
    get: () => (loaded.value ? Boolean(serverValue.value[pageOf()]) : readCache(pageOf())),
    set: (value) => {
      const page = pageOf();
      serverValue.value = { ...serverValue.value, [page]: value };
      writeCache(page, value);

      const key = SETTINGS_KEY[page];
      if (!key) return;
      saveSettings({ [key]: value ? "true" : "false" }).catch(() => {
        // 已被后续操作覆盖就不再回滚，避免把新状态改坏。
        if (serverValue.value[page] !== value) return;
        serverValue.value = { ...serverValue.value, [page]: !value };
        writeCache(page, !value);
        toast.error("保存信息面板偏好失败，请稍后重试");
      });
    },
  });

  return {
    hidden,
    hide: () => (hidden.value = true),
    show: () => (hidden.value = false),
    toggle: () => (hidden.value = !hidden.value),
  };
}

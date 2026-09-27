export function formatSize(bytes: number, isDir = false): string {
  if (isDir) return "-";
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const val = bytes / Math.pow(1024, i);
  return `${val >= 10 || i === 0 ? Math.round(val) : val.toFixed(1)} ${units[i]}`;
}

export function fileExtension(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot >= 0 && dot + 1 < name.length ? name.slice(dot + 1).toLowerCase() : "";
}

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

function parseAPITime(value: string): Date | null {
  const raw = value.trim();
  if (!raw) return null;

  if (raw.includes("T") || /[zZ]|[+-]\d{2}:?\d{2}$/.test(raw)) {
    const date = new Date(raw);
    return Number.isNaN(date.getTime()) ? null : date;
  }

  const matched = raw.match(/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?$/);
  if (matched) {
    const date = new Date(
      Number(matched[1]),
      Number(matched[2]) - 1,
      Number(matched[3]),
      Number(matched[4]),
      Number(matched[5]),
      Number(matched[6] || 0),
    );
    return Number.isNaN(date.getTime()) ? null : date;
  }

  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? null : date;
}

export function formatTime(s?: string): string {
  if (!s?.trim()) return "-";
  const date = parseAPITime(s);
  if (!date) return s.trim();
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`;
}

export function formatTimeShort(s?: string): string {
  if (!s?.trim()) return "";
  const date = parseAPITime(s);
  if (!date) return s.trim();
  const now = new Date();
  const sameDay =
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate();
  if (sameDay) {
    return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  }
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatCompactDuration(ms: number): string {
  if (!ms || ms <= 0) return "";
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const min = Math.floor(seconds / 60);
  const sec = seconds % 60;
  return `${min}m${sec}s`;
}

export function formatElapsedMs(ms?: number): string {
  if (!ms || ms <= 0) return "";
  const seconds = Math.max(1, Math.floor(ms / 1000));
  if (seconds < 60) return `耗时 ${seconds}s`;
  const min = Math.floor(seconds / 60);
  const sec = seconds % 60;
  return `耗时 ${min}m${sec}s`;
}

export function formatRelativeTimeAgo(value?: string, emptyLabel = "从未刷新"): string {
  if (!value) return emptyLabel;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const diff = Date.now() - date.getTime();
  if (diff < 60_000) return "刚刚";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  return `${Math.floor(diff / 86_400_000)} 天前`;
}

/** 增强工具卡片标题搜索过滤：空查询匹配所有，否则大小写不敏感包含匹配。 */
export function containsQuery(title: string, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q || title.toLowerCase().includes(q);
}

/** 把未来时间点格式化成「今天 10:58 / 明天 09:30 / 09-14 08:00」；≤60 秒内返回「即将执行」。 */
export function formatRunTimeText(value: string | Date, now: Date = new Date()): string {
  const at = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(at.getTime())) return "—";
  if (at.getTime() <= now.getTime() + 60_000) return "即将执行";
  const clock = `${pad2(at.getHours())}:${pad2(at.getMinutes())}`;
  const dayStart = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const dayDiff = Math.round((dayStart(at) - dayStart(now)) / 86_400_000);
  if (dayDiff <= 0) return `今天 ${clock}`;
  if (dayDiff === 1) return `明天 ${clock}`;
  return `${pad2(at.getMonth() + 1)}-${pad2(at.getDate())} ${clock}`;
}

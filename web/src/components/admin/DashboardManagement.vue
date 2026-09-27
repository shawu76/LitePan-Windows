<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref } from "vue";
import { clearCache, fetchCacheStats, type CacheStats } from "@/api/cache";
import {
  type CacheRetentionStats,
  type CacheRetentionTask,
} from "@/api/cacheRetention";
import { getApiErrorMessage } from "@/api/client";
import { fetchDashboardOverview, type DashboardOverview } from "@/api/dashboard";
import type { FuseMount } from "@/api/fuse";
import type { LogStats } from "@/api/logs";
import type { MediaOrganizeTask } from "@/api/mediaOrganize";
import type { NotificationItem } from "@/api/notifications";
import type { Account } from "@/api/types";
import type { StrmTask } from "@/api/strm";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import AppCardActionButton from "@/components/base/AppCardActionButton.vue";
// 日志面板非默认 tab，按需加载,减小仪表盘首包。
const SystemLogs = defineAsyncComponent(() => import("@/components/admin/SystemLogs.vue"));
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { toast } from "@/composables/useToast";
import { formatRelativeTimeAgo, formatSize } from "@/utils/format";
import "@/styles/admin-shared.css";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const OVERVIEW_TAB = "overview";
const LOGS_TAB = "logs";
const OVERVIEW_CACHE_KEY = "litepan:dashboard:overview:v1";
const VALID_TABS = [OVERVIEW_TAB, LOGS_TAB] as const;

const tabs = [
  { key: OVERVIEW_TAB, label: "运行概况" },
  { key: LOGS_TAB, label: "系统日志" },
];

const { activeTab, setActiveTab } = useSectionTabRoute(OVERVIEW_TAB, VALID_TABS);
const logPresetLevel = ref<string | number>("");
const logPresetSeq = ref(0);

const accounts = ref<Account[]>([]);
const cacheStats = ref<CacheStats | null>(null);
const cacheRetentionTasks = ref<CacheRetentionTask[]>([]);
const cacheRetentionStats = ref<CacheRetentionStats | null>(null);
const fuseMounts = ref<FuseMount[]>([]);
const strmTasks = ref<StrmTask[]>([]);
const organizeTasks = ref<MediaOrganizeTask[]>([]);
const notifications = ref<NotificationItem[]>([]);
const unreadCount = ref(0);
const logStats = ref<LogStats | null>(null);
const loading = ref(true);
const refreshing = ref(false);
const clearingCache = ref(false);
const loadError = ref("");
const hasLoadedOverview = ref(false);
let overviewLoadSequence = 0;
useAdminPageLoading("dashboard", computed(() => activeTab.value === OVERVIEW_TAB && loading.value));

function applyOverview(data: DashboardOverview) {
  accounts.value = data.accounts ?? [];
  cacheStats.value = data.cache_stats ?? null;
  cacheRetentionTasks.value = data.cache_retention_tasks ?? [];
  cacheRetentionStats.value = data.cache_retention_stats ?? null;
  fuseMounts.value = data.fuse_mounts ?? [];
  strmTasks.value = data.strm_tasks ?? [];
  organizeTasks.value = data.organize_tasks ?? [];
  notifications.value = data.notifications ?? [];
  unreadCount.value = Number(data.unread_count || 0);
  logStats.value = data.log_stats ?? null;
}

function restoreOverview() {
  try {
    const raw = sessionStorage.getItem(OVERVIEW_CACHE_KEY);
    if (!raw) return false;
    const data = JSON.parse(raw) as DashboardOverview;
    if (!Array.isArray(data.accounts)) return false;
    applyOverview(data);
    return true;
  } catch {
    sessionStorage.removeItem(OVERVIEW_CACHE_KEY);
    return false;
  }
}

function saveOverview(data: DashboardOverview) {
  try {
    sessionStorage.setItem(OVERVIEW_CACHE_KEY, JSON.stringify(data));
  } catch {
    // 会话缓存不可用时不影响后台使用。
  }
}

if (restoreOverview()) {
  hasLoadedOverview.value = true;
  loading.value = false;
}

const accountCount = computed(() => accounts.value.length);
const activeAccountCount = computed(() => accounts.value.filter((account) => account.is_active).length);
const inactiveAccountCount = computed(() => Math.max(0, accountCount.value - activeAccountCount.value));
const authErrorAccountCount = computed(() => accounts.value.filter((account) => isAccountAuthError(account)).length);
const cooldownAccountCount = computed(() => accounts.value.filter((account) => isAccountCooldown(account)).length);

const enabledStrmCount = computed(
  () => strmTasks.value.filter((task) => isStrmTaskEnabled(task)).length,
);
const enabledCacheCount = computed(() => {
  if (cacheRetentionStats.value) return cacheRetentionStats.value.running;
  return cacheRetentionTasks.value.filter((task) => isCacheTaskEnabled(task)).length;
});
const enabledOrganizeCount = computed(
  () => organizeTasks.value.filter((task) => isOrganizeTaskEnabled(task)).length,
);
const enabledTaskCount = computed(
  () => enabledStrmCount.value + enabledCacheCount.value + enabledOrganizeCount.value,
);
const totalTaskCount = computed(
  () =>
    strmTasks.value.length +
    (cacheRetentionStats.value?.total ?? cacheRetentionTasks.value.length) +
    organizeTasks.value.length,
);
const mountedFuseCount = computed(() => fuseMounts.value.filter((mount) => mount.state === "mounted").length);
const totalFuseCount = computed(() => fuseMounts.value.length);

const recentErrorCount = computed(() => logStats.value?.recent_unacknowledged_errors ?? 0);
const recentErrorTotal = computed(() => logStats.value?.recent_errors ?? 0);
// 日志量极大时后端会按读取预算提前结束统计，此时数字只是下界，加「+」避免当成精确值。
const logTotalText = computed(() => {
  const total = logStats.value?.total ?? 0;
  return logStats.value?.truncated ? `${total}+` : String(total);
});
const systemStatus = computed(() => {
  if (authErrorAccountCount.value > 0) {
    return { label: "账号需要重新授权", tone: "danger", icon: "triangle-exclamation" };
  }
  if (cooldownAccountCount.value > 0) {
    return { label: "账号认证冷却中", tone: "warn", icon: "clock" };
  }
  if (recentErrorCount.value > 0) return { label: "需要留意", tone: "warn", icon: "triangle-exclamation" };
  if (inactiveAccountCount.value > 0) return { label: "部分账号未启用", tone: "warn", icon: "circle-info" };
  return { label: "运行正常", tone: "ok", icon: "check" };
});
const ovOnlineRate = computed(() =>
  accountCount.value > 0 ? Math.round((activeAccountCount.value / accountCount.value) * 100) : 100,
);
const ovRingOffset = computed(() => Math.round(327 * (1 - ovOnlineRate.value / 100)));
const ovStatusIcon = computed(() => (systemStatus.value.tone === "ok" ? "check" : "triangle-exclamation"));
const fuseSubline = computed(() => {
  if (totalFuseCount.value === 0) return "尚未创建挂载点";
  const missing = totalFuseCount.value - mountedFuseCount.value;
  return missing === 0 ? "全部已挂载" : `${missing} 个未挂载`;
});

const systemStatusText = computed(() => {
  if (authErrorAccountCount.value > 0) {
    return `${authErrorAccountCount.value} 个账号认证已失效，需要重新授权`;
  }
  if (cooldownAccountCount.value > 0) {
    return `${cooldownAccountCount.value} 个账号认证刷新失败，正在等待系统重试`;
  }
  if (recentErrorCount.value > 0) return `近 24 小时有 ${recentErrorTotal.value} 条错误日志，${recentErrorCount.value} 条待确认`;
  if (recentErrorTotal.value > 0) return "近 24 小时错误已确认，当前无新的待确认错误";
  if (inactiveAccountCount.value > 0) return `${inactiveAccountCount.value} 个账号未启用，其余模块正常`;
  return "账号、任务与缓存服务状态正常";
});
const canJumpToErrorLogs = computed(
  () => recentErrorCount.value > 0 && authErrorAccountCount.value === 0 && cooldownAccountCount.value === 0,
);

const generatedStrmCount = computed(() => strmTasks.value.reduce((sum, task) => sum + (task.generated_count || 0), 0));
const cacheHitRate = computed(() => `${Math.round(cacheStats.value?.hit_rate ?? 0)}%`);
const cacheItemCount = computed(() => cacheStats.value?.total_keys ?? 0);
const cacheSizeLabel = computed(() => formatSize(cacheStats.value?.total_size_bytes ?? 0));
const latestCacheRefresh = computed(() => latestTime(cacheRetentionTasks.value.map((task) => task.last_refresh)));
const latestStrmScan = computed(() => latestTime(strmTasks.value.map((task) => task.last_scan)));
const latestOrganizeRun = computed(() => latestTime(organizeTasks.value.map((task) => task.last_run_at)));

const sortedAccounts = computed(() =>
  [...accounts.value].sort((a, b) => {
    if (a.is_default !== b.is_default) return a.is_default ? -1 : 1;
    if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order;
    return a.id - b.id;
  }),
);
const taskSummaries = computed(() => [
  {
    title: "缓存任务",
    icon: "box-archive",
    count: cacheRetentionStats.value?.total ?? cacheRetentionTasks.value.length,
    enabled: enabledCacheCount.value,
    detail: `${cacheItemCount.value} 条缓存 · ${cacheHitRate.value} 命中率`,
    progress: taskProgress(
      enabledCacheCount.value,
      cacheRetentionStats.value?.total ?? cacheRetentionTasks.value.length,
    ),
    tone: "blue",
    updated: formatRelativeTimeAgo(latestCacheRefresh.value, "从未刷新"),
  },
  {
    title: "STRM 任务",
    icon: "film",
    count: strmTasks.value.length,
    enabled: enabledStrmCount.value,
    detail: `已生成 ${generatedStrmCount.value} 个播放文件`,
    progress: taskProgress(enabledStrmCount.value, strmTasks.value.length),
    tone: "purple",
    updated: formatRelativeTimeAgo(latestStrmScan.value, "从未扫描"),
  },
  {
    title: "目录整理",
    icon: "wand-magic-sparkles",
    count: organizeTasks.value.length,
    enabled: enabledOrganizeCount.value,
    detail: organizeTaskDetail.value,
    progress: taskProgress(enabledOrganizeCount.value, organizeTasks.value.length),
    tone: "amber",
    updated: formatRelativeTimeAgo(latestOrganizeRun.value, "从未执行"),
  },
]);

const organizeTaskDetail = computed(() => {
  const failed = organizeTasks.value.filter((task) => (task.last_run_result?.failed ?? 0) > 0).length;
  if (failed > 0) return `${failed} 个任务最近有失败项`;
  const renamed = organizeTasks.value.reduce((sum, task) => sum + (task.last_run_result?.renamed ?? 0), 0);
  return renamed > 0 ? `最近整理 ${renamed} 个条目` : "可预览后手动确认执行";
});

async function loadOverview() {
  const firstLoad = !hasLoadedOverview.value;
  const sequence = ++overviewLoadSequence;
  loading.value = firstLoad;
  refreshing.value = !firstLoad;
  loadError.value = "";
  try {
    const data = await fetchDashboardOverview();
    if (sequence !== overviewLoadSequence) return;
    applyOverview(data);
    saveOverview(data);
    hasLoadedOverview.value = true;
  } catch (error) {
    if (sequence === overviewLoadSequence) {
      loadError.value = getApiErrorMessage(error, "运行概况加载失败，已保留上次数据");
    }
  } finally {
    if (sequence === overviewLoadSequence) {
      loading.value = false;
      refreshing.value = false;
    }
  }
}

async function clearDashboardCache() {
  clearingCache.value = true;
  try {
    const res = await clearCache();
    toast.success(`已清空 ${res.cleared_count} 条缓存`);
    cacheStats.value = await fetchCacheStats();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清空缓存失败"));
  } finally {
    clearingCache.value = false;
  }
}

function openErrorLogs() {
  if (!canJumpToErrorLogs.value) return;
  logPresetLevel.value = 40;
  logPresetSeq.value += 1;
  setActiveTab(LOGS_TAB);
}

function handleLogStatsAcked(next: LogStats) {
  logStats.value = next;
}

function normalizeStatus(status?: string) {
  return (status || "").trim().toLowerCase();
}

function normalizeAuthStatus(account: Account) {
  return normalizeStatus(account.auth_status);
}

function isAccountAuthError(account: Account) {
  return account.is_active && ["token_expired", "failed"].includes(normalizeAuthStatus(account));
}

function isAccountCooldown(account: Account) {
  return account.is_active && normalizeAuthStatus(account) === "cooldown";
}

function isCacheTaskEnabled(task: CacheRetentionTask): boolean {
  return normalizeStatus(task.status) === "running";
}

function isStrmTaskEnabled(task: StrmTask): boolean {
  const status = normalizeStatus(task.status);
  return status === "active" || status === "running";
}

function isOrganizeTaskEnabled(task: MediaOrganizeTask): boolean {
  return normalizeStatus(task.status) !== "paused";
}

function taskProgress(enabled: number, total: number) {
  if (enabled <= 0 || total <= 0) return 0;
  return Math.max(8, Math.min(100, Math.round((enabled / total) * 100)));
}

function latestTime(values: Array<string | undefined>) {
  let latest = 0;
  for (const value of values) {
    if (!value) continue;
    const time = new Date(value).getTime();
    if (!Number.isNaN(time) && time > latest) latest = time;
  }
  return latest > 0 ? new Date(latest).toISOString() : "";
}

function parseAccountConfig(account: Account): Record<string, unknown> {
  if (!account.config) return {};
  try {
    const parsed = JSON.parse(account.config);
    return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function downloadModeLabel(account: Account) {
  const config = parseAccountConfig(account);
  const mode = String(config.download_mode ?? config.downloadMode ?? "").toLowerCase();
  const driverType = account.driver_type.toLowerCase();
  if (mode === "proxy") return "本机代理";
  if (mode === "redirect") return "302 直链";
  if (driverType.includes("baidu") || driverType.includes("quark")) return "本机代理";
  return "302 直链";
}

function downloadModeIcon(account: Account) {
  return downloadModeLabel(account) === "本机代理" ? "rotate" : "bolt";
}

function accountRowStyle(account: Account) {
  const color = normalizeHexColor(account.driver_card_color) || "#4c74df";
  return {
    "--account-color": color,
    "--account-soft": hexToRgba(color, 0.15),
    "--account-faint": hexToRgba(color, 0.045),
  };
}

function accountStatusClass(account: Account) {
  if (isAccountAuthError(account)) return "is-auth-error";
  if (isAccountCooldown(account)) return "is-cooldown";
  if (!account.is_active) return "is-disabled";
  return "is-active";
}

function accountStatusIcon(account: Account) {
  if (isAccountAuthError(account)) return "triangle-exclamation";
  if (isAccountCooldown(account)) return "clock";
  if (!account.is_active) return "circle-pause";
  return "circle-check";
}

function accountStatusLabel(account: Account) {
  if (isAccountAuthError(account)) return "失效";
  if (isAccountCooldown(account)) return "认证冷却中";
  if (!account.is_active) return "已禁用";
  return "正常";
}

function driverLabel(account: Account) {
  return account.driver_card_name || account.driver_type;
}

function accountSubline(account: Account) {
  return `${account.driver_type}${account.is_default ? " · 默认账号" : ""}`;
}

function fallbackLogoText(account: Account) {
  return (driverLabel(account).trim()[0] || "L").toUpperCase();
}

function normalizeHexColor(color?: string) {
  const raw = (color || "").trim();
  if (/^#[0-9a-fA-F]{6}$/.test(raw)) return raw;
  if (/^#[0-9a-fA-F]{3}$/.test(raw)) {
    const [, r, g, b] = raw;
    return `#${r}${r}${g}${g}${b}${b}`;
  }
  return "";
}

function hexToRgba(hex: string, alpha: number) {
  const normalized = normalizeHexColor(hex);
  if (!normalized) return `rgba(76, 116, 223, ${alpha})`;
  const value = Number.parseInt(normalized.slice(1), 16);
  const r = (value >> 16) & 255;
  const g = (value >> 8) & 255;
  const b = value & 255;
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

function notificationLevelClass(level: string) {
  const normalized = level.toLowerCase();
  if (normalized.includes("error") || normalized.includes("danger")) return "is-error";
  if (normalized.includes("warn")) return "is-warn";
  return "is-info";
}

onMounted(() => {
  void loadOverview();
});
</script>

<template>
  <div class="dashboard-page admin-tabbed-page">
    <SectionTabBar :model-value="activeTab" :tabs="tabs" @update:model-value="setActiveTab" />

    <div v-if="activeTab === OVERVIEW_TAB && !loading" class="dashboard-overview">
      <section class="ov-board" aria-label="运行概况" :class="`ov-board--${systemStatus.tone}`">
        <div class="ov-ring">
          <svg class="ov-ring__svg" viewBox="0 0 120 120" aria-hidden="true">
            <circle class="ov-ring__track" cx="60" cy="60" r="52" />
            <circle class="ov-ring__arc" :stroke-dashoffset="ovRingOffset" cx="60" cy="60" r="52" />
          </svg>
          <div class="ov-ring__center">
            <SvgIcon :name="ovStatusIcon" :size="24" />
            <div class="ov-ring__pct">{{ ovOnlineRate }}%</div>
            <div class="ov-ring__of">账号在线</div>
          </div>
          <div class="ov-ring__cap">健康度 · 实时</div>
        </div>

        <div class="ov-body">
          <div class="ov-status">
            <span class="ov-pulse" />
            <h2 class="ov-status__title">{{ systemStatus.label }}</h2>
            <span class="ov-status__detail">{{ systemStatusText }}</span>
          </div>

          <div v-if="loadError" class="dashboard-warning">
            <SvgIcon name="circle-info" size="1em" />
            <span>{{ loadError }}</span>
            <button type="button" :disabled="refreshing" @click="loadOverview">
              {{ refreshing ? "刷新中..." : "重试" }}
            </button>
          </div>

          <div class="ov-rows">
            <div class="ov-row ov-row--fuse">
              <span class="ov-row__ico"><SvgIcon name="plug" :size="15" /></span>
              <div class="ov-row__main">
                <span class="ov-row__name">FUSE 挂载点</span>
                <span class="ov-row__sub">{{ fuseSubline }}</span>
              </div>
              <div class="ov-row__right">
                <span class="ov-row__num">{{ mountedFuseCount }}<small>/ {{ totalFuseCount }}</small></span>
              </div>
            </div>

            <div class="ov-row ov-row--cache">
              <span class="ov-row__ico"><SvgIcon name="hand-database" :size="15" /></span>
              <div class="ov-row__main">
                <span class="ov-row__name">缓存空间</span>
                <span class="ov-row__sub">{{ cacheSizeLabel }}</span>
              </div>
              <div class="ov-row__right">
                <AppCardActionButton
                  icon-class="trash-can"
                  label="清理"
                  variant="danger"
                  :disabled="clearingCache"
                  @click="clearDashboardCache"
                />
              </div>
            </div>

            <div class="ov-row ov-row--errors" :class="{ 'is-warn': recentErrorCount > 0 }">
              <span class="ov-row__ico"><SvgIcon :name="recentErrorCount > 0 ? 'triangle-exclamation' : 'check'" :size="15" /></span>
              <div class="ov-row__main">
                <span class="ov-row__name">待确认错误</span>
                <span class="ov-row__sub">近 24 小时运行日志</span>
              </div>
              <div class="ov-row__right">
                <span class="ov-row__num">{{ recentErrorCount }}<small>条</small></span>
                <button v-if="canJumpToErrorLogs" type="button" class="ov-row__act" @click="openErrorLogs">
                  查看详情
                </button>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section class="dashboard-layout">
        <article class="dashboard-panel dashboard-panel--accounts">
          <header class="dashboard-panel__head">
            <div>
              <h3>存储账号</h3>
              <p>{{ accountCount }} 个接入 · {{ activeAccountCount }} 个在线</p>
            </div>
            <button type="button" class="dashboard-link-button" :disabled="refreshing" @click="loadOverview">
              <SvgIcon name="rotate-right" size="1em" :class="{ 'is-spinning': refreshing }" />
              刷新
            </button>
          </header>

          <div v-if="sortedAccounts.length" class="account-list">
            <div
              v-for="account in sortedAccounts"
              :key="account.id"
              class="account-row"
              :class="{
                'is-disabled': !account.is_active,
                'is-auth-error': isAccountAuthError(account),
                'is-cooldown': isAccountCooldown(account),
              }"
              :style="accountRowStyle(account)"
            >
              <img
                v-if="account.driver_card_logo"
                class="account-logo"
                :src="account.driver_card_logo"
                :alt="driverLabel(account)"
              />
              <div v-else class="account-logo account-logo--text">{{ fallbackLogoText(account) }}</div>
              <div class="account-row__main">
                <strong>
                  {{ account.name }}
                  <span v-if="account.is_default" class="default-tag">默认</span>
                </strong>
                <small>{{ accountSubline(account) }}</small>
              </div>
              <span class="method-tag">
                <SvgIcon :name="downloadModeIcon(account)" size="1em" />
                {{ downloadModeLabel(account) }}
              </span>
              <span
                class="status-tag"
                :class="accountStatusClass(account)"
                :title="accountStatusLabel(account)"
              >
                <SvgIcon :name="accountStatusIcon(account)" size="1em" />
                {{ accountStatusLabel(account) }}
              </span>
            </div>
          </div>
          <div v-else class="panel-empty">还没有添加存储账号</div>
        </article>

        <aside class="dashboard-side">
          <article class="dashboard-panel">
            <header class="dashboard-panel__head">
            <div>
              <h3>后台任务</h3>
              <p>{{ totalTaskCount }} 个任务 · {{ enabledTaskCount }} 个运行中</p>
            </div>
          </header>

            <div class="task-list">
              <div v-for="task in taskSummaries" :key="task.title" class="task-row">
                <div class="task-row__icon" :class="`task-row__icon--${task.tone}`">
                  <SvgIcon :name="task.icon" size="1em" />
                </div>
                <div class="task-row__main">
                  <div class="task-row__title">
                    <strong>{{ task.title }}</strong>
                    <span>{{ task.count }} 个</span>
                  </div>
                  <div class="task-progress" aria-hidden="true">
                    <span :style="{ width: `${task.progress}%` }" />
                  </div>
                  <small>{{ task.detail }} · {{ task.updated }}</small>
                </div>
              </div>
            </div>
          </article>

          <article class="dashboard-panel">
            <header class="dashboard-panel__head">
              <div>
                <h3>日志与通知</h3>
                <p>近 24 小时</p>
              </div>
            </header>

            <div class="log-snapshot">
              <div>
                <strong>{{ logTotalText }}</strong>
                <span>日志总数</span>
              </div>
              <div>
                <strong>{{ unreadCount }}</strong>
                <span>未读通知</span>
              </div>
            </div>

            <!-- 多条未读通知聚合为一张卡片，避免面板被逐条渲染拉长；仅最新一条做摘要。 -->
            <div v-if="unreadCount > 0" class="notice-list">
              <div
                class="notice-row"
                :class="notificationLevelClass(notifications[0]?.level ?? 'info')"
              >
                <SvgIcon name="bell" size="1em" />
                <div>
                  <strong>有 {{ unreadCount }} 条未读通知</strong>
                  <small v-if="notifications.length">
                    {{ notifications[0].title }} · {{ formatRelativeTimeAgo(notifications[0].created_at, "") }}
                  </small>
                  <small v-else>点击右上角铃铛查看</small>
                </div>
              </div>
            </div>
            <div v-else class="notice-good">
              <SvgIcon name="check" size="1em" />
              <div>
                <strong>通知状态正常</strong>
                <small>暂无未处理通知</small>
              </div>
            </div>
          </article>
        </aside>
      </section>
    </div>

    <SystemLogs
      v-else-if="activeTab === LOGS_TAB"
      :preset-level="logPresetLevel"
      :preset-seq="logPresetSeq"
      @acked-errors="handleLogStatsAcked"
    />
  </div>
</template>

<style scoped>.dashboard-page {
  padding-bottom: 24px;
}
.dashboard-overview {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.dashboard-panel {
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-lg);
  background: var(--surface);
  box-shadow: var(--shadow-soft);
}
.dashboard-panel h3 {
  margin: 0;
  color: var(--text);
}

.dashboard-warning {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--warning) 10%, var(--surface));
  color: var(--text);
  font-size: 13px;
}
.dashboard-warning span {
  flex: 1;
  min-width: 0;
}
.dashboard-warning button, .dashboard-link-button {
  border: 0;
  background: transparent;
  color: var(--brand);
  font: inherit;
  font-weight: 700;
  cursor: pointer;
}
.dashboard-warning button:disabled, .dashboard-link-button:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.dashboard-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(360px, 0.55fr);
  gap: 16px;
}
.dashboard-panel {
  padding: 20px;
}
.dashboard-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}
.dashboard-panel__head h3 {
  display: flex;
  align-items: center;
  font-size: 15px;
  font-weight: 800;
}
.dashboard-panel__head h3::before {
  display: none;
}
.dashboard-panel__head p {
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: 12px;
}
.dashboard-link-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
}
.is-spinning {
  animation: spin 0.9s linear infinite;
}
.account-list, .dashboard-side, .task-list, .notice-list {
  display: grid;
  gap: 10px;
}
.account-list {
  max-height: 532px;
  overflow-x: hidden;
  overflow-y: auto;
}
.account-row {
  display: grid;
  grid-template-columns: 46px minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: 0;
  border-radius: var(--radius-md);
  background:
    linear-gradient(
      90deg,
      var(--account-soft),
      color-mix(in srgb, var(--surface) 88%, var(--account-color)) 52%,
      var(--surface)
    ),
    var(--surface);
}
.account-row:nth-child(2n) {
  background:
    linear-gradient(
      90deg,
      var(--account-soft),
      color-mix(in srgb, var(--surface) 92%, var(--account-color)) 58%,
      var(--surface)
    ),
    var(--surface);
}
.account-row.is-disabled {
  opacity: 0.86;
}
.account-row.is-auth-error {
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--danger) 24%, transparent);
  background:
    linear-gradient(
      90deg,
      color-mix(in srgb, var(--danger) 13%, var(--surface)),
      color-mix(in srgb, var(--surface) 90%, var(--danger)) 52%,
      var(--surface)
    ),
    var(--surface);
}
.account-row.is-cooldown {
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--warning) 22%, transparent);
}
.account-logo {
  width: 42px;
  height: 42px;
  object-fit: contain;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--surface) 88%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--surface) 55%, transparent);
}
.account-logo--text {
  display: grid;
  place-items: center;
  background: linear-gradient(135deg, var(--account-color), rgba(17, 168, 232, 0.88));
  color: #fff;
  font-weight: 800;
}
.account-row__main {
  min-width: 0;
}
.account-row__main strong {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--text);
  font-size: 14px;
}
.account-row__main small {
  display: block;
  margin-top: 2px;
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.default-tag {
  height: 20px;
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  padding: 0 7px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
  color: var(--brand);
  font-size: 11px;
  font-weight: 800;
}
.method-tag, .status-tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}
.method-tag {
  color: var(--text-muted);
}
.method-tag i, .method-tag .lp-svg-icon {
  color: var(--account-color);
  font-size: 11px;
}
.status-tag {
  color: var(--success);
}
.status-tag.is-disabled {
  color: var(--warning);
}
.status-tag.is-cooldown {
  color: var(--warning);
}
.status-tag.is-auth-error {
  color: var(--danger);
}
.panel-empty {
  display: grid;
  place-items: center;
  min-height: 180px;
  border: 1px dashed var(--border-soft);
  border-radius: var(--radius-md);
  color: var(--text-muted);
  font-size: 13px;
}
.task-row {
  display: grid;
  grid-template-columns: 42px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border-soft);
}
.task-row:last-child {
  border-bottom: 0;
}
.task-row__icon {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
  color: var(--brand);
}
.task-row__icon--purple {
  background: color-mix(in srgb, #8b5cf6 12%, var(--surface));
  color: #8b5cf6;
}
.task-row__icon--amber {
  background: color-mix(in srgb, var(--warning) 10%, var(--surface));
  color: var(--warning);
}
.task-row__main {
  min-width: 0;
}
.task-row__title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.task-row__title strong {
  color: var(--text);
  font-size: 14px;
}
.task-row__title span, .task-row small {
  color: var(--text-muted);
  font-size: 12px;
}
.task-progress {
  height: 4px;
  margin: 8px 0 6px;
  overflow: hidden;
  border-radius: var(--radius-pill);
  background: var(--border-soft);
}
.task-progress span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--brand-gradient-h);
}
.log-snapshot {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin-bottom: 12px;
}
.log-snapshot > div {
  padding: 14px;
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
}
.log-snapshot strong {
  display: block;
  color: var(--text);
  font-size: 22px;
  line-height: 1.1;
}
.log-snapshot span {
  color: var(--text-muted);
  font-size: 12px;
}
.notice-row, .notice-good {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
}
.notice-row > i, .notice-row > .lp-svg-icon, .notice-good > i, .notice-good > .lp-svg-icon {
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
  color: var(--brand);
}
.notice-row.is-warn > i {
  background: color-mix(in srgb, var(--warning) 10%, var(--surface));
  color: var(--warning);
}
.notice-row.is-error > i {
  background: color-mix(in srgb, var(--danger) 10%, var(--surface));
  color: var(--danger);
}
.notice-row strong, .notice-good strong {
  display: block;
  color: var(--text);
  font-size: 13px;
  line-height: 1.4;
}
.notice-row small, .notice-good small {
  display: block;
  margin-top: 3px;
  color: var(--text-muted);
  font-size: 12px;
}
.notice-good {
  background: color-mix(in srgb, var(--success) 10%, var(--surface));
  border-color: color-mix(in srgb, var(--success) 22%, var(--border));
}
.notice-good > i, .notice-good > .lp-svg-icon {
  background: var(--surface);
  color: var(--success);
}
@media (max-width: 1180px) {

  .dashboard-layout {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 760px) {
  .dashboard-layout {
    grid-template-columns: 1fr;
  }

  .hero-metric {
    min-width: 0;
    padding: 10px 12px;
    border-left: 0;
    border-radius: var(--radius-sm);
    background: var(--surface-sunken);
  }

  .account-row {
    grid-template-columns: 42px minmax(0, 1fr);
  }

  .method-tag,
  .status-tag {
    justify-self: start;
  }
}
/* ===== 运行概况：左环 + 右侧三行数据（合并原状态栏与四卡片） ===== */.ov-board {
  --tone: var(--success);
  --tone-soft: color-mix(in srgb, var(--tone) 9%, var(--surface));
  display: grid;
  grid-template-columns: 218px minmax(0, 1fr);
  background: var(--surface);
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-soft);
  overflow: hidden;
}
.ov-board--ok { --tone: var(--success); }
.ov-board--warn { --tone: var(--warning); }
.ov-board--danger { --tone: var(--danger); }
.ov-ring {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 24px 16px;
  background: radial-gradient(circle at 50% 42%, var(--tone-soft), var(--surface) 74%);
  border-right: 1px solid var(--border-soft);
  transition: background 0.3s;
}
.ov-ring__svg { width: 128px; height: 128px; transform: rotate(-90deg); }
.ov-ring__track { fill: none; stroke: var(--border-soft); stroke-width: 9; }
.ov-ring__arc {
  fill: none;
  stroke: var(--tone);
  stroke-width: 9;
  stroke-linecap: round;
  stroke-dasharray: 327;
  transition: stroke-dashoffset 0.8s cubic-bezier(0.22, 1, 0.36, 1), stroke 0.3s;
}
.ov-ring__center {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -58%);
  text-align: center;
  pointer-events: none;
  color: var(--tone);
}
.ov-ring__pct {
  margin-top: 4px;
  font-size: 19px;
  font-weight: 800;
  font-variant-numeric: tabular-nums;
  line-height: 1;
  color: var(--text);
}
.ov-ring__of { font-size: 11px; color: var(--text-muted); margin-top: 2px; }
.ov-ring__cap { font-size: 11.5px; color: var(--text-muted); font-weight: 600; }
.ov-body { padding: 18px 22px 16px; min-width: 0; }
.ov-status {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border-soft);
}
.ov-status__title { margin: 0; font-size: 17px; }
.ov-status__detail { font-size: 12px; color: var(--text-muted); margin-left: auto; text-align: right; }
.ov-pulse {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--tone);
  flex: 0 0 auto;
  animation: ovPulse 2.2s ease-out infinite;
}
@keyframes ovPulse {
  0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--tone) 40%, transparent); }
  70% { box-shadow: 0 0 0 7px transparent; }
  100% { box-shadow: 0 0 0 0 transparent; }
}
.ov-rows { padding-top: 2px; }
.ov-row {
  display: grid;
  grid-template-columns: 30px minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 11px 2px;
  border-bottom: 1px solid var(--border-soft);
  transition: background 0.15s;
}
.ov-row:last-child { border-bottom: 0; }
.ov-row:hover { background: color-mix(in srgb, var(--border-soft) 45%, transparent); }
.ov-row__ico {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  display: grid;
  place-items: center;
  color: #0284c7;
  background: color-mix(in srgb, #0284c7 12%, var(--surface));
}
.ov-row--cache .ov-row__ico { color: var(--warning); background: color-mix(in srgb, var(--warning) 14%, var(--surface)); }
.ov-row--errors .ov-row__ico { color: var(--success); background: color-mix(in srgb, var(--success) 14%, var(--surface)); }
.ov-row--errors.is-warn .ov-row__ico { color: var(--danger); background: color-mix(in srgb, var(--danger) 13%, var(--surface)); }
.ov-row__main { display: flex; align-items: center; gap: 8px; min-width: 0; }
.ov-row__name { font-size: 13px; color: var(--text-regular); }
.ov-row__sub { font-size: 11.5px; color: var(--text-muted); }
.ov-row__right { display: flex; align-items: center; gap: 14px; }
.ov-row__num {
  min-width: 74px;
  display: flex;
  align-items: baseline;
  justify-content: flex-end;
  gap: 4px;
  font-size: 19px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.3px;
}
.ov-row__num small { font-size: 11px; font-weight: 600; color: var(--text-muted); }
.ov-row--errors.is-warn .ov-row__num { color: var(--danger); }
.ov-row__act {
  border: 1px solid var(--border);
  background: var(--surface-sunken);
  color: var(--text-regular);
  border-radius: var(--radius-sm);
  padding: 4px 10px;
  font: 600 11.5px var(--font);
  cursor: pointer;
}
.ov-row__act:hover { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 35%, var(--border)); }
@media (max-width: 900px) {
  .ov-board { grid-template-columns: minmax(0, 1fr); }
  .ov-ring { border-right: 0; border-bottom: 1px solid var(--border-soft); }
  .ov-status { flex-wrap: wrap; }
  .ov-status__detail { margin-left: 0; width: 100%; text-align: left; }
}

</style>

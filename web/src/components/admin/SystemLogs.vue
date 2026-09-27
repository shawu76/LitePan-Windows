<script lang="ts">
import { ref } from "vue";
import type { LogEntry, LogStats } from "@/api/logs";
import "@/styles/system-logs.css";

  // 会话缓存保留日志和筛选，重新进入时先显示旧结果再后台刷新。
const logs = ref<LogEntry[]>([]);
const stats = ref<LogStats | null>(null);
const level = ref<string | number>("");
const module = ref("");
const period = ref("all");
const keyword = ref("");
const page = ref(1);
const hasMore = ref(false);
</script>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { LOG_LEVELS, LOG_MODULE_GROUPS, LOG_PERIODS, logsApi } from "@/api/logs";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import { formatTime, formatTimeShort } from "@/utils/format";

const props = withDefaults(
  defineProps<{
    presetLevel?: string | number;
    presetSeq?: number;
  }>(),
  {
    presetLevel: "",
    presetSeq: 0,
  },
);
const emit = defineEmits<{
  ackedErrors: [stats: LogStats];
}>();

const api = logsApi();

const loading = ref(false);
const cleaningKeepToday = ref(false);
const cleaningAll = ref(false);
const acknowledgingRecentErrors = ref(false);
const expanded = ref<Set<number>>(new Set());
const logsPanelRef = ref<HTMLElement | null>(null);

const PAGE_SIZE = 50;

const activeModuleCount = computed(() =>
  stats.value?.by_module ? Object.keys(stats.value.by_module).length : 0,
);
const recentErrorCount = computed(() => stats.value?.recent_errors ?? 0);
const recentUnacknowledgedErrorCount = computed(() => stats.value?.recent_unacknowledged_errors ?? 0);
const canAcknowledgeRecentErrors = computed(() => recentUnacknowledgedErrorCount.value > 0);

let searchTimer: ReturnType<typeof setTimeout> | undefined;
let loadSequence = 0;

function levelClass(lv: number): string {
  if (lv >= 40) return "error";
  if (lv >= 30) return "warning";
  if (lv >= 20) return "info";
  return "debug";
}

function levelLabel(log: LogEntry): string {
  return `${log.level_emoji} ${log.level_name}`;
}

function levelSegmentCount(key: string): number {
  if (!stats.value) return 0;
  if (key === "") return stats.value.total;
  return stats.value.by_level?.[key] ?? 0;
}

function selectLevel(value: string | number) {
  if (level.value === value) return;
  level.value = value;
  onFilterChange();
}

function periodRange(): { start_time?: string } {
  const now = new Date();
  if (period.value === "today") {
    const start = new Date(now);
    start.setHours(0, 0, 0, 0);
    return { start_time: start.toISOString() };
  }
  if (period.value === "24h") {
    return { start_time: new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString() };
  }
  if (period.value === "7d") {
    return { start_time: new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString() };
  }
  return {};
}

function buildQuery() {
  const q: Record<string, string | number | undefined> = {
    ...periodRange(),
    // 多取一条仅用于判断是否还有下一页，页面仍只渲染 50 条。
    limit: PAGE_SIZE + 1,
    offset: (page.value - 1) * PAGE_SIZE,
  };
  if (level.value !== "") q.level = Number(level.value);
  if (module.value) q.module = module.value;
  const kw = keyword.value.trim();
  if (kw) q.keyword = kw;
  return q;
}

async function loadLogs(): Promise<boolean> {
  const sequence = ++loadSequence;
  // 仅在当前无内容时显示整块加载占位；有缓存时后台静默刷新，列表不空屏。
  loading.value = true;
  try {
    const result = await api.list(buildQuery());
    if (sequence !== loadSequence) return false;
    hasMore.value = result.length > PAGE_SIZE;
    logs.value = result.slice(0, PAGE_SIZE);
    return true;
  } catch (e) {
    if (sequence === loadSequence) {
      toast.error(getApiErrorMessage(e, "加载日志失败"));
    }
    return false;
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
}

async function loadStats() {
  try {
    stats.value = await api.stats();
  } catch {
    /* 统计失败不阻断列表 */
  }
}

async function refreshAll() {
  resetPage();
  await Promise.all([loadStats(), loadLogs()]);
}

async function loadInitialPage() {
  const returningFromOlderPage = page.value > 1;
  resetPage();
  if (returningFromOlderPage) logs.value = [];
  await loadLogs();
  // 先返回首屏日志；全量统计使用缓存并在列表之后刷新，不阻塞日志展示。
  void loadStats();
}

async function cleanupKeepToday() {
  try {
    await confirm({
      title: "清理今天之外的日志？",
      message: "将保留今天的日志，删除今天之前的全部旧日志文件。",
      confirmText: "清理",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  cleaningKeepToday.value = true;
  try {
    const result = await api.cleanupKeepToday();
    toast.success(
      result.deleted_files > 0 ? `已清理 ${result.deleted_files} 个今天之外的旧日志文件` : "无需清理，当前只有今天的日志",
    );
    await refreshAll();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清理日志失败"));
  } finally {
    cleaningKeepToday.value = false;
  }
}

async function cleanupAllLogs() {
  try {
    await confirm({
      title: "清理全部日志？",
      message: "将删除全部日志文件，包括今天的日志。后续新日志会重新生成。",
      confirmText: "全部清理",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  cleaningAll.value = true;
  try {
    const result = await api.cleanupAll();
    toast.success(result.deleted_files > 0 ? `已清理 ${result.deleted_files} 个日志文件` : "当前没有可清理的日志文件");
    await refreshAll();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清理全部日志失败"));
  } finally {
    cleaningAll.value = false;
  }
}

async function ackRecentErrors() {
  if (!canAcknowledgeRecentErrors.value) return;
  acknowledgingRecentErrors.value = true;
  try {
    const next = await api.ackRecentErrors();
    stats.value = next;
    emit("ackedErrors", next);
    toast.success("已知晓当前错误，后续新错误会再次提醒");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "确认当前错误失败"));
  } finally {
    acknowledgingRecentErrors.value = false;
  }
}

function onFilterChange() {
  resetPage();
  void loadLogs();
}

function onKeywordInput() {
  clearTimeout(searchTimer);
  resetPage();
  searchTimer = setTimeout(() => void loadLogs(), 350);
}

function resetFilters() {
  level.value = "";
  module.value = "";
  period.value = "all";
  keyword.value = "";
  void refreshAll();
}

function resetPage() {
  page.value = 1;
  hasMore.value = false;
  expanded.value = new Set();
}

function applyPresetFilter(triggerRefresh: boolean) {
  if (props.presetLevel === "" || props.presetLevel === undefined || props.presetLevel === null) return;
  level.value = props.presetLevel;
  module.value = "";
  period.value = "24h";
  keyword.value = "";
  if (triggerRefresh) void refreshAll();
}

async function changePage(target: number) {
  if (loading.value || target < 1 || (target > page.value && !hasMore.value)) return;
  const previous = page.value;
  page.value = target;
  expanded.value = new Set();
  if (!(await loadLogs())) {
    page.value = previous;
    return;
  }
  logsPanelRef.value?.scrollIntoView({ behavior: "smooth", block: "start" });
}

function toggleRow(id: number) {
  const next = new Set(expanded.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  expanded.value = next;
}

function hasDetails(log: LogEntry): boolean {
  return !!log.details && Object.keys(log.details).length > 0;
}

function detailsText(log: LogEntry): string {
  return formatDetails(log.details ?? {});
}

function formatDetails(details: Record<string, unknown>) {
  return JSON.stringify(details, null, 2);
}

const moduleOptions = LOG_MODULE_GROUPS.map((o) => ({ value: o.value, label: o.label }));
const periodOptions = LOG_PERIODS.map((o) => ({ value: o.value, label: o.label }));

watch(
  () => props.presetSeq,
  (next, prev) => {
    if (next > 0 && next !== prev) applyPresetFilter(true);
  },
);

onMounted(() => {
  if (props.presetSeq > 0) applyPresetFilter(false);
  void loadInitialPage();
});
onUnmounted(() => clearTimeout(searchTimer));
</script>

<template>
  <div class="logs-page">
    <section class="logs-board">
      <!-- 统计带：原 4 张统计卡 + 未确认提示条合并成一条 -->
      <div v-if="stats" class="logs-meta">
        <span class="logs-meta__item">总日志 <b>{{ stats.total }}</b></span>
        <span
          class="logs-meta__item"
          :class="{ 'logs-meta__item--error': recentErrorCount > 0 }"
        >
          近 24h 错误 <b>{{ recentErrorCount }}</b>
        </span>
        <span class="logs-meta__item">活跃模块 <b>{{ activeModuleCount }}</b></span>
        <span class="logs-meta__item">本页 <b>{{ logs.length }}</b></span>
        <span class="logs-meta__spacer" />
        <button
          v-if="canAcknowledgeRecentErrors"
          type="button"
          class="logs-ack-pill"
          :disabled="acknowledgingRecentErrors"
          title="确认后“运行概况”会恢复正常，后续新错误仍会再次提醒。"
          @click="ackRecentErrors"
        >
          <SvgIcon name="check" :size="12" />
          <span>{{ recentUnacknowledgedErrorCount }} 条未确认 · 一键确认</span>
        </button>
      </div>

      <!-- 工具条：搜索 + 级别段控件 + 模块/时间 + 图标动作 -->
      <div class="logs-toolbar">
        <label class="logs-search" for="log-keyword">
          <SvgIcon name="magnifying-glass" :size="14" />
          <AppInput
            id="log-keyword"
            v-model="keyword"
            placeholder="搜索消息内容…"
            @update:model-value="onKeywordInput"
          />
        </label>

        <div class="logs-seg" role="group" aria-label="级别筛选">
          <button
            v-for="seg in LOG_LEVELS"
            :key="seg.label"
            type="button"
            class="logs-seg__btn"
            :class="{ 'logs-seg__btn--on': level === seg.value }"
            :aria-pressed="level === seg.value"
            @click="selectLevel(seg.value)"
          >
            {{ seg.label }}
            <span v-if="stats" class="logs-seg__count">{{ levelSegmentCount(seg.key) }}</span>
          </button>
        </div>

        <div class="logs-select">
          <AppSelect v-model="module" :options="moduleOptions" @update:model-value="onFilterChange" />
        </div>
        <div class="logs-select">
          <AppSelect v-model="period" :options="periodOptions" @update:model-value="onFilterChange" />
        </div>

        <div class="logs-actions">
          <button
            type="button"
            class="logs-action-btn logs-action-btn--primary"
            :disabled="loading"
            title="刷新"
            aria-label="刷新"
            @click="refreshAll"
          >
            <span class="logs-action-btn__icon">
              <SvgIcon :name="'hand-sync-alt'" :size="18" :class-name="loading ? 'logs-action-btn__icon-spin' : ''" />
            </span>
          </button>
          <button
            type="button"
            class="logs-action-btn"
            title="重置"
            aria-label="重置"
            @click="resetFilters"
          >
            <span class="logs-action-btn__icon"><SvgIcon name="hand-undo-alt" :size="18" /></span>
          </button>
          <button
            type="button"
            class="logs-action-btn logs-action-btn--warning"
            :disabled="cleaningKeepToday"
            title="清理今天之外的"
            aria-label="清理今天之外的"
            @click="cleanupKeepToday"
          >
            <span class="logs-action-btn__icon">
              <SvgIcon
                :name="cleaningKeepToday ? 'hand-sync-alt' : 'hand-eraser'"
                :size="18"
                :class-name="cleaningKeepToday ? 'logs-action-btn__icon-spin' : ''"
              />
            </span>
          </button>
          <button
            type="button"
            class="logs-action-btn logs-action-btn--danger"
            :disabled="cleaningAll"
            title="清理所有"
            aria-label="清理所有"
            @click="cleanupAllLogs"
          >
            <span class="logs-action-btn__icon">
              <SvgIcon
                :name="cleaningAll ? 'hand-sync-alt' : 'hand-trash-alt'"
                :size="18"
                :class-name="cleaningAll ? 'logs-action-btn__icon-spin' : ''"
              />
            </span>
          </button>
        </div>
      </div>

      <div ref="logsPanelRef" class="logs-panel">
        <AppStateBlock v-if="loading && logs.length === 0" message="正在加载日志…" loading min-height="360px" />
        <AppStateBlock v-else-if="logs.length === 0" message="当前筛选条件下暂无日志" min-height="360px" />
        <template v-else>
          <div class="logs-list" :class="{ 'logs-list--loading': loading }">
            <article
              v-for="log in logs"
              :key="log.id"
              class="log-row"
              :class="[`log-row--${levelClass(log.level)}`, { 'log-row--open': expanded.has(log.id) }]"
              role="button"
              tabindex="0"
              :aria-expanded="expanded.has(log.id)"
              @click="toggleRow(log.id)"
              @keydown.enter.prevent="toggleRow(log.id)"
              @keydown.space.prevent="toggleRow(log.id)"
            >
              <time class="log-row__time" :title="formatTime(log.timestamp)">
                {{ formatTimeShort(log.timestamp) }}
              </time>
              <span class="log-row__level">{{ levelLabel(log) }}</span>
              <span class="log-row__module" :style="{ '--module-color': log.module_color }">
                <span class="log-row__dot" aria-hidden="true" />
                <span class="log-row__module-name">{{ log.module_name }}</span>
              </span>
              <span class="log-row__message">{{ log.message }}</span>
              <span class="log-row__chevron"><SvgIcon name="hand-chevron-down" :size="14" /></span>

              <div v-if="expanded.has(log.id)" class="log-row__detail" @click.stop>
                <p class="log-row__full">{{ log.message }}</p>
                <div v-if="log.driver_name || log.account_id" class="log-row__chips">
                  <span v-if="log.driver_name" class="log-meta-chip">驱动 {{ log.driver_name }}</span>
                  <span v-if="log.account_id" class="log-meta-chip">账号 {{ log.account_id }}</span>
                </div>
                <pre v-if="hasDetails(log)" class="log-row__json">{{ detailsText(log) }}</pre>
              </div>
            </article>
          </div>
          <nav v-if="page > 1 || hasMore" class="logs-pagination" aria-label="日志分页">
            <button
              type="button"
              class="logs-pagination__button"
              :disabled="page <= 1 || loading"
              @click="changePage(page - 1)"
            >
              上一页
            </button>
            <span class="logs-pagination__current" aria-live="polite">第 {{ page }} 页</span>
            <button
              type="button"
              class="logs-pagination__button"
              :disabled="!hasMore || loading"
              @click="changePage(page + 1)"
            >
              下一页
            </button>
          </nav>
        </template>
      </div>
    </section>
  </div>
</template>

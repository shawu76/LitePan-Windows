<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { clearCache, fetchCacheStats } from "@/api/cache";
import { getApiErrorMessage } from "@/api/client";
import { fetchSettings } from "@/api/settings";
import BandMidRows from "@/components/admin/band/BandMidRows.vue";
import SignalBand from "@/components/admin/band/SignalBand.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { toast } from "@/composables/useToast";
import { formatSize } from "@/utils/format";

// 缓存页仪表带：缓存统计与容量上限自行拉取，任务计数由 CacheRetentionPanel 上报。
const props = withDefaults(
  defineProps<{
    taskTotal?: number;
    taskEnabled?: number;
    taskError?: number;
  }>(),
  {
    taskTotal: 0,
    taskEnabled: 0,
    taskError: 0,
  },
);

const emit = defineEmits<{ "open-settings": []; dismiss: [event?: MouseEvent] }>();

const stats = reactive({ totalKeys: 0, totalSize: 0, hitRate: 0 });
const limits = reactive({ maxItems: "", maxMemory: "" });

const refreshing = ref(false);
const clearing = ref(false);

const statRows = computed(() => [
  { key: "total", label: "任务总数", value: props.taskTotal, tone: "brand" as const },
  { key: "enabled", label: "已启用", value: props.taskEnabled, tone: "success" as const },
  { key: "error", label: "异常", value: props.taskError, tone: "warn" as const },
]);

// 第二列状态条：总数 + 各状态分段（与 STRM 页同一套样式）。
const barSegments = computed(() => [
  { key: "ok", label: "已启用", value: Math.max(0, props.taskEnabled - props.taskError), tone: "success" as const },
  { key: "error", label: "异常", value: props.taskError, tone: "warn" as const },
  { key: "rest", label: "未启用", value: Math.max(0, props.taskTotal - props.taskEnabled), tone: "muted" as const },
]);

// 中列只保留 2 行：缓存大小 / 缓存条目；刷新与清理变成行内小图标。
const runtimeRows = computed(() => [
  { key: "size", label: "缓存大小", value: formatSize(stats.totalSize) },
  { key: "keys", label: "缓存条目", value: `${stats.totalKeys.toLocaleString()} 条` },
]);

const limitRows = computed(() => [
  { key: "items", label: "条目上限", value: limits.maxItems },
  { key: "memory", label: "内存上限", value: limits.maxMemory },
]);

async function loadStats() {
  refreshing.value = true;
  try {
    const data = await fetchCacheStats();
    stats.totalKeys = Number(data.total_keys ?? 0);
    stats.totalSize = Number(data.total_size_bytes ?? 0);
    stats.hitRate = Number(data.hit_rate ?? 0);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载缓存统计失败"));
  } finally {
    refreshing.value = false;
  }
}

function formatLimitValue(raw: string, unit: string): string {
  const value = Number(String(raw ?? "").trim());
  if (!Number.isFinite(value) || value <= 0) return "不限";
  return `${value.toLocaleString()} ${unit}`;
}

async function loadLimits() {
  try {
    const payload = await fetchSettings();
    const items = payload.items ?? [];
    const entries = items.find((item) => item.key === "cache_max_items");
    const memory = items.find((item) => item.key === "cache_memory_limit_mb");
    limits.maxItems = entries ? formatLimitValue(entries.value, "条") : "—";
    limits.maxMemory = memory ? formatLimitValue(memory.value, "MB") : "—";
  } catch {
    // 摘要失败不影响其它区域，保留占位符。
    limits.maxItems = "—";
    limits.maxMemory = "—";
  }
}

async function handleClearCache() {
  clearing.value = true;
  try {
    const res = await clearCache();
    toast.success(`已清空 ${res.cleared_count} 条缓存`);
    await loadStats();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "清空缓存失败"));
  } finally {
    clearing.value = false;
  }
}

onMounted(() => {
  void loadStats();
  void loadLimits();
});

defineExpose({ reloadStats: loadStats, reloadSettings: loadLimits });
</script>

<template>
  <SignalBand
    :ring-percent="stats.hitRate"
    ring-label="缓存命中率"
    :stats="statRows"
    bar-label="任务状态"
    :bar-segments="barSegments"
    setup-title="缓存设置"
    :setup-rows="limitRows"
    @open-settings="emit('open-settings')"
    @dismiss="emit('dismiss', $event)"
  >
    <template #middle>
      <BandMidRows :rows="runtimeRows">
        <template #size>
          <button
            type="button"
            class="mid-act"
            :disabled="refreshing"
            title="刷新缓存统计"
            aria-label="刷新缓存统计"
            @click="loadStats"
          >
            <SvgIcon name="hand-sync-alt" :size="14" />
          </button>
        </template>
        <template #keys>
          <button
            type="button"
            class="mid-act mid-act--danger"
            :disabled="clearing"
            title="清空缓存"
            aria-label="清空缓存"
            @click="handleClearCache"
          >
            <SvgIcon name="trash-can" :size="14" />
          </button>
        </template>
      </BandMidRows>
    </template>
  </SignalBand>
</template>

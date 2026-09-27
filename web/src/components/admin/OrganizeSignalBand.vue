<script setup lang="ts">
import { computed, onMounted, reactive } from "vue";
import { fetchMediaOrganizeSettings } from "@/api/mediaOrganize";
import BandMidRows from "@/components/admin/band/BandMidRows.vue";
import SignalBand from "@/components/admin/band/SignalBand.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

// 整理页仪表带：统计与产出由 MediaOrganizePanel 上报，设置摘要自行拉取。
const props = withDefaults(
  defineProps<{
    taskTotal?: number;
    taskRunning?: number;
    taskError?: number;
    organizedCount?: number;
    skippedCount?: number;
    lastRunAt?: string;
    refreshPending?: boolean;
    successRate?: number | null;
  }>(),
  {
    taskTotal: 0,
    taskRunning: 0,
    taskError: 0,
    organizedCount: 0,
    skippedCount: 0,
    lastRunAt: "",
    refreshPending: false,
    successRate: null,
  },
);

const emit = defineEmits<{ refresh: []; "open-settings": []; dismiss: [event?: MouseEvent] }>();

const limits = reactive({ language: "", maxWorks: "" });

const statRows = computed(() => [
  { key: "total", label: "任务总数", value: props.taskTotal, tone: "brand" as const },
  { key: "running", label: "执行中", value: props.taskRunning, tone: "success" as const },
  { key: "error", label: "有失败", value: props.taskError, tone: "warn" as const },
]);

// 第二列状态条：总数 + 各状态分段（与 STRM 页同一套样式）。
const barSegments = computed(() => [
  { key: "ok", label: "执行中", value: Math.max(0, props.taskRunning - props.taskError), tone: "success" as const },
  { key: "error", label: "异常", value: props.taskError, tone: "warn" as const },
  { key: "rest", label: "空闲", value: Math.max(0, props.taskTotal - props.taskRunning), tone: "muted" as const },
]);

// 折叠条右侧显示累计已整理文件数，口径是各任务最近一次运行结果相加，不是同一轮或历史累计；
// 中列只保留整理结果与完成时间两行，刷新是行内小图标。
const outputRows = computed(() => [
  {
    key: "result",
    label: "最近一次整理",
    value: `${formatCount(props.organizedCount)} 个 · 跳过 ${formatCount(props.skippedCount)}`,
  },
  { key: "lastRun", label: "完成时间", value: props.lastRunAt || "尚未执行" },
]);

const settingRows = computed(() => [
  { key: "language", label: "命名语言", value: limits.language },
  { key: "maxWorks", label: "每轮上限", value: limits.maxWorks },
]);

const LANGUAGE_LABELS: Record<string, string> = {
  "zh-CN": "简体中文",
  "zh-TW": "繁体中文",
  "en-US": "English",
};

function formatCount(value: number): string {
  return Number(value || 0).toLocaleString();
}

async function loadLimits() {
  try {
    const data = await fetchMediaOrganizeSettings();
    const language = String(data.tmdb_language || "").trim();
    limits.language = language ? (LANGUAGE_LABELS[language] ?? language) : "—";
    const maxWorks = Number(data.max_works_per_run || 0);
    limits.maxWorks = maxWorks > 0 ? `${formatCount(maxWorks)} 部` : "不限";
  } catch {
    limits.language = "—";
    limits.maxWorks = "—";
  }
}

onMounted(() => {
  void loadLimits();
});

defineExpose({ reloadSettings: loadLimits });
</script>

<template>
  <SignalBand
    :ring-percent="successRate"
    ring-label="整理成功率"
    :stats="statRows"
    bar-label="任务状态"
    :bar-segments="barSegments"
    setup-title="整理设置"
    :setup-rows="settingRows"
    @open-settings="emit('open-settings')"
    @dismiss="emit('dismiss', $event)"
  >
    <template #middle>
      <BandMidRows :rows="outputRows">
        <template #result>
          <button
            type="button"
            class="mid-act"
            :disabled="refreshPending"
            :title="refreshPending ? '刷新中' : '刷新整理任务列表'"
            aria-label="刷新整理任务列表"
            @click="emit('refresh')"
          >
            <SvgIcon name="hand-sync-alt" :size="14" />
          </button>
        </template>
      </BandMidRows>
    </template>
  </SignalBand>
</template>

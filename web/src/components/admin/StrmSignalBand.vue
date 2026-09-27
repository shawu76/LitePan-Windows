<script setup lang="ts">
import { computed } from "vue";
import type { StrmSettings } from "@/api/strm";
import BandPlanRows from "@/components/admin/band/BandPlanRows.vue";
import SignalBand from "@/components/admin/band/SignalBand.vue";

const props = withDefaults(
  defineProps<{
    total: number;
    enabled: number;
    error: number;
    scanning: number;
    /** 最近一轮扫描成功率（0-100）；无已出结果的任务时传 null。 */
    scanSuccessRate?: number | null;
    planItems?: Array<{ time: string; name: string; mode: string }>;
    /** 后端下发的下一次运行时间（ISO）；用于「下次运行」倒计时。 */
    nextRunAt?: string;
    nextRunTask?: string;
    settings?: StrmSettings | null;
    settingsLoading?: boolean;
  }>(),
  {
    scanSuccessRate: null,
    planItems: () => [],
    nextRunAt: "",
    nextRunTask: "",
    settings: null,
    settingsLoading: false,
  },
);

const emit = defineEmits<{ "open-settings": []; dismiss: [event?: MouseEvent] }>();

const statRows = computed(() => [
  { key: "total", label: "任务总数", value: props.total, tone: "brand" as const },
  { key: "enabled", label: "已启用", value: props.enabled, tone: "success" as const },
  { key: "error", label: "异常", value: props.error, tone: "warn" as const },
]);

const planTag = computed(() => (props.scanning > 0 ? `扫描中 ${props.scanning} 个` : ""));

// 状态条分段：启用 / 扫描中 / 异常（按 tone 上色），总数用任务总数。
const barSegments = computed(() => [
  { key: "enabled", label: "已启用", value: Math.max(0, props.enabled - props.error), tone: "success" as const },
  { key: "error", label: "异常", value: props.error, tone: "warn" as const },
  { key: "idle", label: "未启用", value: Math.max(0, props.total - props.enabled), tone: "muted" as const },
]);

// 折叠条右侧显示下一次扫描，收起状态也能判断排期。
function formatInterval(minutes?: number): string {
  const value = Number(minutes ?? 0);
  if (!value) return "—";
  if (value % 60 === 0) return `${value / 60} 小时`;
  if (value < 60) return `${value} 分钟`;
  return `${Math.floor(value / 60)} 小时 ${value % 60} 分钟`;
}

const setupRows = computed(() => {
  const settings = props.settings;
  const placeholder = props.settingsLoading && !settings ? "读取中…" : "—";
  return [
    {
      key: "interval",
      label: "扫描间隔",
      value: settings ? formatInterval(settings.default_scan_interval) : placeholder,
    },
    {
      key: "concurrency",
      label: "并发任务",
      value: settings ? `${Number(settings.task_concurrency || 0)} 个` : placeholder,
    },
  ];
});
</script>

<template>
  <SignalBand
    :ring-percent="scanSuccessRate"
    ring-label="扫描成功率"
    :stats="statRows"
    :bar-segments="barSegments"
    :countdown-at="nextRunAt"
    :countdown-task="nextRunTask"
    setup-title="STRM 设置"
    :setup-rows="setupRows"
    @open-settings="emit('open-settings')"
    @dismiss="emit('dismiss', $event)"
  >
    <template #middle>
      <BandPlanRows
        :items="planItems.slice(0, 3)"
        title="运行计划"
        :tag="planTag"
        empty-text="暂无自动计划：任务均为手动调度或已全部禁用"
      />
    </template>

  </SignalBand>
</template>

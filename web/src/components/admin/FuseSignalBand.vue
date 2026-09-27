<script setup lang="ts">
import { computed } from "vue";
import AppCardActionButton from "@/components/base/AppCardActionButton.vue";
import BandRows from "@/components/admin/band/BandRows.vue";
import SignalBand from "@/components/admin/band/SignalBand.vue";

// 本地挂载页仪表带：圆环＝挂载在线率，中列＝读缓存。
const props = withDefaults(
  defineProps<{
    mountTotal?: number;
    mountedCount?: number;
    errorCount?: number;
    readCacheUsage?: string;
    readCacheBlocks?: number;
    serviceEnabled?: boolean;
    cacheLimitText?: string;
    clearing?: boolean;
  }>(),
  {
    mountTotal: 0,
    mountedCount: 0,
    errorCount: 0,
    readCacheUsage: "—",
    readCacheBlocks: 0,
    serviceEnabled: false,
    cacheLimitText: "—",
    clearing: false,
  },
);

const emit = defineEmits<{ "open-settings": []; "clear-read-cache": []; dismiss: [event?: MouseEvent] }>();

const mountRate = computed<number | null>(() => {
  if (!props.mountTotal) return null;
  return (props.mountedCount / props.mountTotal) * 100;
});

const statRows = computed(() => [
  { key: "total", label: "挂载点", value: props.mountTotal, tone: "brand" as const },
  { key: "mounted", label: "已挂载", value: props.mountedCount, tone: "success" as const },
  { key: "error", label: "异常", value: props.errorCount, tone: "warn" as const },
]);

// 第二列状态条：总数 + 各状态分段（与 STRM 页同一套样式）。
const barSegments = computed(() => [
  { key: "ok", label: "已挂载", value: Math.max(0, props.mountedCount - props.errorCount), tone: "success" as const },
  { key: "error", label: "异常", value: props.errorCount, tone: "warn" as const },
  { key: "rest", label: "未挂载", value: Math.max(0, props.mountTotal - props.mountedCount), tone: "muted" as const },
]);

const cacheRows = computed(() => [
  { key: "usage", label: "读缓存占用", value: props.readCacheUsage },
  { key: "blocks", label: "缓存块数", value: props.readCacheBlocks.toLocaleString() },
]);

const setupRows = computed(() => [
  { key: "service", label: "挂载", value: props.serviceEnabled ? "已启用" : "已关闭" },
  { key: "limit", label: "读缓存上限", value: props.cacheLimitText },
]);
</script>

<template>
  <SignalBand
    :ring-percent="mountRate"
    ring-label="挂载在线率"
    :stats="statRows"
    bar-label="挂载状态"
    :bar-segments="barSegments"
    setup-title="挂载设置"
    :setup-rows="setupRows"
    @open-settings="emit('open-settings')"
    @dismiss="emit('dismiss', $event)"
  >
    <template #middle>
      <BandRows :rows="cacheRows" size="sm">
        <template #actions>
          <AppCardActionButton
            icon-class="trash-can"
            label="清空读缓存"
            variant="danger"
            :disabled="clearing"
            title="清空读缓存"
            @click="emit('clear-read-cache')"
          />
        </template>
      </BandRows>
    </template>

  </SignalBand>
</template>

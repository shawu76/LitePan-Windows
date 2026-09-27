<script setup lang="ts">
/**
 * 仪表带行列表：左侧标签、右侧等宽数字，可选彩色圆点、标题与底部操作行。
 * 三行统计（size="md"）与中列数值行（size="sm"）共用这一个组件。
 */
export type BandRowTone = "brand" | "success" | "warn";

withDefaults(
  defineProps<{
    rows: Array<{ key: string; label: string; value: string | number; tone?: BandRowTone }>;
    /** md：统计用大号数字（19px）；sm：数值行 15px。 */
    size?: "md" | "sm";
    title?: string;
    /** 行多且有操作按钮时用 compact 收紧行距。 */
    density?: "normal" | "compact";
  }>(),
  {
    size: "md",
    title: "",
    density: "normal",
  },
);
</script>

<template>
  <div class="band-rows" :class="`band-rows--${size} band-rows--${density}`">
    <div v-if="title" class="band-rows__title">{{ title }}</div>
    <div class="band-rows__list">
      <div v-for="row in rows" :key="row.key" class="band-rows__row" :class="`band-rows__row--${row.tone ?? 'plain'}`">
        <span class="band-rows__k">
          <i v-if="row.tone" class="band-rows__dot" :class="`band-rows__dot--${row.tone}`" />
          {{ row.label }}
        </span>
        <b class="band-rows__v">{{ row.value }}</b>
      </div>
    </div>
    <div v-if="$slots.actions" class="band-rows__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<style scoped>
.band-rows {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.band-rows--md {
  justify-content: center;
}

.band-rows__title {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  margin-bottom: 8px;
}

.band-rows__row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 0;
  border-bottom: 1px dashed var(--border-soft);
}

.band-rows__row:last-of-type {
  border-bottom: 0;
}

.band-rows--compact .band-rows__row {
  padding: 3px 0;
}

.band-rows__k {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
}

.band-rows__v {
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.01em;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.band-rows--md .band-rows__v {
  font-size: 19px;
  line-height: 1;
}

.band-rows--sm .band-rows__v {
  font-size: 15px;
}

.band-rows__row--success .band-rows__v {
  color: var(--success);
}

.band-rows__row--warn .band-rows__v {
  color: var(--warning);
}

.band-rows__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--brand);
}

.band-rows__dot--success {
  background: var(--success);
}

.band-rows__dot--warn {
  background: var(--warning);
}

.band-rows__actions {
  display: flex;
  gap: 8px;
  padding-top: 10px;
}

.band-rows--compact .band-rows__actions {
  padding-top: 7px;
}

.band-rows__actions :deep(.card-action-btn) {
  flex: 1;
  min-width: 0;
}

@media (max-width: 860px) {
  .band-rows--md .band-rows__list {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
  }

  .band-rows--md .band-rows__row {
    border-bottom: 0;
    padding: 0;
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>

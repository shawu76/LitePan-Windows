<script setup lang="ts">
/**
 * 仪表带中列的紧凑行：左侧标签、右侧等宽数值，行尾可挂一个行内操作（用同名插槽传入）。
 * 用于「缓存任务 / 目录整理」等只需要 2 行信息的页面，避免再占一行放按钮。
 */
withDefaults(
  defineProps<{
    rows: Array<{ key: string; label: string; value: string | number }>;
  }>(),
  { rows: () => [] },
);
</script>

<template>
  <div class="band-mid">
    <div v-for="row in rows" :key="row.key" class="band-mid__row">
      <span class="band-mid__k">{{ row.label }}</span>
      <b class="band-mid__v">{{ row.value }}</b>
      <span v-if="$slots[row.key]" class="band-mid__act">
        <slot :name="row.key" />
      </span>
    </div>
  </div>
</template>

<style scoped>
.band-mid {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 10px;
  height: 100%;
  min-width: 0;
}

.band-mid__row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.band-mid__k {
  font-size: 13px;
  color: var(--text-muted);
  white-space: nowrap;
}

.band-mid__v {
  margin-left: auto;
  font-size: 15px;
  font-weight: 650;
  color: var(--text);
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.band-mid__act {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
</style>

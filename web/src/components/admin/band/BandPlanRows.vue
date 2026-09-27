<script setup lang="ts">
/**
 * 仪表带运行计划列：标题 + 可选状态标签 + 时间/名称/模式三条行。
 */
withDefaults(
  defineProps<{
    items: Array<{ time: string; name: string; mode: string }>;
    title?: string;
    /** 标题右侧的小标签，如「扫描中 1 个」。 */
    tag?: string;
    emptyText?: string;
  }>(),
  {
    title: "运行计划",
    tag: "",
    emptyText: "暂无自动计划",
  },
);
</script>

<template>
  <div class="band-plan">
    <div class="band-plan__head">
      <span class="band-plan__title">{{ title }}</span>
      <span v-if="tag" class="band-plan__tag">{{ tag }}</span>
    </div>
    <ul v-if="items.length" class="band-plan__list">
      <li v-for="item in items" :key="`${item.time}-${item.name}`">
        <span class="band-plan__time">{{ item.time }}</span>
        <span class="band-plan__name" :title="item.name">{{ item.name }}</span>
        <span class="band-plan__mode">{{ item.mode }}</span>
      </li>
    </ul>
    <div v-else class="band-plan__empty">{{ emptyText }}</div>
  </div>
</template>

<style scoped>
.band-plan {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.band-plan__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.band-plan__title {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}

.band-plan__tag {
  font-size: 10.5px;
  font-weight: 600;
  color: var(--brand);
  background: var(--accent-soft);
  border-radius: var(--radius-pill);
  padding: 2px 7px;
  white-space: nowrap;
}

.band-plan__list {
  margin: 2px 0 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
}

.band-plan__list li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
  border-bottom: 1px dashed var(--border-soft);
  font-size: 12.5px;
  min-width: 0;
}

.band-plan__list li:last-child {
  border-bottom: 0;
}

.band-plan__time {
  flex-shrink: 0;
  min-width: 76px;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  color: var(--text);
}

.band-plan__name {
  flex: 1;
  min-width: 0;
  color: var(--text-regular);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.band-plan__mode {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
  background: var(--surface-sunken);
  border-radius: var(--radius-pill);
  padding: 2px 8px;
}

.band-plan__empty {
  padding: 10px 0;
  font-size: 12px;
  color: var(--text-muted);
}

@media (max-width: 620px) {
  .band-plan__time {
    min-width: 62px;
  }
}
</style>

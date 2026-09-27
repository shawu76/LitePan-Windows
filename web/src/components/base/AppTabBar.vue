<script setup lang="ts">
interface Tab {
  key: string;
  label: string;
}

defineProps<{ tabs: Tab[]; modelValue: string }>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
</script>

<template>
  <div class="tabbar">
    <div class="tabbar__tabs">
      <button
        v-for="t in tabs"
        :key="t.key"
        type="button"
        class="tabbar__tab"
        :class="{ 'tabbar__tab--active': t.key === modelValue }"
        @click="emit('update:modelValue', t.key)"
      >
        {{ t.label }}
      </button>
    </div>
    <div class="tabbar__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<style scoped>
.tabbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  background: var(--surface);
  border-radius: var(--radius-md);
  padding: 8px 10px;
  box-shadow: var(--shadow-card);
  margin-bottom: 18px;
}
.tabbar__tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.tabbar__tab {
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  padding: 8px 14px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: var(--transition);
}
.tabbar__tab:hover {
  color: var(--text);
  background: var(--border-soft);
}
.tabbar__tab--active,
.tabbar__tab--active:hover {
  background: var(--brand);
  color: var(--text-on-brand);
}
.tabbar__actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
</style>

<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";
import type { DropdownMenuItem } from "@/types/menu";

withDefaults(
  defineProps<{
    items: DropdownMenuItem[];
    density?: "compact" | "comfortable";
    variant?: "default" | "context";
    minWidth?: number;
  }>(),
  { density: "compact", variant: "default", minWidth: 148 },
);

const emit = defineEmits<{ select: [key: string] }>();

function onItemClick(item: DropdownMenuItem) {
  if (item.type !== "action" || item.disabled) return;
  emit("select", item.key);
}
</script>

<template>
  <ul
    class="menu-panel"
    :class="[
      `menu-panel--${density}`,
      variant === 'context' ? 'menu-panel--context' : '',
    ]"
    :style="{ minWidth: `${minWidth}px` }"
    role="menu"
    @click.stop
  >
    <template v-for="item in items" :key="item.key">
      <li v-if="item.type === 'divider'" class="menu-panel__divider" role="separator" />
      <li v-else-if="item.type === 'hint'" class="menu-panel__hint">{{ item.label }}</li>
      <li v-else class="menu-panel__row" :class="{ 'menu-panel__row--accessory': item.accessoryAction }" role="none">
        <button
          type="button"
          class="menu-panel__item menu-panel__item--main"
          :class="{ 'menu-panel__item--danger': item.danger }"
          role="menuitem"
          :disabled="item.disabled"
          @click="onItemClick(item)"
        >
          <span v-if="item.icon" class="menu-panel__icon">
            <SvgIcon :name="item.icon" :size="18" />
          </span>
          <span>{{ item.label }}</span>
        </button>
        <button
          v-if="item.accessoryAction"
          type="button"
          class="menu-panel__accessory"
          :title="item.accessoryTitle"
          :aria-label="item.accessoryTitle || item.label"
          :disabled="item.disabled"
          @click="emit('select', item.accessoryAction)"
        >
          <SvgIcon :name="item.accessoryIcon || 'hand-list'" :size="17" />
        </button>
      </li>
    </template>
    <slot />
  </ul>
</template>

<style scoped>
.menu-panel__row--accessory {
  display: flex;
  align-items: center;
  border-radius: var(--radius-sm);
}
.menu-panel__row--accessory:hover { background: var(--surface-hover); }
.menu-panel__row--accessory .menu-panel__item--main {
  flex: 1;
  width: auto;
  background: transparent;
}
.menu-panel__accessory {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 30px;
  margin-right: 3px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.menu-panel__accessory:hover {
  background: var(--info-soft);
  color: var(--brand);
}
.menu-panel__accessory:disabled { opacity: .55; cursor: not-allowed; }
</style>

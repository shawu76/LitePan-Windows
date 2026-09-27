<script setup lang="ts">
import { computed } from "vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import type { DropdownMenuItem } from "@/types/menu";

/**
 * 面板收起后的入口：☰ 只在面板被移除时出现，点开是两项——打开面板 / 打开设置。
 * 面板展开时不显示它：面板里的「设置」大字就是设置入口，不重复摆一个。
 */
const props = withDefaults(
  defineProps<{
    /** 「打开XX设置」这一项的文案，由所在页面决定（只显示本页自己的设置）。 */
    settingsLabel?: string;
  }>(),
  { settingsLabel: "打开设置" },
);

const emit = defineEmits<{ "show-panel": []; "open-settings": [] }>();

// AppMenuPanel 只对 type === "action" 的项派发 select，这里需显式声明。
const items = computed<DropdownMenuItem[]>(() => [
  { key: "show-panel", label: "打开信息面板", type: "action" },
  { key: "open-settings", label: props.settingsLabel, type: "action" },
]);

function onSelect(key: string) {
  if (key === "show-panel") emit("show-panel");
  else if (key === "open-settings") emit("open-settings");
}
</script>

<template>
  <AppDropdown :items="items" trigger="click" align="center" :min-width="0" density="compact" @select="onSelect">
    <template #trigger="{ open, toggle }">
      <button
        type="button"
        class="band-menu-btn"
        :class="{ 'band-menu-btn--open': open }"
        :aria-expanded="open"
        aria-label="信息面板"
        title="信息面板"
        @click.stop="toggle"
      >
        <SvgIcon name="hand-menu" :size="16" />
      </button>
    </template>
  </AppDropdown>
</template>

<style scoped>
.band-menu-btn {
  animation: band-menu-pop 0.26s cubic-bezier(0.34, 1.4, 0.64, 1) both;
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
  transition: color 0.16s ease, border-color 0.16s ease, background 0.16s ease;
}
.band-menu-btn:not(:disabled):hover {
  color: var(--brand);
  border-color: color-mix(in srgb, var(--brand) 40%, var(--border));
}
.band-menu-btn--open {
  color: var(--brand);
  border-color: color-mix(in srgb, var(--brand) 45%, var(--border));
  background: var(--accent-soft);
}
@keyframes band-menu-pop {
  from {
    opacity: 0;
    transform: scale(0.4);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

@media (prefers-reduced-motion: reduce) {
  .band-menu-btn {
    animation: none;
  }
}
</style>

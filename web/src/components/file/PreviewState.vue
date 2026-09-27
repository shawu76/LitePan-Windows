<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

// 文件预览的加载、空内容和错误状态。
withDefaults(
  defineProps<{
    icon?: string;
    loading?: boolean;
    title?: string;
    message?: string;
    tone?: "info" | "warn" | "error";
    size?: number;
    inline?: boolean;
  }>(),
  { icon: "", loading: false, title: "", message: "", tone: "info", size: 32, inline: false },
);
</script>

<template>
  <div class="preview-state" :class="[`preview-state--${tone}`, { 'preview-state--inline': inline }]" :role="tone === 'error' ? 'alert' : 'status'">
    <BusySpinner v-if="loading" variant="notch" :size="size" />
    <SvgIcon v-else-if="icon" :name="icon" :size="size" />
    <div class="preview-state__body">
      <strong v-if="title" class="preview-state__title">{{ title }}</strong>
      <span v-if="message" class="preview-state__message">{{ message }}</span>
      <slot />
    </div>
    <div v-if="$slots.actions" class="preview-state__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<style scoped>
.preview-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: var(--preview-overlay-padding, 28px 20px);
  text-align: center;
  color: var(--preview-muted, #9eb0c8);
}
.preview-state--inline {
  flex-direction: row;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  text-align: left;
}
.preview-state--info {
  color: #59a2ff;
}
.preview-state--warn {
  color: #ffb45e;
}
.preview-state--error {
  color: #ffb45e;
}
.preview-state__body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.preview-state__title {
  color: #e7eff9;
  font-size: 14px;
  font-weight: 600;
}
.preview-state--inline .preview-state__title {
  font-size: 13px;
}
.preview-state__message {
  color: var(--preview-muted, #9eb0c8);
  font-size: 12px;
  line-height: 1.7;
}
.preview-state__actions {
  display: flex;
  gap: 8px;
  margin-top: 4px;
}
</style>

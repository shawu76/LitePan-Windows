<script setup lang="ts">
// 弹窗统一关闭按钮。
withDefaults(defineProps<{ label?: string; disabled?: boolean }>(), {
  label: "关闭",
  disabled: false,
});
const emit = defineEmits<{ click: [] }>();
</script>

<template>
  <button
    type="button"
    class="modal-close"
    :aria-label="label"
    :disabled="disabled"
    @click="emit('click')"
  >
    <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path
        d="M4.646 4.646a.5.5 0 0 1 .708 0L8 7.293l2.646-2.647a.5.5 0 0 1 .708.708L8.707 8l2.647 2.646a.5.5 0 0 1-.708.708L8 8.707l-2.646 2.647a.5.5 0 0 1-.708-.708L7.293 8 4.646 5.354a.5.5 0 0 1 0-.708z"
      />
    </svg>
  </button>
</template>

<style scoped>
.modal-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: none;
  border-radius: var(--radius-xs);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: all 0.2s ease;
}
.modal-close:hover:not(:disabled) {
  background: var(--surface-hover);
  color: var(--text);
}
.modal-close:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>

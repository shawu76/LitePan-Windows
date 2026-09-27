<script setup lang="ts">
defineProps<{ confirmTitle?: string; cancelTitle?: string }>();
defineEmits<{ confirm: []; cancel: [] }>();
</script>

<template>
  <button
    type="button"
    class="folder-inline-btn confirm"
    :title="confirmTitle"
    @mousedown.prevent
    @click="$emit('confirm')"
  >
    ✓
  </button>
  <button
    type="button"
    class="folder-inline-btn cancel"
    :title="cancelTitle"
    @mousedown.prevent
    @click="$emit('cancel')"
  >
    ×
  </button>
</template>

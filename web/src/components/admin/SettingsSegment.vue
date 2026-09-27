<script setup lang="ts">
import "@/styles/settings-panel.css";

export interface SegmentOption {
  value: string;
  label: string;
}

const model = defineModel<string>({ required: true });

defineProps<{
  label: string;
  options: SegmentOption[];
}>();
</script>

<template>
  <div class="settings-segment" role="radiogroup" :aria-label="label">
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      class="settings-segment__btn"
      :class="{ 'settings-segment__btn--active': model === opt.value }"
      role="radio"
      :aria-checked="model === opt.value"
      @click="model = opt.value"
    >
      {{ opt.label }}
    </button>
  </div>
</template>

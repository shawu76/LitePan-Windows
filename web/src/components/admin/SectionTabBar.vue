<script setup lang="ts">
import AppTabBar from "@/components/base/AppTabBar.vue";

interface Tab {
  key: string;
  label: string;
}

defineProps<{ tabs: Tab[]; modelValue: string }>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
</script>

<template>
  <AppTabBar :tabs="tabs" :model-value="modelValue" @update:model-value="emit('update:modelValue', $event)">
    <template #actions>
      <slot name="actions" />
    </template>
  </AppTabBar>
</template>

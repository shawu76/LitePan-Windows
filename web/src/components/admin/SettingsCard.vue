<script setup lang="ts">
import "@/styles/settings-panel.css";

withDefaults(
  defineProps<{
    title: string;
    accent?: string;
  }>(),
  { accent: "var(--brand)" },
);
</script>

<template>
  <section class="settings-card" :style="{ '--accent': accent }">
    <div class="settings-card__head">
      <div class="settings-card__head-main">
        <h3 class="settings-card__title">{{ title }}</h3>
        <div v-if="$slots['head-aside']" class="settings-card__head-aside">
          <slot name="head-aside" />
        </div>
      </div>
      <div v-if="$slots['head-actions']" class="settings-card__head-actions">
        <slot name="head-actions" />
      </div>
    </div>
    <div class="settings-card__body">
      <slot />
    </div>
  </section>
</template>

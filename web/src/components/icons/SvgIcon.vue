<script setup lang="ts">
import { computed } from "vue";
import { getSvg } from "./registry";

const props = withDefaults(
  defineProps<{ name: string; size?: number | string; className?: string }>(),
  { size: 18, className: "" },
);

// size 支持像素数值或 1em 等 CSS 长度。
const rootStyle = computed(() => {
  const raw = props.size;
  if (typeof raw === "string" && /[a-z%]/i.test(raw.trim())) {
    const len = raw.trim();
    return { width: len, height: len };
  }
  const n = Number(raw);
  const px = Number.isFinite(n) && n > 0 ? n : 18;
  return { width: `${px}px`, height: `${px}px` };
});

const markup = computed(() => getSvg(props.name));
</script>

<template>
  <span class="lp-svg-icon" :class="className" :style="rootStyle" aria-hidden="true">
    <span class="lp-svg" v-html="markup" />
  </span>
</template>

<style scoped>
.lp-svg-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  vertical-align: middle;
  line-height: 0;
}
.lp-svg :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
  fill: currentColor;
}
</style>

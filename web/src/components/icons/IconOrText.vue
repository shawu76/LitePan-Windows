<script setup lang="ts">
import { computed } from "vue";
import SvgIcon from "./SvgIcon.vue";

// 注册名渲染为 SVG，其他内容按文字显示。
const props = withDefaults(defineProps<{ value?: string; size?: number | string }>(), {
  value: "",
  size: 18,
});

const isIconName = computed(() => /^[a-z][a-z0-9-]*$/.test(props.value));
</script>

<template>
  <SvgIcon v-if="isIconName" :name="value" :size="size" />
  <span v-else class="icon-or-text__text">{{ value }}</span>
</template>

<style scoped>
.icon-or-text__text {
  line-height: 1;
}
</style>

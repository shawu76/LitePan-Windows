<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";
defineProps<{
  name: string;
}>();
</script>

<template>
  <span class="admin-nav-icon"><SvgIcon :name="name" :size="18" /></span>
</template>

<style scoped>
.admin-nav-icon {
  flex: none;
  width: 24px;
  text-align: center;
  font-size: 18px;
  line-height: 1;
}
</style>

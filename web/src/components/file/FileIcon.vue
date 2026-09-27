<script setup lang="ts">
import type { FileItem } from "@/api/types";
import { fileKind } from "@/utils/fileIcon";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const props = withDefaults(defineProps<{ file: FileItem; size?: number }>(), { size: 18 });

// code 类型没有专用徽章，使用通用文件图标。
const KIND_ICON: Record<string, string> = {
  folder: "badge-folder",
  video: "badge-video",
  audio: "badge-audio",
  image: "badge-image",
  archive: "badge-archive",
  pdf: "badge-pdf",
  doc: "badge-doc",
  sheet: "badge-sheet",
  slide: "badge-slide",
  text: "badge-text",
  file: "badge-file",
  code: "hand-file",
};
</script>

<template>
  <SvgIcon :name="KIND_ICON[fileKind(props.file)] ?? 'hand-file'" :size="size" class="file-icon" />
</template>

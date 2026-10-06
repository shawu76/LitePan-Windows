<script setup lang="ts">
import { nextTick, ref } from "vue";
import "@/styles/settings-panel.css";
import SvgIcon from "@/components/icons/SvgIcon.vue";

// title 可留空：留空时不渲染标题条，只显示正文（分享设置里那种"一句话提示"就不带标题）
withDefaults(defineProps<{
  title?: string;
  icon?: string;
  variant?: "default" | "panel";
}>(), { title: "", icon: "question-circle", variant: "default" });

const visible = ref(false);
const anchor = ref<HTMLElement | null>(null);
const popover = ref<HTMLElement | null>(null);
const popoverStyle = ref<Record<string, string>>({});
// right=贴在图标右侧；under=右侧放不下时压到图标下方（此时不画箭头）
const placement = ref<"right" | "under">("right");

const GAP = 12;
const EDGE = 12;

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}

async function show() {
  visible.value = true;
  await nextTick();
  updatePosition();
}

function hide() {
  visible.value = false;
}

/**
 * 提示框是 teleport 到 body 的，不设限就会跑出所在弹窗；这里按「最近的弹窗」为界
 * （没有弹窗时按视口），优先显示在图标右侧，放不下再压到下方并夹进边界内。
 */
function updatePosition() {
  const el = anchor.value;
  const pop = popover.value;
  if (!el) return;
  if (pop) pop.style.maxWidth = ""; // 先按 CSS 自然宽度测量，避免上一次的限宽影响判断
  const rect = el.getBoundingClientRect();
  const host = el.closest(".modal");
  const hostRect = host instanceof HTMLElement ? host.getBoundingClientRect() : null;
  const leftBound = (hostRect ? hostRect.left : 0) + EDGE;
  const rightBound = (hostRect ? hostRect.right : window.innerWidth) - EDGE;
  const topBound = (hostRect ? hostRect.top : 0) + EDGE;
  const bottomBound = (hostRect ? hostRect.bottom : window.innerHeight) - EDGE;
  const width = pop?.offsetWidth ?? 0;
  const height = pop?.offsetHeight ?? 0;

  if (rect.right + GAP + width <= rightBound) {
    placement.value = "right";
    popoverStyle.value = {
      top: `${clamp(rect.top + rect.height / 2 - height / 2, topBound, Math.max(topBound, bottomBound - height))}px`,
      left: `${rect.right + GAP}px`,
      transform: "none",
      maxWidth: "",
    };
    return;
  }

  placement.value = "under";
  const left = clamp(rect.left, leftBound, Math.max(leftBound, rightBound - width));
  const below = rect.bottom + GAP;
  const top = below + height <= bottomBound ? below : Math.max(topBound, rect.top - GAP - height);
  popoverStyle.value = {
    top: `${top}px`,
    left: `${left}px`,
    transform: "none",
    maxWidth: `${Math.max(200, rightBound - leftBound)}px`,
  };
}
</script>

<template>
  <span
    ref="anchor"
    class="settings-help"
    @mouseenter="show"
    @mouseleave="hide"
  >
    <SvgIcon :name="icon" size="1em" class="settings-help__icon" />
    <Teleport to="body">
      <div
        v-show="visible"
        ref="popover"
        class="settings-help__popover settings-help__popover--portal"
        :class="{
          'settings-help__popover--panel': variant === 'panel',
          'settings-help__popover--under': placement === 'under',
        }"
        :style="popoverStyle"
        role="tooltip"
      >
        <div v-if="title" class="settings-help__title">{{ title }}</div>
        <div class="settings-help__body">
          <slot />
        </div>
      </div>
    </Teleport>
  </span>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useId, watch } from "vue";
import BandRows from "@/components/admin/band/BandRows.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { formatRunTimeText } from "@/utils/format";

/**
 * 后台四个任务页共用的仪表带：圆环 + 三行统计 + 中列插槽 + 设置摘要，支持折叠成摘要条。
 * 圆环、统计、设置行都由外壳渲染，页面只提供数据与中列内容。
 */
const props = withDefaults(
  defineProps<{
    /** 圆环百分比（0-100）；null 表示暂无数据。 */
    ringPercent?: number | null;
    ringLabel?: string;
    stats?: Array<{
      key: string;
      label: string;
      value: string | number;
      tone?: "brand" | "success" | "warn";
    }>;
    /** 设置列的标题与摘要行。 */
    setupTitle?: string;
    setupRows?: Array<{ key: string; label: string; value: string | number }>;
    openLabel?: string;
    /** 第二列改用「状态条」时：各状态分段（不传则仍渲染 stats 行）。 */
    /** 状态条标题，如「任务状态」「挂载状态」。 */
    barLabel?: string;
    barSegments?: Array<{
      key: string;
      label: string;
      value: number;
      tone?: "brand" | "success" | "warn" | "muted";
    }>;
    /** 第三列改用「下次运行倒计时」时：目标时间（ISO）与任务名。 */
    countdownAt?: string;
    countdownTask?: string;
  }>(),
  {
    ringPercent: null,
    ringLabel: "",
    stats: () => [],
    setupTitle: "设置",
    setupRows: () => [],
    openLabel: "打开设置",
    barLabel: "任务状态",
    barSegments: () => [],
    countdownAt: "",
    countdownTask: "",
  },
);

const emit = defineEmits<{ "open-settings": []; dismiss: [event?: MouseEvent] }>();

const RADIUS = 42;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;
// 同页多个仪表带需要各自的渐变 id，避免 url(#id) 解析到别的圆环。
const gradientBase = useId();
const gradientId = `band-ring-md-${gradientBase}`;

const hasRingValue = computed(() => props.ringPercent !== null && props.ringPercent !== undefined);

const rate = computed(() => {
  if (!hasRingValue.value) return 0;
  return Math.max(0, Math.min(100, Math.round(Number(props.ringPercent))));
});

const dashOffset = computed(() => CIRCUMFERENCE * (1 - rate.value / 100));

/* ---------- 第二列：状态条（一个图形表达 总数 / 各状态占比） ---------- */
const barSegs = computed(() => props.barSegments ?? []);
const hasBar = computed(() => barSegs.value.length > 0);
const barSum = computed(() => barSegs.value.reduce((sum, seg) => sum + (Number(seg.value) || 0), 0));
function barWidth(value: number): string {
  const total = Math.max(1, barSum.value);
  return `${((Number(value) || 0) / total) * 100}%`;
}

/* ---------- 第三列：下次运行（毫秒级倒计时） ---------- */
const countdownAt = computed(() => props.countdownAt ?? "");
const hasCountdown = computed(() => countdownAt.value !== "" && !Number.isNaN(new Date(countdownAt.value).getTime()));
const countdownSub = computed(() => (hasCountdown.value ? formatRunTimeText(countdownAt.value) : ""));
const cdHms = ref("--:--:--");
const cdMs = ref("00");
const cdSoon = ref(false);

let rafId = 0;
let lastSecond = -1;

function stopCountdown() {
  if (rafId) {
    cancelAnimationFrame(rafId);
    rafId = 0;
  }
}

/** rAF 驱动：每帧只写 2 位毫秒，秒变化时才写时分秒（等宽字体，不触发重排）。 */
function startCountdown() {
  stopCountdown();
  if (!hasCountdown.value) return;
  const target = new Date(countdownAt.value).getTime();
  const pad = (n: number) => String(n).padStart(2, "0");
  const frame = () => {
    const left = Math.max(0, target - Date.now());
    const second = Math.floor(left / 1000);
    if (second !== lastSecond) {
      lastSecond = second;
      cdHms.value = `${pad(Math.floor(left / 3_600_000))}:${pad(Math.floor((left % 3_600_000) / 60_000))}:${pad(second % 60)}`;
      cdSoon.value = left < 10 * 60 * 1000;
    }
    cdMs.value = pad(Math.floor((left % 1000) / 10));
    rafId = requestAnimationFrame(frame);
  };
  rafId = requestAnimationFrame(frame);
}

function handleVisibility() {
  if (document.hidden) stopCountdown();
  else startCountdown();
}

watch(countdownAt, () => startCountdown());
onMounted(() => {
  startCountdown();
  document.addEventListener("visibilitychange", handleVisibility);
});
onUnmounted(() => {
  stopCountdown();
  document.removeEventListener("visibilitychange", handleVisibility);
});

/* ---------- 第四列：设置入口（大字 + 提示 + 箭头） ---------- */
const setupHint = computed(() => props.setupRows.map((row) => row.label).join(" · "));
</script>

<template>
  <section class="signal-band">
      <div class="signal-band__gauge">
        <div class="band-ring">
          <svg viewBox="0 0 96 96" aria-hidden="true">
            <defs>
              <linearGradient :id="gradientId" x1="0" y1="0" x2="1" y2="1">
                <stop offset="0" style="stop-color: var(--brand-start)" />
                <stop offset="1" style="stop-color: var(--brand-end)" />
              </linearGradient>
            </defs>
            <circle class="band-ring__track" cx="48" cy="48" :r="RADIUS" />
            <circle
              class="band-ring__value"
              cx="48"
              cy="48"
              :r="RADIUS"
              :stroke="`url(#${gradientId})`"
              :stroke-dasharray="CIRCUMFERENCE"
              :stroke-dashoffset="dashOffset"
            />
          </svg>
          <div class="band-ring__mid">
            <b>{{ hasRingValue ? `${rate}%` : "—" }}</b>
            <span>{{ hasRingValue ? ringLabel : "暂无记录" }}</span>
          </div>
        </div>
      </div>

      <div class="signal-band__stats">
        <!-- 状态条：一个图形同时表达总数与各状态占比 -->
        <div v-if="hasBar" class="band-cmp">
          <div class="band-cmp__k">{{ barLabel }}</div>
          <div class="band-cmp__bar">
            <i
              v-for="seg in barSegments"
              :key="seg.key"
              :class="`band-cmp__seg band-cmp__seg--${seg.tone ?? 'brand'}`"
              :style="{ width: barWidth(seg.value) }"
            />
          </div>
          <div class="band-cmp__legend">
            <span v-for="seg in barSegments" :key="seg.key">
              <i :class="`band-cmp__dot band-cmp__dot--${seg.tone ?? 'brand'}`" />
              {{ seg.label }} <b>{{ seg.value }}</b>
            </span>
          </div>
        </div>
        <BandRows v-else :rows="stats" size="md" />
      </div>

      <div class="signal-band__middle">
        <!-- 下次运行：毫秒级倒计时 -->
        <div v-if="hasCountdown" class="band-next">
          <div class="band-next__k">下次运行</div>
          <div class="band-next__cd" :class="{ 'band-next__cd--soon': cdSoon }">
            {{ cdHms }}<i>.{{ cdMs }}</i>
          </div>
          <div class="band-next__sub">
            <span v-if="countdownTask" class="band-next__task">{{ countdownTask }}</span>
            <span v-if="countdownTask">·</span>{{ countdownSub }}
          </div>
        </div>
        <slot v-else name="middle" />
      </div>

      <div
        class="signal-band__setup band-setup"
        role="button"
        tabindex="0"
        :title="openLabel"
        @click="emit('open-settings')"
        @keydown.enter.prevent="emit('open-settings')"
      >
        <div class="band-setup__main">
          <span class="band-setup__text">
            <span class="band-setup__title">{{ setupTitle }}</span>
            <span class="band-setup__hint">{{ setupHint }}</span>
          </span>
          <span class="band-setup__arrow" aria-hidden="true">
            <svg viewBox="0 0 24 24" focusable="false"><path d="M5 12h13" /><path d="M13 6.5 18.5 12 13 17.5" /></svg>
          </span>
        </div>
        <button
          type="button"
          class="signal-band__dismiss"
          title="隐藏信息面板"
          aria-label="隐藏信息面板"
          @click.stop="emit('dismiss', $event)"
        >
          <SvgIcon name="hand-close" :size="13" />
        </button>
      </div>
  </section>
</template>

<style scoped>
.signal-band {
  display: grid;
  /* 首尾两列同宽（有底色的“书挡”）：圆环列与设置列各 190px，中间两列按内容比例分。 */
  grid-template-columns: 190px minmax(0, 1.6fr) minmax(0, 1.4fr) 190px;
  align-items: stretch;
  /* 各页仪表带同高，切换页签不跳动；高度与设计稿一致（圆环 96 + 上下 22）。 */
  min-height: 140px;
  border: 1px solid var(--border-soft);
  /* 与相邻卡片（页签栏、表格卡）同圆角，折叠态也一致。 */
  border-radius: var(--radius-md);
  background: var(--surface);
  box-shadow: var(--shadow-card);
  overflow: hidden;
}

.signal-band > * {
  min-width: 0;
  padding: 22px;
  border-right: 1px solid var(--border-soft);
}

.signal-band > *:last-child {
  border-right: 0;
}

.signal-band__gauge {
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(
    180deg,
    color-mix(in srgb, var(--brand) 2%, var(--surface)),
    color-mix(in srgb, var(--brand) 5%, var(--surface))
  );
}

.signal-band__stats {
  display: flex;
  flex-direction: column;
  justify-content: center;
}

.signal-band__middle {
  display: flex;
  flex-direction: column;
}

.signal-band__setup {
  position: relative;
  display: flex;
  flex-direction: column;
  /* 内容在单元格里垂直居中（设置大字 + 描述 + 箭头这一组）。 */
  justify-content: center;
  background: color-mix(in srgb, var(--brand) 5%, var(--surface));
}

.signal-band__dismiss {
  /* 隐藏信息面板按钮，常驻这一列右上角 */
  position: absolute;
  top: 12px;
  right: 12px;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  border: 1px solid transparent;
  border-radius: var(--radius-xs);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: color 0.16s ease, border-color 0.16s ease, background 0.16s ease;
}

.signal-band__dismiss:hover {
  color: var(--brand);
  border-color: var(--border);
  background: var(--surface-hover);
}

.signal-band__dismiss:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--brand) 40%, transparent);
  outline-offset: 1px;
}

/* 圆环 */
.band-ring {
  position: relative;
  width: 96px;
  height: 96px;
}

.band-ring svg,

.band-ring__track,
.band-ring__value {
  fill: none;
  stroke-width: 9;
  stroke-linecap: round;
}

.band-ring__track {
  stroke: var(--border);
}

.band-ring__value {
  transition: stroke-dashoffset 0.4s ease;
}

.band-ring__mid {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  text-align: center;
}

.band-ring__mid b {
  font-size: 23px;
  font-weight: 700;
  line-height: 1;
  letter-spacing: -0.02em;
  color: var(--text);
}

.band-ring__mid span {
  font-size: 10.5px;
  color: var(--text-muted);
  padding: 0 6px;
}

/* 折叠态：外壳退化成单列，摘要条才能占满整行（否则会被塞进第一列 150px）。 */

@media (max-width: 1240px) {
  .signal-band > * {
    padding: 14px 15px;
  }

  .band-ring,
  .band-ring svg {
    width: 92px;
    height: 92px;
  }

}

/* 平板：统计与中列并排、设置整行放下面，「打开设置」与收起按钮始终可达。 */
@media (max-width: 1024px) {
  .signal-band {
    grid-template-columns: 190px repeat(2, minmax(0, 1fr));
  }

  .signal-band__setup {
    grid-column: 1 / -1;
    border-top: 1px solid var(--border-soft);
  }
}

@media (max-width: 860px) {
  .signal-band {
    grid-template-columns: 1fr;
  }

  .signal-band > * {
    border-right: 0;
    border-bottom: 1px solid var(--border-soft);
  }

  .signal-band > *:last-child {
    border-bottom: 0;
  }

}

/* ---------- 第二列：状态条 ---------- */
.band-cmp {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 10px;
  height: 100%;
  min-width: 0;
}
.band-cmp__k { font-size: 11.5px; color: var(--text-muted); letter-spacing: 0.04em; }
.band-cmp__bar { display: flex; height: 10px; flex: none; border-radius: 99px; overflow: hidden; background: var(--border-soft); }
.band-cmp__seg { display: block; height: 100%; }
.band-cmp__seg--brand { background: var(--brand-gradient-h); }
.band-cmp__seg--success { background: linear-gradient(90deg, #12b886, #2fd3a3); }
.band-cmp__seg--warn { background: var(--warning); }
.band-cmp__seg--muted { background: var(--border); }
.band-cmp__legend { display: flex; gap: 16px; flex-wrap: wrap; font-size: 12px; color: var(--text-muted); }
.band-cmp__legend span { display: inline-flex; align-items: center; gap: 6px; }
.band-cmp__legend b { color: var(--text-regular); font-weight: 650; font-variant-numeric: tabular-nums; }
.band-cmp__dot { width: 8px; height: 8px; border-radius: 2.5px; flex: none; }
.band-cmp__dot--brand { background: var(--brand); }
.band-cmp__dot--success { background: #12b886; }
.band-cmp__dot--warn { background: var(--warning); }
.band-cmp__dot--muted { background: #cbd5e1; }

/* ---------- 第三列：下次运行倒计时 ---------- */
.band-next { display: flex; flex-direction: column; justify-content: center; gap: 8px; height: 100%; min-width: 0; }
.band-next__k { font-size: 11px; font-weight: 600; letter-spacing: 0.05em; color: var(--text-muted); text-transform: uppercase; }
.band-next__cd {
  font-family: var(--font-mono, ui-monospace, Menlo, monospace);
  font-variant-numeric: tabular-nums;
  font-size: 34px;
  font-weight: 700;
  letter-spacing: -0.03em;
  line-height: 1.06;
  color: var(--text);
}
.band-next__cd i { font-style: normal; font-size: 0.52em; color: var(--text-muted); }
.band-next__cd--soon { color: var(--warning); }
.band-next__sub { display: flex; align-items: center; gap: 7px; font-size: 12.5px; color: var(--text-muted); }
.band-next__task { color: var(--text-regular); font-weight: 600; }

/* ---------- 第四列：设置入口（大字） ---------- */
.band-setup {
  display: flex;
  align-items: center;
  cursor: pointer;
  transition: background 0.18s ease;
}
.band-setup:hover { background: color-mix(in srgb, var(--brand) 5%, transparent); }
.band-setup:focus-visible { outline: 2px solid color-mix(in srgb, var(--brand) 40%, transparent); outline-offset: -3px; }
.band-setup__main { display: flex; align-items: center; gap: 12px; min-width: 0; }
.band-setup__text { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.band-setup__title {
  font-size: 20px;
  font-weight: 750;
  letter-spacing: -0.025em;
  color: var(--text);
  /* 最后一列只有 190px：标题不换行，超出用省略号兜底。 */
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.band-setup__hint {
  font-size: 12.5px;
  color: var(--text-muted);
  /* 提示同样不换行（如「扫描间隔 · 并发任务」）。 */
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 秀气箭头：细线条、垂直居中，跟在文字后面，不做 hover 位移 */
.band-setup__arrow { flex: none; display: grid; place-items: center; color: var(--brand); }
.band-setup__arrow svg {
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.5;
  stroke-linecap: round;
  stroke-linejoin: round;
}
</style>

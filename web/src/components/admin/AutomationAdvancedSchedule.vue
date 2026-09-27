<script setup lang="ts">
const mode = defineModel<'weekly' | 'monthly'>('mode', { required: true })
const weekdays = defineModel<number[]>('weekdays', { required: true })
const monthDays = defineModel<number[]>('monthDays', { required: true })
const weekOptions = ['一', '二', '三', '四', '五', '六', '日']
const toggleValue = (values: number[], value: number) => values.includes(value)
  ? values.filter(item => item !== value)
  : [...values, value].sort((a, b) => a - b)
</script>

<template>
  <div class="advanced-schedule">
    <div class="advanced-schedule__modes">
      <span class="advanced-schedule__mode-indicator" :class="{ monthly: mode === 'monthly' }" />
      <button type="button" :class="{ active: mode === 'weekly' }" @click="mode = 'weekly'">
        <b>每周</b><small>按星期执行</small>
      </button>
      <button type="button" :class="{ active: mode === 'monthly' }" @click="mode = 'monthly'">
        <b>每月</b><small>按日期执行</small>
      </button>
    </div>
    <div class="advanced-schedule__panel">
      <div class="advanced-schedule__caption">
        <span>{{ mode === 'weekly' ? '选择星期' : '选择日期' }}</span>
        <small>支持多选</small>
      </div>
      <div v-if="mode === 'weekly'" class="advanced-schedule__values advanced-schedule__values--week">
        <button v-for="(label, index) in weekOptions" :key="label" type="button" :class="{ active: weekdays.includes(index + 1) }" @click="weekdays = toggleValue(weekdays, index + 1)">
          <small>周</small><b>{{ label }}</b>
        </button>
      </div>
      <div v-else class="advanced-schedule__calendar">
        <div class="advanced-schedule__values advanced-schedule__values--month">
          <button v-for="day in 31" :key="day" type="button" :class="{ active: monthDays.includes(day) }" @click="monthDays = toggleValue(monthDays, day)">{{ day }}</button>
        </div>
      </div>
      <div class="advanced-schedule__tip">
        <span class="advanced-schedule__tip-dot" />
        {{ mode === 'weekly' ? '将在选中的星期按时执行' : '当月没有所选日期时，本月自动跳过' }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.advanced-schedule { display: grid; gap: 12px; }
.advanced-schedule__modes {
  position: relative;
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  padding: 4px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-lg);
  background: var(--surface-sunken);
  overflow: hidden;
}
.advanced-schedule__mode-indicator {
  position: absolute;
  top: 4px;
  bottom: 4px;
  left: 4px;
  width: calc(50% - 4px);
  border: 1px solid color-mix(in srgb, var(--brand) 28%, var(--border));
  border-radius: calc(var(--radius-lg) - 3px);
  background: var(--surface);
  box-shadow: var(--shadow-soft);
  transition: transform .22s ease;
}
.advanced-schedule__mode-indicator.monthly { transform: translateX(100%); }
.advanced-schedule__modes button {
  position: relative;
  z-index: 1;
  display: grid;
  gap: 1px;
  padding: 8px 10px;
  border: 0;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.advanced-schedule__modes b { color: inherit; font-size: 13px; font-weight: 650; }
.advanced-schedule__modes small { color: inherit; font-size: 11px; opacity: .72; }
.advanced-schedule__modes button.active { color: var(--brand); }
.advanced-schedule__panel {
  padding: 14px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-lg);
  background: linear-gradient(145deg, color-mix(in srgb, var(--brand) 4%, var(--surface)), var(--surface) 48%);
  box-shadow: inset 0 1px 0 color-mix(in srgb, white 38%, transparent);
}
.advanced-schedule__caption { display: flex; align-items: center; justify-content: space-between; margin-bottom: 11px; }
.advanced-schedule__caption span { color: var(--text); font-size: 13px; font-weight: 650; }
.advanced-schedule__caption small { color: var(--text-muted); font-size: 11px; }
.advanced-schedule__values { display: grid; }
.advanced-schedule__values--week { grid-template-columns: repeat(7, 1fr); gap: 7px; }
.advanced-schedule__values--week button {
  display: grid;
  place-content: center;
  min-width: 0;
  height: 52px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface);
  color: var(--text-regular);
  box-shadow: 0 2px 7px color-mix(in srgb, var(--text) 5%, transparent);
  cursor: pointer;
  transition: transform .16s ease, border-color .16s ease, color .16s ease, box-shadow .16s ease;
}
.advanced-schedule__values--week small { color: var(--text-muted); font-size: 10px; }
.advanced-schedule__values--week b { margin-top: -1px; font-size: 16px; font-weight: 650; }
.advanced-schedule__values--week button:hover { transform: translateY(-2px); border-color: color-mix(in srgb, var(--brand) 45%, var(--border)); }
.advanced-schedule__values--week button.active { border-color: var(--brand); color: var(--brand); box-shadow: 0 5px 14px color-mix(in srgb, var(--brand) 16%, transparent), inset 0 0 0 1px var(--brand); }
.advanced-schedule__values--week button.active small { color: var(--brand); }
.advanced-schedule__calendar { padding: 10px 9px 9px; border: 1px solid var(--border-soft); border-radius: var(--radius-md); background: var(--surface); }
.advanced-schedule__values--month { display: grid; grid-template-columns: repeat(7, 1fr); gap: 4px; }
.advanced-schedule__values--month button {
  aspect-ratio: 1;
  min-width: 0;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 50%;
  background: transparent;
  color: var(--text-regular);
  font-size: 11px;
  cursor: pointer;
  transition: background .15s ease, border-color .15s ease, color .15s ease, transform .15s ease;
}
.advanced-schedule__values--month button:hover { border-color: color-mix(in srgb, var(--brand) 45%, transparent); color: var(--brand); transform: scale(1.08); }
.advanced-schedule__values--month button.active { border-color: var(--brand); background: var(--brand); color: white; box-shadow: 0 3px 9px color-mix(in srgb, var(--brand) 28%, transparent); }
.advanced-schedule__tip { display: flex; align-items: center; gap: 7px; margin-top: 11px; color: var(--text-muted); font-size: 11px; }
.advanced-schedule__tip-dot { width: 5px; height: 5px; border-radius: 50%; background: var(--brand); box-shadow: 0 0 0 3px color-mix(in srgb, var(--brand) 12%, transparent); }
@media (max-width: 480px) {
  .advanced-schedule__panel { padding: 12px; }
  .advanced-schedule__values--week { gap: 4px; }
  .advanced-schedule__values--week button { height: 46px; }
  .advanced-schedule__calendar { padding-inline: 5px; }
}
</style>

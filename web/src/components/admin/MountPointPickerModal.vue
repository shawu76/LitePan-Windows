<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { http, getApiErrorMessage } from "@/api/client";
import type { LocalBrowseDir, LocalBrowseResult } from "@/components/common/LocalDirBrowserModal.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppButton from "@/components/base/AppButton.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

interface MountDriveOption {
  label: string;
  occupied: boolean;
  isSystem: boolean;
}

const props = withDefaults(
  defineProps<{
    open: boolean;
    initialValue?: string;
  }>(),
  { initialValue: "" },
);
const emit = defineEmits<{ close: []; select: [value: string] }>();

const loading = ref(false);
const error = ref("");
const occupiedVolumes = ref<LocalBrowseDir[]>([]);
const selected = ref("");

// 真实存在的盘符用于标记"已占用"：A-Z 全部展示，占用项禁用、C: 额外标注系统盘。
const driveOptions = computed<MountDriveOption[]>(() => {
  const occupied = new Set<string>();
  for (const v of occupiedVolumes.value) {
    const m = /^([A-Za-z]):/.exec(v.path || v.name || "");
    if (m) occupied.add(m[1].toUpperCase());
  }
  return Array.from({ length: 26 }, (_, i) => {
    const letter = String.fromCharCode(65 + i);
    return {
      label: `${letter}:`,
      occupied: occupied.has(letter),
      isSystem: letter === "C",
    };
  });
});

function normalizeInitial(v: string): string {
  const m = /^([A-Za-z]):/.exec((v || "").trim());
  return m ? `${m[1].toUpperCase()}:` : "";
}

async function loadVolumes() {
  loading.value = true;
  error.value = "";
  try {
    const data = await http.get<LocalBrowseResult>("/admin/local-fs/browse");
    occupiedVolumes.value = data.volumes ?? [];
  } catch (e) {
    error.value = getApiErrorMessage(e, "加载失败");
    occupiedVolumes.value = [];
  } finally {
    loading.value = false;
  }
}

function toggleOption(opt: MountDriveOption) {
  if (opt.occupied || loading.value) return;
  selected.value = selected.value === opt.label ? "" : opt.label;
}

function confirm() {
  if (!selected.value) return;
  emit("select", selected.value);
}

watch(
  () => props.open,
  (open) => {
    // 关闭时恢复未选中状态；打开时以当前值高亮并重新拉取真实盘符。
    selected.value = "";
    if (!open) return;
    selected.value = normalizeInitial(props.initialValue);
    void loadVolumes();
  },
);
</script>

<template>
  <AppModal :open="open" bare nested @close="emit('close')">
    <div class="mount-point-picker">
      <div class="mount-point-picker__header">
        <h3 class="mount-point-picker__title">选择挂载点</h3>
        <button
          type="button"
          class="mount-point-picker__close"
          aria-label="关闭"
          @click="emit('close')"
        >
          <SvgIcon name="xmark" :size="14" />
        </button>
      </div>

      <div class="mount-point-picker__body">
        <div v-if="loading" class="mount-point-picker__state">加载中…</div>
        <div v-else-if="error" class="mount-point-picker__state error">{{ error }}</div>
        <template v-else>
          <p class="mount-point-picker__hint">已占用的盘符不可选；空闲盘符可直接挂载为该盘符。</p>
          <div class="mount-point-picker__grid">
            <button
              v-for="opt in driveOptions"
              :key="opt.label"
              type="button"
              class="mount-point-drive"
              :class="{
                'mount-point-drive--occupied': opt.occupied,
                'mount-point-drive--selected': !opt.occupied && selected === opt.label,
              }"
              :disabled="opt.occupied"
              @click="toggleOption(opt)"
            >
              <span class="mount-point-drive__letter">{{ opt.label }}</span>
              <span
                class="mount-point-drive__badge"
                :class="{ 'mount-point-drive__badge--system': opt.isSystem }"
              >
                {{ opt.isSystem ? "系统盘" : opt.occupied ? "已占用" : "空闲" }}
              </span>
            </button>
          </div>
        </template>
      </div>

      <div class="mount-point-picker__footer">
        <AppButton variant="cancel" @click="emit('close')">取消</AppButton>
        <AppButton variant="primary" :disabled="!selected" @click="confirm">选择</AppButton>
      </div>
    </div>
  </AppModal>
</template>

<style scoped>
.mount-point-picker {
  display: flex;
  flex-direction: column;
  width: min(90vw, 640px);
  height: min(80vh, 520px);
  min-height: 0;
  overflow: hidden;
  border-radius: var(--radius-md);
  box-sizing: border-box;
}

.mount-point-picker__header {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 20px 24px 0;
}
.mount-point-picker__title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text);
  flex: 1;
  min-width: 0;
}
.mount-point-picker__close {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--text-muted);
  font-size: 20px;
  line-height: 1;
  width: 24px;
  height: 24px;
  padding: 0;
  cursor: pointer;
}
.mount-point-picker__close:hover {
  color: var(--text);
}

.mount-point-picker__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 14px 24px 12px;
  box-sizing: border-box;
  overflow-y: auto;
}

.mount-point-picker__hint {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--text-muted);
}

.mount-point-picker__grid {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 10px;
}

.mount-point-drive {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 14px 8px 10px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}
.mount-point-drive:hover {
  border-color: color-mix(in srgb, var(--brand) 35%, var(--border));
  background: var(--info-soft);
}

.mount-point-drive__letter {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 20px;
  font-weight: 600;
  color: var(--text);
  line-height: 1;
}

.mount-point-drive__badge {
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
}
.mount-point-drive__badge--system {
  color: var(--danger);
}

.mount-point-drive--selected {
  border-color: var(--brand);
  background: var(--info-soft);
}
.mount-point-drive--selected .mount-point-drive__letter {
  color: var(--brand);
}

.mount-point-drive--occupied {
  cursor: not-allowed;
  opacity: 0.55;
  background: var(--surface-sunken);
}
.mount-point-drive--occupied:hover {
  border-color: var(--border);
  background: var(--surface-sunken);
}

.mount-point-picker__state {
  padding: 36px 16px;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
.mount-point-picker__state.error {
  color: var(--danger);
}

.mount-point-picker__footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  padding: 0 24px 24px;
}

@media (max-width: 480px) {
  .mount-point-picker__grid {
    grid-template-columns: repeat(5, 1fr);
  }
}
</style>

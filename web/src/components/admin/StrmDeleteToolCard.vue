<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { containsQuery } from "@/utils/format";
import { getApiErrorMessage } from "@/api/client";
import { strmDeleteApi, type StrmDeleteConfig, type StrmDeleteStatus } from "@/api/cloudTools";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import ProxyWorkspace, { type ProxyField, type ProxyWorkspaceItem } from "@/components/admin/ProxyWorkspace.vue";
import ToolCard from "@/components/admin/ToolCard.vue";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

const defaultThreshold = 100;
const defaultDelayMinutes = 10;

const emptyStatus = (): StrmDeleteStatus => ({
  config: { enabled: false, items: [] },
  tasks: [],
  pending: [],
});

function normalizeConfig(raw: StrmDeleteConfig | null | undefined): StrmDeleteConfig {
  const base = emptyStatus().config;
  if (!raw || typeof raw !== "object") return base;
  const items = Array.isArray(raw.items) ? raw.items : [];
  return {
    enabled: !!raw.enabled,
    items: items
      .map((item) => {
        const delay = Number(item?.delay_minutes);
        return {
          task_id: Number(item?.task_id) || 0,
          threshold: Number(item?.threshold) || defaultThreshold,
          strategy: item?.strategy === "block" ? ("block" as const) : ("confirm" as const),
          delay_minutes: Number.isFinite(delay) && delay >= 0 ? delay : defaultDelayMinutes,
        };
      })
      .filter((item) => item.task_id > 0),
  };
}

function normalizeStatus(raw: StrmDeleteStatus | null | undefined): StrmDeleteStatus {
  const base = emptyStatus();
  if (!raw || typeof raw !== "object") return base;
  return {
    config: normalizeConfig(raw.config),
    tasks: Array.isArray(raw.tasks) ? raw.tasks : [],
    pending: Array.isArray(raw.pending) ? raw.pending : [],
  };
}

const status = ref<StrmDeleteStatus>(emptyStatus());
const workspaceOpen = ref(false);
const taskPickerOpen = ref(false);
const taskPickerIDs = ref<number[]>([]);
const selectedID = ref("");
const loading = ref(false);
const saving = ref(false);
const busyID = ref(0);
const visible = computed(() => containsQuery("STRM 删除监控", props.searchQuery));

// 右侧表单按 ProxyWorkspace 约定用字符串键；name 只用于标题展示
const draft = reactive<Record<string, string>>({
  name: "",
  threshold: String(defaultThreshold),
  delay_minutes: String(defaultDelayMinutes),
  delay_mode: "delay",
  strategy: "confirm",
});

const workspaceFields = computed<ProxyField[]>(() => [
  {
    key: "threshold",
    label: "删除保护阈值（个媒体文件）",
    inputmode: "numeric",
    helpTitle: "删除保护阈值说明",
    helpBody: "同一任务在归并窗口内删除的媒体文件累计超过该数量时，整批不会自动删除，不按单个目录分别放行。",
  },
  {
    key: "strategy",
    label: "超过阈值时",
    type: "segment",
    options: [
      { value: "block", label: "直接拦截" },
      { value: "confirm", label: "等待确认" },
    ],
    helpTitle: "超限处理说明",
    helpBody: "等待确认：整批写入通知中心，管理员确认后再删。<br>直接拦截：整批不删远端文件，只发提醒。",
  },
  {
    key: "delay_mode",
    label: "执行时机",
    type: "segment",
    options: [
      { value: "immediate", label: "立即处理" },
      { value: "delay", label: "延迟处理" },
    ],
    helpTitle: "执行时机说明",
    helpBody: "建议延迟处理，便于在误操作后恢复本地文件。<br>立即处理仍会保留约 2 秒的内部事件归并，避免大量子文件被拆开处理。",
  },
  {
    key: "delay_minutes",
    label: "延迟执行（分钟）",
    inputmode: "numeric",
    hidden: draft.delay_mode !== "delay",
    helpTitle: "延迟执行说明",
    helpBody: "防止误操作。延迟期间本地路径恢复（文件重新出现）会自动取消本次删除。",
  },
]);

const config = computed(() => status.value.config);
const selectedTask = computed(() => status.value.tasks.find((task) => String(task.id) === selectedID.value));
const selectedListened = computed(() => config.value.items.some((item) => String(item.task_id) === selectedID.value));
const pendingForTask = computed(() => status.value.pending.filter((item) => String(item.task_id) === selectedID.value));

const workspaceItems = computed<ProxyWorkspaceItem[]>(() =>
  config.value.items.flatMap((configured) => {
    const task = status.value.tasks.find((item) => item.id === configured.task_id);
    return task ? [{ id: String(task.id), name: task.name, running: true, subtitle: task.local_dir }] : [];
  }),
);

const workspaceSubtitle = computed(() => {
  const task = selectedTask.value;
  if (!task) return "";
  return `${selectedListened.value ? "已监控" : "未监控"} · ${task.local_dir}`;
});

async function load() {
  loading.value = true;
  try {
    status.value = normalizeStatus(await strmDeleteApi.getConfig());
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载 STRM 删除监控设置失败"));
  } finally {
    loading.value = false;
  }
}

function selectTask(id: string) {
  selectedID.value = id;
  const task = status.value.tasks.find((item) => String(item.id) === id);
  const taskConfig = config.value.items.find((item) => String(item.task_id) === id);
  Object.assign(draft, {
    name: task?.name ?? "",
    threshold: String(taskConfig?.threshold ?? defaultThreshold),
    delay_minutes: String(taskConfig?.delay_minutes ?? defaultDelayMinutes),
    delay_mode: taskConfig?.delay_minutes === 0 ? "immediate" : "delay",
    strategy: taskConfig?.strategy ?? "confirm",
  });
}

async function openWorkspace() {
  await load();
  if (!status.value.tasks.length) {
    toast.info("还没有 STRM 任务，请先在任务管理中创建");
    return;
  }
  workspaceOpen.value = true;
  const listened = workspaceItems.value[0];
  if (listened) {
    selectTask(listened.id);
  } else {
    selectedID.value = "";
    openTaskPicker();
  }
}

function toPositiveInt(value: string, fallback: number) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function taskDelayMinutes() {
  return draft.delay_mode === "immediate" ? 0 : toPositiveInt(draft.delay_minutes, defaultDelayMinutes);
}

async function saveTask() {
  const taskID = Number(selectedID.value);
  if (!taskID) return;
  const taskName = selectedTask.value?.name ?? "";
  if (!selectedListened.value) {
    toast.info(`请先在左侧勾选「${taskName}」`);
    return;
  }
  const items = config.value.items.filter((item) => item.task_id !== taskID);
  items.push({
    task_id: taskID,
    threshold: toPositiveInt(draft.threshold, defaultThreshold),
    strategy: draft.strategy === "block" ? "block" : "confirm",
    delay_minutes: taskDelayMinutes(),
  });
  saving.value = true;
  try {
    status.value = normalizeStatus(await strmDeleteApi.saveConfig({ enabled: config.value.enabled, items }));
    selectTask(selectedID.value);
    workspaceOpen.value = false;
    toast.success(`「${taskName}」的设置已保存`);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存设置失败"));
  } finally {
    saving.value = false;
  }
}

function openTaskPicker() {
  taskPickerIDs.value = config.value.items.map((item) => item.task_id);
  taskPickerOpen.value = true;
}

function togglePickerTask(taskID: number) {
  taskPickerIDs.value = taskPickerIDs.value.includes(taskID)
    ? taskPickerIDs.value.filter((id) => id !== taskID)
    : [...taskPickerIDs.value, taskID];
}

async function applyTaskPicker() {
  const existing = new Map(config.value.items.map((item) => [item.task_id, item]));
  const items = taskPickerIDs.value.map((taskID) => existing.get(taskID) ?? ({
    task_id: taskID,
    threshold: defaultThreshold,
    strategy: "confirm" as const,
    delay_minutes: defaultDelayMinutes,
  }));
  saving.value = true;
  try {
    status.value = normalizeStatus(await strmDeleteApi.saveConfig({ enabled: items.length > 0 && config.value.enabled, items }));
    taskPickerOpen.value = false;
    const selectedStillExists = items.some((item) => String(item.task_id) === selectedID.value);
    const nextID = selectedStillExists ? selectedID.value : String(items[0]?.task_id ?? "");
    if (nextID) selectTask(nextID);
    else selectedID.value = "";
    toast.success("监控任务已更新");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "更新监控列表失败"));
  } finally {
    saving.value = false;
  }
}

async function removeTask() {
  const taskID = Number(selectedID.value);
  if (!taskID) return;
  taskPickerIDs.value = config.value.items.filter((item) => item.task_id !== taskID).map((item) => item.task_id);
  await applyTaskPicker();
}

async function toggleEnabled() {
  const current = config.value;
  const enabled = !current.enabled;
  if (enabled && current.items.length === 0) {
    toast.info("请先勾选要监控的 STRM 任务");
    await openWorkspace();
    return;
  }
  saving.value = true;
  try {
    status.value = normalizeStatus(await strmDeleteApi.saveConfig({ enabled, items: [...current.items] }));
    toast.success(enabled ? "STRM 删除监控已启用" : "STRM 删除监控已停用");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "切换失败"));
  } finally {
    saving.value = false;
  }
}

async function actPending(id: number, action: "confirm" | "cancel") {
  busyID.value = id;
  try {
    await (action === "confirm" ? strmDeleteApi.confirm(id) : strmDeleteApi.cancel(id));
    toast.success(action === "confirm" ? "已删除网盘源文件" : "已取消远端删除");
    await load();
  } catch (e) {
    toast.error(getApiErrorMessage(e, action === "confirm" ? "删除失败" : "取消失败"));
  } finally {
    busyID.value = 0;
  }
}

onMounted(load);
</script>

<template>
  <div v-show="visible">
    <ToolCard
      :enabled="status.config.enabled"
      name="STRM 删除监控"
      driver="本地 STRM · 网盘源文件"
      logo-src="/logos/strm-delete.png"
      logo-alt="STRM 删除监控"
      :tags="[{ label: '高风险', variant: 'warn' }]"
      :stat-value="status.config.items.length"
      stat-label="个任务已选择"
    >
      监控所选任务的本地删除操作，联动删除网盘源文件；超过阈值时拦截或等待确认。
      <template #toggle>
        <button
          class="check-toggle"
          type="button"
          :class="{ on: status.config.enabled }"
          :aria-label="status.config.enabled ? '停用 STRM 删除监控' : '启用 STRM 删除监控'"
          :disabled="saving || loading"
          title="启用 / 停用"
          @click="toggleEnabled"
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M3.5 8.5 6.5 11.5 12.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </template>
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="loading" @click="openWorkspace">监控设置</AppButton>
      </template>
    </ToolCard>

    <ProxyWorkspace
      v-model="draft"
      :open="workspaceOpen"
      title="STRM 删除监控 · 设置"
      caption=""
      icon="hand-trash-alt"
      :subtitle="workspaceSubtitle"
      :items="workspaceItems"
      :selected-id="selectedID"
      :fields="workspaceFields"
      :name-editable="false"
      :show-entry="false"
      :show-test="false"
      :saving="saving"
      :save-disabled="!selectedListened"
      save-label="保存"
      deletable
      remove-label="取消监控"
      addable
      add-label="选择监控任务"
      @select="selectTask"
      @add="openTaskPicker"
      @remove="removeTask"
      @save="saveTask"
      @cancel="workspaceOpen = false"
    >
      <template #main-extra>
        <div v-if="pendingForTask.length" class="linkage-pending">
          <div class="linkage-pending__cap">待确认删除（{{ pendingForTask.length }}）</div>
          <div v-for="item in pendingForTask" :key="item.id" class="pending-row">
            <span class="pending-row__tx">
              <b>{{ item.relative_path }}</b>
              <small>本批媒体文件总量已超过保护阈值</small>
            </span>
            <span class="pending-row__ops">
              <AppButton size="sm" variant="secondary" :disabled="busyID === item.id" @click="actPending(item.id, 'cancel')">取消</AppButton>
              <AppButton size="sm" variant="danger" :disabled="busyID === item.id" @click="actPending(item.id, 'confirm')">确认删除</AppButton>
            </span>
          </div>
        </div>
      </template>
    </ProxyWorkspace>

    <AppModal :open="taskPickerOpen" size="md" title="选择监控任务" nested @close="taskPickerOpen = false">
      <div class="task-picker">
        <button
          v-for="task in status.tasks"
          :key="task.id"
          type="button"
          class="task-picker__row"
          :class="{ selected: taskPickerIDs.includes(task.id) }"
          @click="togglePickerTask(task.id)"
        >
          <span class="task-picker__check" aria-hidden="true">
            <svg viewBox="0 0 16 16"><path d="M3.5 8.5 6.5 11.5 12.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
          </span>
          <span class="task-picker__text"><b>{{ task.name }}</b><small>{{ task.local_dir }}</small></span>
        </button>
      </div>
      <template #footer>
        <AppButton variant="secondary" @click="taskPickerOpen = false">取消</AppButton>
        <AppButton variant="primary" :disabled="saving" @click="applyTaskPicker">确认</AppButton>
      </template>
    </AppModal>
  </div>
</template>

<style scoped>
.linkage-pending {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-top: 4px;
  border-top: 1px solid var(--border-soft);
}

.linkage-pending__cap {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-regular);
}

.pending-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 0;
}

.pending-row + .pending-row {
  border-top: 1px solid var(--border-soft);
}

.pending-row__tx {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.pending-row__tx b {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow-wrap: anywhere;
}

.pending-row__tx small {
  font-size: 12px;
  color: var(--text-muted);
}

.pending-row__ops {
  display: flex;
  gap: 7px;
  flex-shrink: 0;
}

.task-picker {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: min(52vh, 480px);
  overflow: auto;
}

.task-picker__row {
  display: flex;
  align-items: center;
  gap: 11px;
  width: 100%;
  padding: 11px 12px;
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  background: var(--surface);
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.task-picker__row:hover,
.task-picker__row.selected {
  border-color: color-mix(in srgb, var(--primary) 45%, var(--border-soft));
  background: color-mix(in srgb, var(--primary) 7%, var(--surface));
}

.task-picker__check {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  border: 1px solid var(--border-strong);
  border-radius: 6px;
  color: transparent;
}

.task-picker__row.selected .task-picker__check {
  border-color: var(--primary);
  color: var(--primary);
}

.task-picker__check svg {
  width: 13px;
  height: 13px;
}

.task-picker__text {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.task-picker__text b {
  font-size: 13px;
}

.task-picker__text small {
  color: var(--text-muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}

@media (max-width: 680px) {
  .pending-row {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>

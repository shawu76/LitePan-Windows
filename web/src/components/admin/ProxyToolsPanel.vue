<script setup lang="ts">
import { containsQuery } from "@/utils/format";
import { computed, onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchEmbyConfigs,
  refreshEmbyLibrary,
  saveEmbyConfigs,
  testEmbyConfig,
  type EmbyConfig,
  type EmbyConfigUpdate,
} from "@/api/emby";
import { fetchFnosConfig, saveFnosConfig, saveFnosManagement, testFnosConfig, testFnosManagement } from "@/api/fnos";
import { confirm } from "@/composables/useConfirm";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import ToolCard from "@/components/admin/ToolCard.vue";
import ProxyWorkspace, { type ProxyField, type ProxyWorkspaceItem } from "@/components/admin/ProxyWorkspace.vue";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

function matches(title: string) {
  return containsQuery(title, props.searchQuery);
}

/* ── Emby 多配置 ── */
const embyConfigs = ref<EmbyConfig[]>([]);
const embyEnabled = ref(false);
const embyOpen = ref(false);
const embySelectedID = ref("");
const embySaving = ref(false);
const embyTesting = ref(false);
const embyRefreshing = ref(false);
function emptyEmbyDraft(name = "") {
  return {
    name,
    emby_url: "",
    api_key: "",
    proxy_port: "",
    direct_strm_clients: "",
  };
}

const embyDraft = reactive<Record<string, string>>(emptyEmbyDraft());

const embyRunning = computed(() => embyConfigs.value.filter((item) => item.running).length);
const selectedEmby = computed(() => embyConfigs.value.find((item) => item.id === embySelectedID.value) || null);
const embyEntryRunning = computed(() => Boolean(selectedEmby.value?.running));
const embyEntryURL = computed(() => (selectedEmby.value ? resolveProxyURL(selectedEmby.value.proxy_url, selectedEmby.value.proxy_port) : ""));

const embyItems = computed<ProxyWorkspaceItem[]>(() =>
  embyConfigs.value.map((item) => ({
    id: item.id,
    name: item.name,
    running: item.running,
    port: String(item.proxy_port || ""),
    lastError: item.last_error,
  })),
);

const embyFields: ProxyField[] = [
  {
    key: "emby_url",
    label: "Emby/Jellyfin 地址",
    placeholder: "http://192.168.1.10:8096",
    helpTitle: "媒体服务器地址说明",
    helpBody: "你的 Emby 或 Jellyfin 服务器地址，例如 <code>http://192.168.1.10:8096</code>。<br>给 LitePan 连接媒体服务器使用，播放器里不要填这个。",
  },
  {
    key: "api_key",
    label: "API Key",
    type: "password",
    placeholder: "Emby/Jellyfin API Key",
    helpTitle: "API Key 说明",
    helpBody: "在 Emby 或 Jellyfin 后台生成 API Key，粘贴到这里，用来连接媒体服务器、扫库和补全媒体信息。",
  },
  {
    key: "proxy_port",
    label: "反代端口",
    inputmode: "numeric",
    placeholder: "例如 18097",
    helpTitle: "反代端口说明",
    helpBody: "反代用的端口，随便选一个没被占用的数字就行。<br>留空则不启动反代。",
  },
  {
    key: "direct_strm_clients",
    label: "STRM 直读客户端",
    placeholder: "默认留空",
    helpTitle: "STRM 直读客户端说明",
    helpBody: "一般无需填写。播放器无法通过反代播放 STRM 时，可让它自己读取 STRM 中的地址。<br>填写客户端关键字，多个用分号隔开，例如 <code>XXXPlay;YYYPlayer</code>；未匹配的播放器仍由 LitePan 代取地址，填错了反而播不了。<br>外网播放时，请确保 STRM 的 URL 基础地址可从外网访问。",
  },
];

function openEmby() {
  embyOpen.value = true;
  if (embyConfigs.value.length) {
    embySelectedID.value = embyConfigs.value[0].id;
    loadEmbyDraft(embyConfigs.value[0]);
  } else {
    embySelectedID.value = "";
    Object.assign(embyDraft, emptyEmbyDraft());
  }
}

function loadEmbyDraft(config: EmbyConfig) {
  Object.assign(embyDraft, {
    name: config.name,
    emby_url: config.emby_url,
    api_key: config.api_key,
    proxy_port: String(config.proxy_port || ""),
    direct_strm_clients: config.direct_strm_clients || "",
  });
}

function selectEmby(id: string) {
  const config = embyConfigs.value.find((item) => item.id === id);
  if (!config) return;
  embySelectedID.value = id;
  loadEmbyDraft(config);
}

function addEmby() {
  embySelectedID.value = "";
  Object.assign(embyDraft, emptyEmbyDraft(embyConfigs.value.length ? `媒体服务 ${embyConfigs.value.length + 1}` : "媒体服务"));
}

function updatesFromConfigs(configs: EmbyConfig[]): EmbyConfigUpdate[] {
  return configs.map((item) => ({
    id: item.id,
    name: item.name,
    emby_url: item.emby_url,
    api_key: item.api_key,
    proxy_port: String(item.proxy_port || ""),
    direct_strm_clients: item.direct_strm_clients || "",
  }));
}

async function persistEmby(items: EmbyConfigUpdate[], message: string, enabled = embyEnabled.value) {
  embySaving.value = true;
  try {
    const state = await saveEmbyConfigs(enabled, items);
    embyEnabled.value = state.enabled;
    embyConfigs.value = state.items || [];
    toast.success(message);
    return true;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存 Emby/Jellyfin 配置失败"));
    return false;
  } finally {
    embySaving.value = false;
  }
}

async function saveEmby() {
  if (!embyDraft.name.trim() || !embyDraft.emby_url.trim() || !embyDraft.api_key.trim() || !embyDraft.proxy_port.trim()) {
    toast.error("请填写配置名称、Emby/Jellyfin 地址、API Key 和反代端口");
    return;
  }
  const items = updatesFromConfigs(embyConfigs.value);
  const next: EmbyConfigUpdate = {
    name: embyDraft.name,
    emby_url: embyDraft.emby_url,
    api_key: embyDraft.api_key,
    proxy_port: embyDraft.proxy_port,
    direct_strm_clients: embyDraft.direct_strm_clients,
  };
  const editing = embyConfigs.value.find((item) => item.id === embySelectedID.value);
  if (editing) {
    next.id = editing.id;
    const index = items.findIndex((item) => item.id === editing.id);
    items[index] = next;
  } else {
    items.push(next);
  }
  if (await persistEmby(items, editing ? "Emby/Jellyfin 配置已保存" : "Emby/Jellyfin 配置已添加")) {
    if (editing) {
      embySelectedID.value = editing.id;
    } else {
      const saved = embyConfigs.value.find((item) => item.name === next.name && item.emby_url === next.emby_url) || embyConfigs.value[0];
      embySelectedID.value = saved?.id || "";
    }
    embyOpen.value = false;
  }
}

async function testEmby() {
  embyTesting.value = true;
  try {
    await testEmbyConfig({
      id: embySelectedID.value || undefined,
      name: embyDraft.name,
      emby_url: embyDraft.emby_url,
      api_key: embyDraft.api_key,
      proxy_port: embyDraft.proxy_port,
      direct_strm_clients: embyDraft.direct_strm_clients,
    });
    toast.success("Emby/Jellyfin 连接成功");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Emby/Jellyfin 连接失败"));
  } finally {
    embyTesting.value = false;
  }
}

async function refreshEmby() {
  if (!selectedEmby.value) return;
  embyRefreshing.value = true;
  try {
    await refreshEmbyLibrary({ config_id: selectedEmby.value.id, mode: "global" });
    toast.success(`已通知「${selectedEmby.value.name}」扫描全部媒体库`);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Emby/Jellyfin 扫库失败"));
  } finally {
    embyRefreshing.value = false;
  }
}

async function deleteEmby() {
  const config = selectedEmby.value;
  if (!config) return;
  const ok = await confirm({
    title: "删除 Emby/Jellyfin 配置？",
    message: `将删除「${config.name}」。引用它的自动联动需要重新选择 Emby/Jellyfin 配置。`,
    confirmText: "确认删除",
    cancelText: "取消",
    danger: true,
  }).catch(() => false);
  if (!ok) return;
  await persistEmby(updatesFromConfigs(embyConfigs.value.filter((item) => item.id !== config.id)), "Emby/Jellyfin 配置已删除");
  if (embyConfigs.value.length) {
    embySelectedID.value = embyConfigs.value[0].id;
    loadEmbyDraft(embyConfigs.value[0]);
  } else {
    embySelectedID.value = "";
    Object.assign(embyDraft, emptyEmbyDraft());
  }
}

async function setEmbyEnabled(enabled: boolean) {
  if (enabled && embyConfigs.value.length === 0) {
    toast.error("请先添加 Emby/Jellyfin 配置");
    embyOpen.value = true;
    return;
  }
  await persistEmby(
    updatesFromConfigs(embyConfigs.value),
    enabled ? "Emby/Jellyfin 反代已启用" : "Emby/Jellyfin 反代已停用",
    enabled,
  );
}

/* ── 飞牛影视（单配置，列表结构） ── */
const fnosEnabled = ref(false);
const fnosRunning = ref(false);
const fnosOpen = ref(false);
const fnosSaving = ref(false);
const fnosTesting = ref(false);
const fnosProxyURL = ref("");
const fnosLastError = ref("");
const fnosManagementOpen = ref(false);
const fnosManagementSaving = ref(false);
const fnosManagementTesting = ref(false);
const fnosManagementReady = ref(false);
const fnosPasswordMask = "********";
const fnosManagement = reactive({ username: "", password: "" });
const fnosForm = reactive<Record<string, string>>({
  name: "飞牛影视",
  fnos_url: "",
  strm_path_maps: "",
  proxy_port: "",
  direct_strm_clients: "Infuse",
});

function applyFnos(config: {
  enabled?: boolean;
  name?: string;
  fnos_url?: string;
  proxy_port?: string;
  strm_path_maps?: string;
  direct_strm_clients?: string;
  proxy_url?: string;
  running?: boolean;
  last_error?: string;
  admin_username?: string;
  management_ready?: boolean;
}) {
  fnosEnabled.value = Boolean(config.enabled);
  fnosRunning.value = Boolean(config.running);
  fnosProxyURL.value = config.proxy_url || "";
  fnosLastError.value = config.last_error || "";
  fnosManagementReady.value = Boolean(config.management_ready);
  fnosManagement.username = config.admin_username || "";
  fnosManagement.password = fnosManagementReady.value ? fnosPasswordMask : "";
  Object.assign(fnosForm, {
    name: config.name || "飞牛影视",
    fnos_url: config.fnos_url || "",
    strm_path_maps: config.strm_path_maps || "",
    proxy_port: config.proxy_port || "",
    direct_strm_clients: config.direct_strm_clients || "",
  });
}

async function testManagement() {
  fnosManagementTesting.value = true;
  try {
    await testFnosManagement({
      username: fnosManagement.username,
      password: fnosManagement.password === fnosPasswordMask ? "" : fnosManagement.password,
    });
    toast.success("飞牛影视管理权限验证成功");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "飞牛影视管理员登录失败"));
  } finally {
    fnosManagementTesting.value = false;
  }
}

async function saveManagement() {
  fnosManagementSaving.value = true;
  try {
    const saved = await saveFnosManagement({
      username: fnosManagement.username,
      password: fnosManagement.password === fnosPasswordMask ? "" : fnosManagement.password,
    });
    applyFnos(saved);
    fnosManagementOpen.value = false;
    toast.success(saved.management_ready ? "飞牛影视管理权限已保存" : "飞牛影视管理权限已清除");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存飞牛影视管理权限失败"));
  } finally {
    fnosManagementSaving.value = false;
  }
}

const fnosItems = computed<ProxyWorkspaceItem[]>(() => [
  {
    id: "fnos",
    name: fnosForm.name || "飞牛影视",
    running: fnosRunning.value,
    port: fnosForm.proxy_port || "",
    lastError: fnosLastError.value || undefined,
  },
]);

const fnosEntryURL = computed(() => resolveProxyURL(fnosProxyURL.value, fnosForm.proxy_port));

const fnosFields: ProxyField[] = [
  {
    key: "fnos_url",
    label: "飞牛影视地址",
    placeholder: "http://192.168.1.50:8005",
    helpTitle: "飞牛影视地址说明",
    helpBody: "你的飞牛影视地址，端口一般是 8005（不是 NAS 管理页的 5666）。<br>给 LitePan 连飞牛用的，播放器里不要填这个。",
  },
  {
    key: "strm_path_maps",
    label: "飞牛 STRM 目录",
    placeholder: "/vol1/1000/Strm/LitePanGO",
    helpTitle: "飞牛 STRM 目录说明",
    helpBody: "把 Docker 里映射到 <code>/app/strm</code> 的左边路径填到这里。<br>例：<code>/vol1/1000/Strm/LitePanGO:/app/strm</code> → 填 <code>/vol1/1000/Strm/LitePanGO</code>。<br>两边路径相同则可留空。",
  },
  {
    key: "proxy_port",
    label: "反代端口",
    inputmode: "numeric",
    placeholder: "例如 18997",
    helpTitle: "反代端口说明",
    helpBody: "反代用的端口，随便选一个没被占用的数字就行，别和 Emby/Jellyfin 反代用同一个。<br>留空则不启动反代。",
  },
  {
    key: "direct_strm_clients",
    label: "STRM 直读客户端",
    placeholder: "Infuse;XXXPlay",
    helpTitle: "STRM 直读客户端说明",
    helpBody: "目前已知只有 Infuse 不支持由 LitePan 代取下载地址，需要自己读取 STRM 中的地址。<br>填写这些播放器的客户端关键字，多个用分号隔开，例如 <code>Infuse;XXXPlay</code>；未匹配的播放器仍由 LitePan 代取地址，填错了反而播不了。<br>外网播放时，请确保 STRM 的 URL 基础地址可从外网访问。",
  },
];

async function saveFnos() {
  fnosSaving.value = true;
  try {
    const saved = await saveFnosConfig({
      enabled: fnosEnabled.value,
      name: fnosForm.name,
      fnos_url: fnosForm.fnos_url,
      proxy_port: fnosForm.proxy_port,
      strm_path_maps: fnosForm.strm_path_maps,
      direct_strm_clients: fnosForm.direct_strm_clients,
    });
    applyFnos(saved);
    toast.success("飞牛影视反代配置已保存");
    fnosOpen.value = false;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存飞牛影视配置失败"));
  } finally {
    fnosSaving.value = false;
  }
}

async function testFnos() {
  fnosTesting.value = true;
  try {
    await testFnosConfig({
      enabled: fnosEnabled.value,
      name: fnosForm.name,
      fnos_url: fnosForm.fnos_url,
      proxy_port: fnosForm.proxy_port,
      strm_path_maps: fnosForm.strm_path_maps,
      direct_strm_clients: fnosForm.direct_strm_clients,
    });
    toast.success("飞牛影视连接成功");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "飞牛影视连接失败"));
  } finally {
    fnosTesting.value = false;
  }
}

async function setFnosEnabled(enabled: boolean) {
  if (fnosSaving.value) return;
  fnosSaving.value = true;
  try {
    const saved = await saveFnosConfig({
      enabled,
      name: fnosForm.name,
      fnos_url: fnosForm.fnos_url,
      proxy_port: fnosForm.proxy_port,
      strm_path_maps: fnosForm.strm_path_maps,
      direct_strm_clients: fnosForm.direct_strm_clients,
    });
    applyFnos(saved);
    toast.success(enabled ? "飞牛影视反代已启用" : "飞牛影视反代已停用");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存飞牛影视配置失败"));
  } finally {
    fnosSaving.value = false;
  }
}

/* ── 通用 ── */
function resolveProxyURL(proxyURL: string, port: string) {
  const value = port.trim();
  if (!value) return proxyURL;
  try {
    const url = new URL(proxyURL || `http://127.0.0.1:${value}`);
    if (["127.0.0.1", "localhost"].includes(url.hostname) && !["127.0.0.1", "localhost"].includes(window.location.hostname)) {
      return `${window.location.protocol}//${window.location.hostname}:${value}`;
    }
  } catch {}
  return proxyURL;
}

async function copyEndpoint(proxyURL: string, port: string, running: boolean) {
  const endpoint = resolveProxyURL(proxyURL, port);
  if (!running || !endpoint) {
    toast.error("反代尚未运行");
    return;
  }
  await copyTextToClipboard(endpoint, { successMessage: "已复制反代地址", errorMessage: "复制失败" });
}

onMounted(async () => {
  try {
    const [emby, fnos] = await Promise.all([fetchEmbyConfigs(), fetchFnosConfig()]);
    embyEnabled.value = Boolean(emby.enabled);
    embyConfigs.value = emby.items || [];
    applyFnos(fnos);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载反代配置失败"));
  }
});
</script>

<template>
  <div class="proxy-enhancement-cards">
    <ToolCard
      v-show="matches('Emby/Jellyfin 反代')"
      :enabled="embyEnabled"
      name="Emby/Jellyfin 反代"
      driver="STRM 直连 · 多媒体服务"
      logo-src="/logos/emby.png"
      logo-fit="contain"
      logo-alt="Emby/Jellyfin"
      :stat-value="embyConfigs.length"
      :stat-label="`个配置 · ${embyRunning} 个运行`"
    >
      <template #toggle>
        <button class="check-toggle" type="button" :class="{ on: embyEnabled }" :disabled="embySaving" title="启用 / 停用" @click="setEmbyEnabled(!embyEnabled)"><svg viewBox="0 0 16 16"><path d="M3.5 8.5 6.5 11.5 12.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg></button>
      </template>
      将 Emby/Jellyfin 的 STRM 播放请求转换为网盘 302 直链，避免媒体流量经过媒体服务器中转。
      <template #actions>
        <AppButton size="sm" variant="secondary" @click="openEmby">配置反代</AppButton>
      </template>
    </ToolCard>

    <ToolCard
      v-show="matches('飞牛影视反代')"
      :enabled="fnosEnabled"
      name="飞牛影视反代"
      driver="STRM 直连 · 飞牛路径转换"
      logo-src="/logos/fnmovie.png"
      logo-fit="contain"
      logo-alt="飞牛影视"
      :stat-value="fnosRunning ? '运行中' : fnosEnabled ? '待监听' : '未启用'"
    >
      <template #toggle>
        <button class="check-toggle" type="button" :class="{ on: fnosEnabled }" :disabled="fnosSaving" title="启用 / 停用" @click="setFnosEnabled(!fnosEnabled)"><svg viewBox="0 0 16 16"><path d="M3.5 8.5 6.5 11.5 12.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg></button>
      </template>
      解决Vidhub、Senplayer、爆米花等第三方播放器添加飞牛影视源后，无法播放STRM的问题。
      <template #actions>
        <AppButton size="sm" variant="secondary" @click="fnosOpen = true">配置反代</AppButton>
      </template>
    </ToolCard>

    <ProxyWorkspace
      v-model="embyDraft"
      :open="embyOpen"
      title="Emby/Jellyfin 反代配置"
      caption="EMBY 配置"
      icon="hand-play"
      subtitle="STRM 直连 · 多媒体服务"
      :items="embyItems"
      :selected-id="embySelectedID"
      :fields="embyFields"
      :entry-url="embyEntryURL"
      :entry-running="embyEntryRunning"
      entry-help-title="反代入口说明"
      entry-help-body="在播放器里添加 Emby 或 Jellyfin 服务器时，填这个地址。<br>注意不是上面的媒体服务器地址，别填混了。"
      :show-refresh="true"
      :refreshing="embyRefreshing"
      :testing="embyTesting"
      :saving="embySaving"
      :deletable="Boolean(selectedEmby)"
      :addable="true"
      @select="selectEmby"
      @add="addEmby"
      @remove="deleteEmby"
      @test="testEmby"
      @refresh="refreshEmby"
      @copy="selectedEmby && copyEndpoint(selectedEmby.proxy_url, String(selectedEmby.proxy_port || ''), selectedEmby.running)"
      @save="saveEmby"
      @cancel="embyOpen = false"
    />

    <ProxyWorkspace
      v-model="fnosForm"
      :open="fnosOpen"
      title="飞牛影视反代配置"
      caption="飞牛影视配置"
      icon="hand-monitor"
      subtitle="STRM 直连 · 飞牛路径转换"
      :items="fnosItems"
      selected-id="fnos"
      :fields="fnosFields"
      name-placeholder="例如：飞牛影视"
      :entry-url="fnosEntryURL"
      :entry-running="fnosRunning"
      entry-help-title="反代入口说明"
      entry-help-body="在播放器里添加飞牛服务器时，填这个地址。<br>注意不是上面的飞牛影视地址，别填混了。"
      :testing="fnosTesting"
      :saving="fnosSaving"
      :deletable="false"
      :addable="false"
      @test="testFnos"
      @copy="copyEndpoint(fnosProxyURL, fnosForm.proxy_port, fnosRunning)"
      @save="saveFnos"
      @cancel="fnosOpen = false"
    >
      <template #footer-actions>
        <AppButton variant="secondary" @click="fnosManagementOpen = true">
          {{ fnosManagementReady ? "管理权限已配置" : "配置管理权限" }}
        </AppButton>
      </template>
    </ProxyWorkspace>

    <AppModal :open="fnosManagementOpen" size="sm" title="飞牛影视管理权限" @close="fnosManagementOpen = false">
      <div class="management-form">
        <p>仅用于自动联动中的媒体库扫描和元数据刷新；飞牛反代播放本身不需要管理员账号。不使用这些动作可以留空。</p>
        <label>管理员账号</label>
        <input v-model.trim="fnosManagement.username" class="form-input" autocomplete="username" placeholder="飞牛影视管理员账号">
        <label>管理员密码</label>
        <input v-model="fnosManagement.password" class="form-input" type="password" autocomplete="new-password" placeholder="飞牛影视管理员密码" @focus="fnosManagement.password === fnosPasswordMask && (fnosManagement.password = '')">
      </div>
      <template #footer>
        <AppButton variant="secondary" :disabled="fnosManagementTesting" @click="testManagement">{{ fnosManagementTesting ? "测试中…" : "测试登录" }}</AppButton>
        <AppButton variant="primary" :disabled="fnosManagementSaving" @click="saveManagement">{{ fnosManagementSaving ? "保存中…" : "保存" }}</AppButton>
      </template>
    </AppModal>
  </div>
</template>

<style scoped>
.proxy-enhancement-cards {
  display: contents;
}
.management-form { display: grid; gap: 10px; }
.management-form p { margin: 0 0 4px; color: var(--text-muted); line-height: 1.65; font-size: 13px; }
.management-form label { font-size: 13px; font-weight: 650; color: var(--text); }
.management-form .form-input {
  width: 100%;
  box-sizing: border-box;
  height: 40px;
  padding: 0 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: var(--text);
  outline: none;
}
.management-form .form-input:focus { border-color: var(--brand); box-shadow: 0 0 0 3px var(--brand-soft); }
</style>

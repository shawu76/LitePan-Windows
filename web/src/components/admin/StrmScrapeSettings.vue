<script setup lang="ts">
import { onMounted, ref } from "vue";
import { testMediaOrganizeTmdb } from "@/api/mediaOrganize";
import {
  fetchStrmScrapeSettings,
  saveStrmScrapeSettings,
  type StrmScrapeSettings,
  type StrmScrapeWriteMode,
} from "@/api/strmScrape";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import TmdbHostsHelpTip from "@/components/admin/TmdbHostsHelpTip.vue";
import { useSettingsForm, bindSettingsPanelExpose, useSettingsSave } from "@/composables/useSettingsForm";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { runTmdbTest } from "@/composables/useTmdbTest";

const SCRAPE_SETTINGS_ACCENT = "#38bdf8";

const tmdbLanguageOptions = [
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁体中文" },
  { value: "en-US", label: "English" },
];

const writeModeOptions = [
  { value: "missing_only", label: "仅补缺（推荐）" },
  { value: "overwrite", label: "覆盖已有 nfo / 海报" },
];

const { loading, loaded, runLoad } = useSettingsLoad();
const { saving, runSave } = useSettingsSave();
const tmdbTesting = ref(false);

const {
  settings,
  isDirty: settingsChanged,
  isFieldChanged,
  snapshotBaseline,
  applyBaseline,
  revert: revertToBaseline,
} = useSettingsForm<StrmScrapeSettings>({
  write_mode: "missing_only",
  episode_info: true,
  fanart: false,
  actors: false,
  clearlogo: false,
  tmdb_api_key: "",
  tmdb_language: "zh-CN",
  tmdb_api_host: "https://api.themoviedb.org",
  tmdb_image_host: "https://image.tmdb.org",
  tmdb_request_interval_ms: 300,
  proxy_enabled: false,
  proxy_url: "",
  proxy_username: "",
  proxy_password: "",
});

async function loadSettings(opts?: { silent?: boolean }) {
  await runLoad(
    async () => {
      const data = await fetchStrmScrapeSettings();
      applyBaseline({
        write_mode: (data.write_mode as StrmScrapeWriteMode) || "missing_only",
        episode_info: Boolean(data.episode_info),
        fanart: Boolean(data.fanart),
        actors: Boolean(data.actors),
        clearlogo: Boolean(data.clearlogo),
        tmdb_api_key: data.tmdb_api_key || "",
        tmdb_language: data.tmdb_language || "zh-CN",
        tmdb_api_host: data.tmdb_api_host || "https://api.themoviedb.org",
        tmdb_image_host: data.tmdb_image_host || "https://image.tmdb.org",
        tmdb_request_interval_ms: Number(data.tmdb_request_interval_ms) || 300,
        proxy_enabled: Boolean(data.proxy_enabled),
        proxy_url: data.proxy_url || "",
        proxy_username: data.proxy_username || "",
        proxy_password: "",
      });
    },
    "加载 STRM 刮削设置失败",
    { silent: opts?.silent },
  );
}

async function saveSettings() {
  await runSave(async () => {
    const data = await saveStrmScrapeSettings({
      ...settings,
      tmdb_request_interval_ms: Number(settings.tmdb_request_interval_ms),
    });
    applyBaseline({
      ...settings,
      write_mode: (data.write_mode as StrmScrapeWriteMode) || settings.write_mode,
      tmdb_api_key: data.tmdb_api_key || settings.tmdb_api_key,
      proxy_password: "",
    });
    snapshotBaseline();
  }, { successMessage: "刮削设置已保存" });
}

async function testTmdb() {
  if (tmdbTesting.value) return;
  tmdbTesting.value = true;
  try {
    await runTmdbTest(() => testMediaOrganizeTmdb({
      tmdb_api_key: settings.tmdb_api_key,
      tmdb_language: settings.tmdb_language,
      tmdb_api_host: settings.tmdb_api_host,
      tmdb_image_host: settings.tmdb_image_host,
      proxy_enabled: settings.proxy_enabled,
      proxy_url: settings.proxy_url,
      proxy_username: settings.proxy_username,
      proxy_password: settings.proxy_password,
      tmdb_request_interval_ms: Number(settings.tmdb_request_interval_ms),
    }));
  } finally {
    tmdbTesting.value = false;
  }
}

onMounted(() => {
  void loadSettings();
});

defineExpose(
  bindSettingsPanelExpose({
    isDirty: settingsChanged,
    saving,
    save: saveSettings,
    reload: () => loadSettings({ silent: loaded.value }),
    revert: revertToBaseline,
  }),
);
</script>

<template>
  <div class="settings-panel" :style="{ '--settings-accent': SCRAPE_SETTINGS_ACCENT }">
    <AppStateBlock v-if="loading && !loaded" message="加载中…" loading min-height="120px" />
    <template v-else>
      <SettingsCard title="刮削策略" :accent="SCRAPE_SETTINGS_ACCENT">
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('write_mode')">
          <template #info>
            <div class="settings-row__label">写入策略</div>
          </template>
          <template #control>
            <AppSelect v-model="settings.write_mode" :options="writeModeOptions" />
          </template>
        </SettingsRow>
        <SettingsRow
          :show-changed-badge="true"
          :changed="isFieldChanged('episode_info') || isFieldChanged('fanart') || isFieldChanged('actors') || isFieldChanged('clearlogo')"
        >
          <template #info>
            <div class="settings-row__label">额外刮削</div>
          </template>
          <template #control>
            <div class="scrape-extra-options">
              <label><input v-model="settings.episode_info" type="checkbox" />分集信息</label>
              <label><input v-model="settings.fanart" type="checkbox" />详情页背景图</label>
              <label><input v-model="settings.actors" type="checkbox" />演员信息</label>
              <label><input v-model="settings.clearlogo" type="checkbox" />影片 Logo</label>
            </div>
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="TMDB 设置" :accent="SCRAPE_SETTINGS_ACCENT">
        <template #head-aside>
          <p class="scrape-settings-tip">与「目录整理」共用同一套 TMDB / 代理配置，修改后两边同步生效。</p>
        </template>
        <template #head-actions>
          <AppButton type="button" variant="secondary" size="sm" :disabled="tmdbTesting" @click="testTmdb">
            {{ tmdbTesting ? "测试中…" : "测试连通性" }}
          </AppButton>
        </template>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_api_key')">
          <template #info>
            <div class="settings-row__label">TMDB API Key</div>
          </template>
          <template #control>
            <AppInput
              v-model="settings.tmdb_api_key"
              type="password"
              placeholder="请填写 TMDB API Key（必填）"
              :ignore-autofill="true"
            />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_language')">
          <template #info>
            <div class="settings-row__label">搜索语言</div>
          </template>
          <template #control>
            <AppSelect v-model="settings.tmdb_language" :options="tmdbLanguageOptions" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_api_host')">
          <template #info>
            <div class="settings-row__label">
              <span>TMDB API 主域名</span>
              <SettingsHelpTooltip title="TMDB API 主域名说明">
                <p>自建反代时填写主域名，程序自动补 /3；默认使用官方地址。</p>
                <p>国内网络可尝试填写 https://api.tmdb.org（与官方域名解析到不同节点，部分地区可直连，效果因网络环境而异）。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_api_host" placeholder="https://api.themoviedb.org" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_image_host')">
          <template #info>
            <div class="settings-row__label">
              <span>TMDB 图片主域名</span>
              <SettingsHelpTooltip title="TMDB 图片主域名说明">
                <p>自建反代时填写主域名，程序自动补 /t/p；默认使用官方地址。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_image_host" placeholder="https://image.tmdb.org" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_request_interval_ms')">
          <template #info>
            <div class="settings-row__label">
              <span>请求间隔（毫秒）</span>
              <SettingsHelpTooltip title="请求间隔说明">
                <p>连续请求 TMDB 的最小间隔，过小可能触发限流。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_request_interval_ms" type="number" min="200" max="5000" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="代理设置" :accent="SCRAPE_SETTINGS_ACCENT">
        <template #head-aside>
          <TmdbHostsHelpTip />
        </template>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_enabled')">
          <template #info>
            <div class="settings-row__label">启用代理</div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.proxy_enabled" label="启用代理访问 TMDB" />
          </template>
        </SettingsRow>
        <template v-if="settings.proxy_enabled">
          <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_url')">
            <template #info>
              <div class="settings-row__label">代理地址</div>
            </template>
            <template #control>
              <AppInput v-model="settings.proxy_url" placeholder="http://127.0.0.1:1080 或 socks5://127.0.0.1:1080" />
            </template>
          </SettingsRow>
          <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_username')">
            <template #info>
              <div class="settings-row__label">用户名</div>
            </template>
            <template #control>
              <AppInput v-model="settings.proxy_username" placeholder="可选" autocomplete="off" />
            </template>
          </SettingsRow>
          <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_password')">
            <template #info>
              <div class="settings-row__label">密码</div>
            </template>
            <template #control>
              <AppInput v-model="settings.proxy_password" type="password" placeholder="不修改请留空" autocomplete="off" />
            </template>
          </SettingsRow>
        </template>
      </SettingsCard>
    </template>
  </div>
</template>

<style scoped>
.scrape-settings-tip {
  margin: 0;
  padding: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.45;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.settings-row__label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}
.scrape-extra-options {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
}
.scrape-extra-options label {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--text);
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;
}
.scrape-extra-options input {
  appearance: none;
  width: 15px;
  height: 15px;
  margin: 0;
  border: 1.5px solid var(--border-strong, var(--border));
  border-radius: 4px;
  background: transparent;
  display: grid;
  place-content: center;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.scrape-extra-options input::before {
  content: "";
  width: 7px;
  height: 4px;
  border-left: 1.8px solid var(--settings-accent);
  border-bottom: 1.8px solid var(--settings-accent);
  transform: rotate(-45deg) scale(0);
  transform-origin: center;
  transition: transform 0.12s ease;
}
.scrape-extra-options input:checked {
  border-color: var(--settings-accent);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--settings-accent) 12%, transparent);
}
.scrape-extra-options input:checked::before {
  transform: rotate(-45deg) scale(1);
}
.scrape-extra-options input:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--settings-accent) 28%, transparent);
  outline-offset: 2px;
}
</style>

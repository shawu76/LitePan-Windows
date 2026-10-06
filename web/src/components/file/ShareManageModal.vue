<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppModal from "@/components/base/AppModal.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppIconButton from "@/components/base/AppIconButton.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { cloudShareApi } from "@/api/cloudShare";
import { getApiErrorMessage } from "@/api/client";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import { confirm } from "@/composables/useConfirm";
import { formatSize } from "@/utils/format";
import { shareURLWithPassword, shareTrafficSwitch } from "@/utils/cloudShare";
import type { CloudShareCapabilities, CloudShareItem, CloudShareKind } from "@/types/cloud-share";

const props = defineProps<{
  open: boolean;
  accountId: number | null;
  accountName: string;
  capability: CloudShareCapabilities | null;
}>();
const emit = defineEmits<{ close: [] }>();

const kind = ref<CloudShareKind>("free");
const items = ref<CloudShareItem[]>([]);
const cursor = ref("");
const loading = ref(false);
const loadingMore = ref(false);
const editing = ref<CloudShareItem | null>(null);
const guestTraffic = ref(false);
const overTraffic = ref(false);
const saving = ref(false);
const cancellingId = ref("");

const hasMore = computed(() => cursor.value !== "" && cursor.value !== "-1");

watch(() => props.open, (open) => {
  if (!open) return;
  kind.value = props.capability?.supports_free ? "free" : "paid";
  editing.value = null;
  void load(true);
});
watch(kind, () => {
  if (props.open) {
    editing.value = null;
    void load(true);
  }
});

async function load(reset: boolean) {
  if (!props.accountId || (reset ? loading.value : loadingMore.value)) return;
  if (reset) loading.value = true;
  else loadingMore.value = true;
  try {
    const result = await cloudShareApi.list(props.accountId, kind.value, reset ? "" : cursor.value);
    items.value = reset ? result.items : [...items.value, ...result.items];
    cursor.value = result.next_cursor;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "获取分享列表失败"));
  } finally {
    loading.value = false;
    loadingMore.value = false;
  }
}

async function copy(item: CloudShareItem) {
  await copyTextToClipboard(shareURLWithPassword(item.url, item.password), { successMessage: "分享信息已复制", errorMessage: "复制失败" });
}

function startEdit(item: CloudShareItem) {
  editing.value = item;
  guestTraffic.value = item.traffic_switch === 2 || item.traffic_switch === 4;
  overTraffic.value = item.traffic_switch === 3 || item.traffic_switch === 4;
}

async function saveTraffic() {
  if (!props.accountId || !editing.value || saving.value) return;
  saving.value = true;
  try {
    await cloudShareApi.update({
      account_id: props.accountId,
      kind: kind.value,
      share_ids: [editing.value.id],
      traffic_switch: shareTrafficSwitch(guestTraffic.value, overTraffic.value),
      traffic_limit_switch: editing.value.traffic_limit_switch,
      traffic_limit: editing.value.traffic_limit,
    });
    toast.success("分享流量设置已保存");
    editing.value = null;
    await load(true);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存分享设置失败"));
  } finally {
    saving.value = false;
  }
}

async function cancelShare(item: CloudShareItem) {
  if (!props.accountId || cancellingId.value) return;
  const accepted = await confirm({
    title: "取消分享？",
    message: `取消后，「${item.name}」的分享链接将立即失效，访问者无法再获取文件。`,
    confirmText: "确认取消",
    cancelText: "保留分享",
    danger: true,
  }).catch(() => false);
  if (!accepted) return;
  cancellingId.value = item.id;
  try {
    await cloudShareApi.cancel({ account_id: props.accountId, share_ids: [item.id] });
    toast.success("分享已取消");
    await load(true);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "取消分享失败"));
  } finally {
    cancellingId.value = "";
  }
}
</script>

<template>
  <AppModal :open="open" size="lg" head-plain @close="emit('close')">
    <template #header>
      <div class="share-heading">
        <h3>分享管理</h3>
        <span>{{ accountName }}</span>
      </div>
    </template>
    <div class="share-manage">
      <div class="share-tabs">
        <button v-if="capability?.supports_free" type="button" :class="{ active: kind === 'free' }" @click="kind = 'free'">免费分享</button>
        <button v-if="capability?.supports_paid" type="button" :class="{ active: kind === 'paid' }" @click="kind = 'paid'">付费分享</button>
      </div>

      <div class="share-content">
        <div v-if="loading" class="share-state"><BusySpinner /><span>正在获取分享记录…</span></div>
        <div v-else-if="!items.length" class="share-state share-state--empty"><SvgIcon name="hand-list" :size="34" /><strong>暂无{{ kind === "paid" ? "付费" : "免费" }}分享</strong></div>
        <div v-else class="share-list">
          <div class="share-row share-row--head"><span>文件</span><span>状态 · 有效期</span><span>使用情况</span></div>
          <div v-for="item in items" :key="item.id" class="share-row">
            <span class="share-name-cell">
              <span class="share-name">
                <strong :title="item.name">{{ item.name }}</strong>
                <small v-if="item.password">提取码 {{ item.password }}</small>
                <small v-else-if="kind === 'paid'">售价 ¥{{ item.pay_amount || 0 }}</small>
                <small v-else>无提取码</small>
              </span>
              <span class="share-actions">
                <AppIconButton icon="hand-copy" label="复制分享" @click="copy(item)" />
                <AppIconButton v-if="capability?.supports_traffic" icon="hand-edit" label="编辑分享" @click="startEdit(item)" />
                <AppIconButton v-if="capability?.supports_cancel" icon="hand-link-off" label="取消分享" :disabled="cancellingId === item.id" @click="cancelShare(item)" />
              </span>
            </span>
            <span class="share-status">
              <em :class="{ expired: item.expired }">{{ item.expired ? "已失效" : "分享中" }}</em>
              <small>{{ item.expiration || "永久有效" }}</small>
            </span>
            <span class="share-stats">
              <small>预览 {{ item.preview_count }} · 转存 {{ item.save_count }} · 下载 {{ item.download_count }} · 已用 {{ formatSize(item.used_bytes || 0) }}<template v-if="kind === 'paid'"> · 订单 {{ item.order_count || 0 }}</template></small>
            </span>
          </div>
        </div>
      </div>

      <div v-if="hasMore && !loading" class="share-more"><AppButton :disabled="loadingMore" @click="load(false)">{{ loadingMore ? "加载中…" : "加载更多" }}</AppButton></div>
      <p v-if="capability?.supports_traffic && !capability?.supports_cancel" class="share-hint">当前网盘只支持修改分享流量设置，不支持取消分享或修改名称、有效期与提取码。</p>
      <p v-else-if="capability?.supports_cancel && !capability?.supports_traffic" class="share-hint">当前网盘支持复制和取消分享，暂不支持修改已创建的分享。</p>
    </div>

    <AppModal :open="Boolean(editing)" size="sm" title="分享链接设置" head-plain nested @close="editing = null">
      <div v-if="editing" class="edit-share">
        <label class="edit-field"><span>分享主题</span><AppInput :model-value="editing.name" disabled /></label>
        <label class="edit-field"><span>有效期</span><AppInput :model-value="editing.expiration || '永久有效'" disabled /></label>
        <div class="edit-field">
          <span>分享形式</span>
          <div class="edit-disabled">{{ kind === "paid" ? "付费提取" : editing.password ? `提取码 ${editing.password}` : "无提取码" }}（Open API 不支持修改）</div>
        </div>
        <div class="edit-field">
          <span>分享流量包</span>
          <div class="edit-options">
            <label><input v-model="guestTraffic" type="checkbox" />游客免登录提取</label>
            <label><input v-model="overTraffic" type="checkbox" />免费用户提取</label>
          </div>
        </div>
      </div>
      <template #footer>
        <AppButton @click="editing = null">取消</AppButton>
        <AppButton variant="primary" :disabled="saving" @click="saveTraffic">{{ saving ? "保存中…" : "修改" }}</AppButton>
      </template>
    </AppModal>
  </AppModal>
</template>

<style scoped>
.share-manage { min-height: 380px; display: flex; flex-direction: column; gap: 14px; }
.share-tabs { display: flex; gap: 8px; border-bottom: 1px solid var(--border-soft); }
.share-tabs button { padding: 9px 13px; border: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--text-muted); font-weight: 600; cursor: pointer; }
.share-tabs button.active { border-bottom-color: var(--brand); color: var(--brand); }
.share-content { min-height: 250px; }
.share-state { min-height: 250px; display: flex; align-items: center; justify-content: center; gap: 9px; color: var(--text-muted); }
.share-state--empty { flex-direction: column; }
.share-list { overflow: hidden; }
.share-row { display: grid; grid-template-columns: minmax(0, 1fr) 172px 250px; align-items: center; gap: 16px; min-height: 64px; padding: 9px 12px; border-bottom: 1px solid var(--border-soft); }
.share-row:not(.share-row--head):hover { background: var(--surface-sunken); }
.share-row:last-child { border-bottom: 0; }
.share-row--head { min-height: 38px; color: var(--text-muted); background: var(--surface-sunken); font-size: 12px; }
.share-name-cell { min-width: 0; display: flex; align-items: center; gap: 10px; }
.share-name, .share-status, .share-stats { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.share-name { flex: 1 1 auto; }
.share-stats { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.share-name strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.share-row small { color: var(--text-muted); font-size: 11px; }
.share-status em { width: fit-content; color: var(--success); font-size: 12px; font-style: normal; font-weight: 600; }
.share-status em.expired { color: var(--text-muted); font-weight: 400; }
.share-actions { display: flex; align-items: center; justify-content: flex-end; gap: 2px; margin-left: auto; opacity: 0; visibility: hidden; transition: opacity .15s ease; }
.share-row:hover .share-actions, .share-row:focus-within .share-actions { opacity: 1; visibility: visible; }
.share-actions :deep(.icon-btn) { width: 28px; height: 28px; }
.share-actions :deep(.lp-svg-icon) { width: 15px !important; height: 15px !important; }
.share-more { display: flex; justify-content: center; }
.share-hint { margin: 0; color: var(--text-muted); font-size: 11px; text-align: center; }
.edit-share { display: flex; flex-direction: column; gap: 16px; padding: 2px 4px; }
.edit-field { display: flex; flex-direction: column; gap: 8px; color: var(--text-muted); font-size: 13px; }
.edit-disabled { padding: 12px; border-radius: var(--radius-control); background: var(--surface-sunken); color: var(--text-disabled); text-align: center; }
.edit-options { display: flex; align-items: center; gap: 24px; padding: 12px; border-radius: var(--radius-control); background: var(--surface-sunken); }
.edit-options label { display: flex; align-items: center; gap: 7px; color: var(--text-regular); }
@media (max-width: 720px) {
  .share-row { grid-template-columns: 1fr; }
  .share-row--head { display: none; }
  .share-status, .share-stats { grid-column: 1; }
  .share-stats { white-space: normal; overflow: visible; }
  .share-actions { opacity: 1; visibility: visible; justify-content: flex-start; }
  .edit-options { align-items: flex-start; flex-direction: column; gap: 10px; }
}
</style>

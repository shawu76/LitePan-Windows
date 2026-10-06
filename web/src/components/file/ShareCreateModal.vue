<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppModal from "@/components/base/AppModal.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { cloudShareApi } from "@/api/cloudShare";
import { getApiErrorMessage } from "@/api/client";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import { shareURLWithPassword, shareTrafficSwitch } from "@/utils/cloudShare";
import type { FileItem } from "@/api/types";
import type { CloudShareCapabilities, CloudShareItem, CloudShareKind } from "@/types/cloud-share";

const props = defineProps<{
  open: boolean;
  accountId: number | null;
  files: FileItem[];
  capability: CloudShareCapabilities | null;
}>();
const emit = defineEmits<{ close: [] }>();

const kind = ref<CloudShareKind>("free");
const name = ref("");
const expireDays = ref(7);
const passwordMode = ref<"none" | "random" | "custom">("random");
const password = ref("");
const payAmount = ref("10");
const resourceDesc = ref("");
const guestTraffic = ref(false);
const overTraffic = ref(false);
const submitting = ref(false);
const created = ref<CloudShareItem | null>(null);

const canPaid = computed(() => Boolean(props.capability?.supports_paid));
const canPassword = computed(() => Boolean(props.capability?.supports_password));
const overLimit = computed(() => Boolean(props.capability?.max_items && props.files.length > props.capability.max_items));
const customPasswordInvalid = computed(() => passwordMode.value === "custom" && !/^[A-Za-z0-9]{4}$/.test(password.value));
const submitDisabled = computed(() => {
  if (submitting.value || !props.accountId || !name.value.trim() || !props.files.length || overLimit.value) return true;
  if (kind.value === "paid") {
    const amount = Number(payAmount.value);
    return !Number.isInteger(amount) || amount < 1 || amount > 1000;
  }
  return customPasswordInvalid.value;
});

watch(() => props.open, (open) => {
  if (!open) return;
  kind.value = "free";
  name.value = props.files.length === 1 ? (props.files[0]?.name || "") : `${props.files[0]?.name || "批量文件"}等 ${props.files.length} 项`;
  expireDays.value = props.capability?.expire_days?.includes(7) ? 7 : (props.capability?.expire_days?.[0] ?? 0);
  passwordMode.value = canPassword.value ? "random" : "none";
  password.value = canPassword.value ? randomPassword() : "";
  payAmount.value = "10";
  resourceDesc.value = "";
  guestTraffic.value = false;
  overTraffic.value = false;
  created.value = null;
});

watch(passwordMode, (mode) => {
  if (mode === "none") password.value = "";
  if (mode === "random") password.value = randomPassword();
});

function randomPassword() {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const values = crypto.getRandomValues(new Uint32Array(4));
  return Array.from(values, (value) => chars[value % chars.length]).join("");
}

function updateCustomPassword(value: string | number | null) {
  password.value = String(value ?? "").replace(/[^A-Za-z0-9]/g, "").slice(0, 4);
}

async function submit() {
  if (!props.accountId || submitDisabled.value) return;
  submitting.value = true;
  try {
    created.value = await cloudShareApi.create({
      account_id: props.accountId,
      kind: kind.value,
      name: name.value.trim(),
      file_ids: props.files.map((file) => file.id),
      expire_days: kind.value === "free" ? expireDays.value : 0,
      password: kind.value === "free" ? password.value.trim() : undefined,
      pay_amount: kind.value === "paid" ? Number(payAmount.value) : undefined,
      reward_enabled: kind.value === "paid" ? true : undefined,
      resource_desc: kind.value === "paid" ? resourceDesc.value.trim() : undefined,
      traffic_switch: shareTrafficSwitch(guestTraffic.value, overTraffic.value),
      traffic_limit_switch: 1,
      traffic_limit: 0,
    });
    toast.success("分享创建成功");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "创建分享失败"));
  } finally {
    submitting.value = false;
  }
}

async function copyResult() {
  if (!created.value) return;
  await copyTextToClipboard(shareURLWithPassword(created.value.url, created.value.password), { successMessage: "分享信息已复制", errorMessage: "复制失败" });
}
</script>

<template>
  <AppModal :open="open" size="sm" head-plain @close="emit('close')">
    <template #header>
      <div class="share-heading">
        <h3>分享链接设置</h3>
        <span>严厉打击违法犯罪活动</span>
        <SettingsHelpTooltip icon="circle-info" variant="panel">
          严禁分享任何色情、暴力以及其他违规违法内容。一经发现，我们将采取严厉措施，包括但不限于对涉及账号进行封停，不予退还VIP会费。对于情节严重者，我们将收集相关违法分享者的日志及信息，并报送当地网安部门进行处理。
        </SettingsHelpTooltip>
      </div>
    </template>
    <div v-if="!created" class="share-form">
      <p v-if="overLimit" class="share-warning">当前选中 {{ files.length }} 项，该网盘一次最多分享 {{ capability?.max_items }} 项。</p>

      <label class="share-field"><span>设置分享主题</span><AppInput v-model="name" maxlength="34" placeholder="请输入分享主题" /></label>

      <template v-if="kind === 'free'">
        <div class="share-field">
          <span>有效期</span>
          <div class="share-segments">
            <button v-for="days in capability?.expire_days || [1, 7, 30, 0]" :key="days" type="button" :class="{ active: expireDays === days }" @click="expireDays = days">
              {{ days === 0 ? "永久" : `${days}天` }}
            </button>
          </div>
        </div>
        <div class="share-field">
          <span>分享形式</span>
          <div class="share-segments">
            <button type="button" :class="{ active: passwordMode === 'none' }" @click="passwordMode = 'none'">无提取码</button>
            <button v-if="canPassword" type="button" :class="{ active: passwordMode === 'random' }" @click="passwordMode = 'random'">随机生成</button>
            <button v-if="canPassword" type="button" :class="{ active: passwordMode === 'custom' }" @click="passwordMode = 'custom'">自定义</button>
            <button v-if="canPaid" type="button" @click="kind = 'paid'">付费提取</button>
          </div>
          <div v-if="passwordMode === 'custom'" class="share-inline-field" :class="{ 'share-inline-field--error': customPasswordInvalid && password.length > 0 }">
            <span>自定义提取码</span>
            <AppInput :model-value="password" maxlength="4" placeholder="请输入4位字母或数字" @update:model-value="updateCustomPassword" />
          </div>
          <div v-else-if="passwordMode === 'random'" class="share-inline-field">
            <span>随机提取码</span>
            <strong>{{ password }}</strong>
            <button type="button" @click="password = randomPassword()">换一个</button>
          </div>
        </div>
      </template>

      <template v-else>
        <div class="share-field">
          <span>分享形式</span>
          <div class="share-segments">
            <button type="button" @click="kind = 'free'; passwordMode = 'none'">无提取码</button>
            <button v-if="canPassword" type="button" @click="kind = 'free'; passwordMode = 'random'">随机提取码</button>
            <button v-if="canPassword" type="button" @click="kind = 'free'; passwordMode = 'custom'">自定义提取码</button>
            <button type="button" class="active">付费提取</button>
          </div>
        </div>
        <div class="paid-box">
          <label class="share-inline-field"><span>付费金额</span><AppInput v-model="payAmount" type="number" placeholder="付费金额范围1-1000元" /></label>
          <label class="share-inline-field share-inline-field--top"><span>资源描述</span><textarea v-model="resourceDesc" rows="3" placeholder="请输入付费资源内容，用于平台审核" /></label>
        </div>
      </template>

      <div v-if="capability?.supports_traffic" class="traffic-section">
        <div class="traffic-section__title">分享流量包</div>
        <div class="traffic-options">
          <label class="share-check"><input v-model="guestTraffic" type="checkbox" />游客免登录提取</label>
          <label class="share-check"><input v-model="overTraffic" type="checkbox" />免费用户提取</label>
        </div>
      </div>
    </div>

    <div v-else class="share-success">
      <span class="share-success__icon"><SvgIcon name="hand-check" :size="28" /></span>
      <strong>分享创建成功</strong>
      <p>{{ created.name }}</p>
      <div class="share-result"><span>{{ created.url }}</span><em v-if="created.password">提取码 {{ created.password }}</em></div>
      <AppButton variant="primary" @click="copyResult"><SvgIcon name="copy" :size="16" />复制分享信息</AppButton>
    </div>

    <template v-if="!created" #footer>
      <AppButton variant="primary" :disabled="submitDisabled" @click="submit">{{ submitting ? "正在创建…" : kind === "paid" ? "提交审核" : "创建链接" }}</AppButton>
    </template>
  </AppModal>
</template>

<style scoped>
.share-heading { display: flex; align-items: center; min-width: 0; gap: 10px; }
.share-heading h3 { margin: 0 12px 0 0; color: var(--text); font-size: 19px; font-weight: 700; }
.share-heading span { color: var(--text-muted); font-size: 13px; }
.share-heading :deep(.settings-help) { color: var(--text-muted); }
.share-form { display: flex; flex-direction: column; gap: 16px; padding: 0 8px 4px; }
.share-warning { margin: -4px 0 0; padding: 9px 11px; border-radius: var(--radius-sm); background: color-mix(in srgb, var(--warning) 12%, var(--surface)); color: var(--warning); font-size: 12px; }
.share-field { display: flex; flex-direction: column; gap: 8px; color: var(--text-muted); font-size: 13px; font-weight: 400; }
.share-segments { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(0, 1fr); gap: 4px; padding: 5px; border-radius: var(--radius-control); background: var(--surface-sunken); }
.share-segments button { min-height: 36px; padding: 0 7px; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--text-regular); font: inherit; font-size: 13px; cursor: pointer; }
.share-segments button.active { background: var(--surface); color: var(--text); box-shadow: var(--shadow-card); font-weight: 600; }
.share-inline-field { display: grid; grid-template-columns: 110px minmax(0, 1fr) auto; align-items: center; gap: 10px; padding: 8px 10px; border-radius: var(--radius-control); background: var(--surface-sunken); color: var(--text-muted); font: inherit; font-size: 13px; }
.share-inline-field strong { letter-spacing: .22em; color: var(--text); }
.share-inline-field > button { border: 0; background: transparent; color: var(--brand); cursor: pointer; }
.share-inline-field--top { align-items: start; }
.share-inline-field--error :deep(.app-input) { border-color: var(--danger); }
.paid-box { display: flex; flex-direction: column; gap: 10px; padding: 10px; border-radius: var(--radius-control); background: var(--surface-sunken); }
.share-inline-field textarea { width: 100%; resize: none; padding: 9px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); font: inherit; font-size: 13px; line-height: 1.55; }
.share-inline-field textarea:focus { outline: none; border-color: var(--brand); }
.share-check { display: flex; align-items: center; gap: 7px; color: var(--text-regular); font: inherit; font-size: 13px; }
.traffic-section { display: flex; flex-direction: column; gap: 8px; }
.traffic-section__title { color: var(--text-muted); font-size: 13px; }
.traffic-options { display: flex; align-items: center; gap: 28px; padding: 11px 14px; border-radius: var(--radius-control); background: var(--surface-sunken); }
.share-success { display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 26px 8px 16px; text-align: center; }
.share-success__icon { width: 58px; height: 58px; display: inline-flex; align-items: center; justify-content: center; border-radius: 50%; background: var(--success-soft); color: var(--success); }
.share-success > strong { font-size: 18px; }
.share-success p { margin: 0; color: var(--text-muted); }
.share-result { width: min(100%, 620px); display: flex; align-items: center; gap: 10px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-sunken); }
.share-result span { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; }
.share-result em { color: var(--brand); font-style: normal; white-space: nowrap; }
@media (max-width: 640px) {
  .share-segments { grid-auto-flow: row; grid-template-columns: repeat(2, 1fr); }
  .share-heading span, .share-heading svg { display: none; }
  .share-inline-field { grid-template-columns: 1fr; }
  .traffic-options { align-items: flex-start; flex-direction: column; gap: 10px; }
  .share-result { flex-direction: column; align-items: stretch; }
}
</style>

<script setup lang="ts">
import { computed, ref } from "vue";
import {
  beginTwoFactorSetup,
  confirmTwoFactorSetup,
  disableTwoFactor,
  regenerateTwoFactorRecoveryCodes,
  type TwoFactorSetupResult,
} from "@/api/auth";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const props = defineProps<{ enabled: boolean }>();
const emit = defineEmits<{ changed: [enabled: boolean] }>();

type Action = "setup" | "rebind" | "disable" | "recovery";
type Step = "manage" | "verify" | "scan" | "code" | "recovery";

const open = ref(false);
const action = ref<Action>("setup");
const step = ref<Step>("verify");
const password = ref("");
const verificationCode = ref("");
const setup = ref<TwoFactorSetupResult | null>(null);
const recoveryCodes = ref<string[]>([]);
const loading = ref(false);

const title = computed(() => {
  if (step.value === "manage") return "两步验证";
  if (step.value === "scan") return "扫码绑定";
  if (step.value === "code") return "输入动态码";
  if (step.value === "recovery") return "保存恢复码";
  if (action.value === "disable") return "关闭两步验证";
  if (action.value === "recovery") return "重新生成恢复码";
  if (action.value === "rebind") return "重新绑定验证器";
  return "开启两步验证";
});

const stepLabels = computed(() => {
  if (step.value === "manage" || action.value === "disable") return [];
  if (action.value === "recovery") return ["验证身份", "保存恢复码"];
  return ["验证身份", "扫码绑定", "输入动态码", "保存恢复码"];
});

const stepIndex = computed(() => {
  if (action.value === "recovery") return step.value === "recovery" ? 1 : 0;
  const map: Record<Step, number> = { manage: 0, verify: 0, scan: 1, code: 2, recovery: 3 };
  return map[step.value];
});

function resetForm() {
  password.value = "";
  verificationCode.value = "";
  setup.value = null;
  recoveryCodes.value = [];
}

function start(next: Action) {
  action.value = next;
  step.value = "verify";
  resetForm();
  open.value = true;
}

function startManage() {
  action.value = "setup";
  step.value = "manage";
  resetForm();
  open.value = true;
}

function close() {
  if (loading.value) return;
  open.value = false;
  resetForm();
}

async function submitVerify() {
  if (!password.value) {
    toast.error("请输入当前管理员密码");
    return;
  }
  if (props.enabled && !verificationCode.value.trim()) {
    toast.error("请输入当前动态码或恢复码");
    return;
  }
  loading.value = true;
  try {
    if (action.value === "disable") {
      await disableTwoFactor({ password: password.value, code: verificationCode.value.trim() });
      emit("changed", false);
      toast.success("两步验证已关闭");
      open.value = false;
      resetForm();
      return;
    }
    if (action.value === "recovery") {
      const result = await regenerateTwoFactorRecoveryCodes({
        password: password.value,
        code: verificationCode.value.trim(),
      });
      recoveryCodes.value = result.recovery_codes;
      password.value = "";
      verificationCode.value = "";
      step.value = "recovery";
      return;
    }
    setup.value = await beginTwoFactorSetup({
      password: password.value,
      verification_code: verificationCode.value.trim() || undefined,
    });
    password.value = "";
    verificationCode.value = "";
    step.value = "scan";
  } catch (error) {
    toast.error(error instanceof Error ? error.message : "验证失败");
  } finally {
    loading.value = false;
  }
}

async function confirmSetup() {
  if (!setup.value || !/^\d{6}$/.test(verificationCode.value.trim())) {
    toast.error("请输入验证器显示的 6 位动态码");
    return;
  }
  loading.value = true;
  try {
    const result = await confirmTwoFactorSetup({
      setup_token: setup.value.setup_token,
      code: verificationCode.value.trim(),
    });
    recoveryCodes.value = result.recovery_codes;
    setup.value = null;
    password.value = "";
    verificationCode.value = "";
    emit("changed", true);
    step.value = "recovery";
    toast.success("两步验证已开启");
  } catch (error) {
    toast.error(error instanceof Error ? error.message : "绑定失败");
  } finally {
    loading.value = false;
  }
}

async function copyText(text: string, message: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(message);
  } catch {
    toast.error("复制失败，请手动复制");
  }
}

function downloadRecoveryCodes() {
  if (!recoveryCodes.value.length) return;
  const rows = [["序号", "恢复码"], ...recoveryCodes.value.map((code, index) => [String(index + 1), code])];
  const csv = rows.map((row) => row.join(",")).join("\r\n");
  const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, "");
  const blob = new Blob([`\uFEFF${csv}\r\n`], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `LitePan-两步验证恢复码-${stamp}.csv`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
  toast.success("恢复码已下载，请妥善保存");
}
</script>

<template>
  <div class="two-factor" :class="{ 'two-factor--on': enabled }">
    <div class="two-factor__state">
      <span class="two-factor__icon"><SvgIcon name="shield" :size="17" /></span>
      <span class="two-factor__text">{{ enabled ? "已开启" : "未开启" }}</span>
    </div>
    <div class="two-factor__ops">
      <AppButton v-if="!enabled" type="button" variant="primary" @click="start('setup')">立即开启</AppButton>
      <AppButton v-else type="button" variant="secondary" @click="startManage">管理</AppButton>
    </div>
  </div>

  <AppModal :open="open" size="sm" :title="title" nested @close="close">
    <div v-if="stepLabels.length" class="factor-steps">
      <template v-for="(label, index) in stepLabels" :key="label">
        <span v-if="index > 0" class="factor-steps__line" />
        <span class="factor-steps__item" :class="{ 'factor-steps__item--active': index === stepIndex }">
          <span class="factor-steps__num">{{ index + 1 }}</span>{{ label }}
        </span>
      </template>
    </div>

    <!-- 已开启：所有管理动作收在这里 -->
    <div v-if="step === 'manage'" class="factor-manage">
      <div class="factor-state">
        <span class="factor-state__dot" />
        <span><b>已开启</b><small>登录时除密码外还需要验证器动态码</small></span>
      </div>
      <div class="factor-actions">
        <button type="button" class="factor-action" @click="start('rebind')">
          <span class="factor-action__tx"><b>重新绑定验证器</b><small>换手机或验证器丢失时使用</small></span>
          <span class="factor-action__go">›</span>
        </button>
        <button type="button" class="factor-action" @click="start('recovery')">
          <span class="factor-action__tx"><b>重新生成恢复码</b><small>旧的恢复码会立即失效</small></span>
          <span class="factor-action__go">›</span>
        </button>
        <button type="button" class="factor-action factor-action--danger" @click="start('disable')">
          <span class="factor-action__tx"><b>关闭两步验证</b><small>关闭后登录只需要密码</small></span>
          <span class="factor-action__go">›</span>
        </button>
      </div>
    </div>

    <div v-else-if="step === 'verify'" class="factor-form">
      <p class="factor-hint">
        {{ enabled ? "请先验证当前管理员密码和两步验证信息。" : "开启前请验证当前管理员密码。" }}
      </p>
      <label class="factor-field">
        <span>当前管理员密码</span>
        <AppInput v-model="password" type="password" autocomplete="current-password" placeholder="请输入当前密码" />
      </label>
      <label v-if="enabled" class="factor-field">
        <span>动态码或恢复码</span>
        <AppInput v-model="verificationCode" autocomplete="one-time-code" placeholder="6 位动态码 / 恢复码" />
      </label>
    </div>

    <div v-else-if="step === 'scan' && setup" class="factor-scan">
      <p class="factor-hint">使用 Microsoft Authenticator、Google Authenticator 等验证器扫描二维码。</p>
      <div class="factor-qr"><img :src="setup.qr_code" alt="两步验证二维码" /></div>
      <div class="factor-secret">
        <span>无法扫码时手动输入</span>
        <code>{{ setup.secret }}</code>
        <button type="button" @click="copyText(setup.secret, '密钥已复制')">复制</button>
      </div>
    </div>

    <div v-else-if="step === 'code'" class="factor-form">
      <p class="factor-hint">输入验证器当前显示的 6 位动态码，用来确认绑定成功。</p>
      <label class="factor-field">
        <span>6 位动态码</span>
        <AppInput v-model="verificationCode" autocomplete="one-time-code" inputmode="numeric" placeholder="000000" />
      </label>
    </div>

    <div v-else class="factor-recovery">
      <div class="factor-warning">请立即保存这些恢复码。每枚只能使用一次，验证器不可用时用它登录。</div>
      <div class="factor-recovery__grid">
        <code v-for="code in recoveryCodes" :key="code">{{ code }}</code>
      </div>
    </div>

    <template #footer>
      <template v-if="step === 'manage'">
        <AppButton type="button" variant="secondary" @click="close">关闭</AppButton>
      </template>
      <template v-else-if="step === 'verify'">
        <AppButton type="button" variant="cancel" @click="close">取消</AppButton>
        <AppButton
          type="button"
          :variant="action === 'disable' ? 'danger' : 'primary'"
          :disabled="loading"
          @click="submitVerify"
        >
          {{ loading ? "验证中…" : action === "disable" ? "确认关闭" : "继续" }}
        </AppButton>
      </template>
      <template v-else-if="step === 'scan'">
        <AppButton type="button" variant="cancel" @click="close">取消</AppButton>
        <AppButton type="button" variant="primary" @click="step = 'code'">我已扫码，下一步</AppButton>
      </template>
      <template v-else-if="step === 'code'">
        <AppButton type="button" variant="cancel" @click="step = 'scan'">上一步</AppButton>
        <AppButton type="button" variant="primary" :disabled="loading" @click="confirmSetup">
          {{ loading ? "绑定中…" : "验证并开启" }}
        </AppButton>
      </template>
      <template v-else>
        <AppButton type="button" variant="secondary" @click="downloadRecoveryCodes">下载恢复码（CSV）</AppButton>
        <span class="factor-foot__spacer" />
        <AppButton type="button" variant="primary" @click="close">我已安全保存</AppButton>
      </template>
    </template>
  </AppModal>
</template>

<style scoped>
/* 自己占一张设置卡片：内部按设置项的两列栅格排（1fr / 2fr），和上面的行对齐 */
.two-factor {
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 20px;
  align-items: center;
  padding: 12px 0 16px;
}
.two-factor__state { display: inline-flex; align-items: center; gap: 10px; min-width: 0; }
.two-factor__icon {
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  border: 1px solid var(--border);
  color: var(--text-muted);
}
.two-factor--on .two-factor__icon {
  background: color-mix(in srgb, var(--success) 10%, var(--surface));
  border-color: color-mix(in srgb, var(--success) 30%, var(--border));
  color: var(--success);
}
.two-factor__text { font-size: 14px; color: var(--text-muted); }
.two-factor--on .two-factor__text { color: #047857; font-weight: 500; }
.two-factor__ops { display: flex; align-items: center; gap: 10px; }

/* 弹窗内的步骤条 */
.factor-foot__spacer { flex: 1 1 auto; }

.factor-steps { display: flex; align-items: center; gap: 8px; margin: 2px 0 16px; }
.factor-steps__item { display: flex; align-items: center; gap: 7px; font-size: 12px; color: var(--text-muted); white-space: nowrap; }
.factor-steps__num {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--border-soft);
  color: var(--text-muted);
  font-size: 11px;
  font-weight: 700;
}
.factor-steps__item--active { color: var(--brand); }
.factor-steps__item--active .factor-steps__num { background: var(--brand); color: #fff; }
.factor-steps__line { flex: 1 1 auto; height: 1px; background: var(--border); }

.factor-manage, .factor-form, .factor-scan, .factor-recovery { display: grid; gap: 16px; }
.factor-hint { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.7; }
.factor-field { display: grid; gap: 8px; color: var(--text); font-size: 13px; font-weight: 600; }

.factor-state {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 13px 15px;
  border: 1px solid color-mix(in srgb, var(--success) 30%, var(--border));
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--success) 6%, var(--surface));
}
.factor-state__dot { width: 10px; height: 10px; border-radius: 50%; background: var(--success); box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.14); }
.factor-state b { display: block; font-size: 13px; color: var(--text); }
.factor-state small { display: block; margin-top: 2px; font-size: 12px; color: var(--text-muted); }

.factor-actions { display: grid; gap: 8px; }
.factor-action {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 12px 14px;
  text-align: left;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: var(--text);
  transition: var(--transition);
}
.factor-action:hover { border-color: var(--brand); background: var(--surface-sunken); }
.factor-action--danger:hover { border-color: var(--danger); background: color-mix(in srgb, var(--danger) 6%, var(--surface)); }
.factor-action__tx { flex: 1 1 auto; min-width: 0; }
.factor-action__tx b { display: block; font-size: 13px; font-weight: 600; }
.factor-action__tx small { display: block; margin-top: 2px; font-size: 12px; color: var(--text-muted); }
.factor-action__go { color: var(--text-muted); }

.factor-qr { display: grid; place-items: center; }
.factor-qr img { width: 196px; height: 196px; padding: 10px; background: #fff; border: 1px solid var(--border); border-radius: var(--radius-card); box-sizing: border-box; }
.factor-secret {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 14px;
  border-radius: var(--radius-control);
  background: var(--surface-sunken);
}
.factor-secret span { font-size: 12px; color: var(--text-muted); }
.factor-secret code { flex: 1 1 auto; font-size: 13px; letter-spacing: 0.08em; color: var(--text); overflow-wrap: anywhere; }
.factor-secret button { border: 0; background: none; color: var(--brand); }

.factor-warning {
  padding: 11px 14px;
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--warning) 12%, var(--surface));
  color: #b45309;
  font-size: 12.5px;
  line-height: 1.7;
}
.factor-recovery__grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.factor-recovery__grid code { padding: 9px; text-align: center; background: var(--surface-sunken); border-radius: var(--radius-sm); font-size: 13px; letter-spacing: 0.06em; }

@media (max-width: 640px) {
  .two-factor { align-items: flex-start; flex-wrap: wrap; }
  .two-factor__desc { flex: 1 1 100%; order: 3; }
  .factor-steps { flex-wrap: wrap; }
  .factor-recovery__grid { grid-template-columns: 1fr; }
}
</style>

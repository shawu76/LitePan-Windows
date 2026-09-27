<script setup lang="ts">
import { computed } from "vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import ModalCloseButton from "@/components/base/ModalCloseButton.vue";
import AppButton from "@/components/base/AppButton.vue";
import ConfirmDialogPresets from "@/components/base/ConfirmDialogPresets.vue";
import {
  CONFIRM_ICON_SVG,
  CONFIRM_ICON_TONE,
  type ConfirmAction,
  type ConfirmIcon,
  type ConfirmPreset,
  type ConfirmSize,
} from "@/types/confirm";

const props = withDefaults(
  defineProps<{
    title: string;
    message?: string;
    icon?: ConfirmIcon;
    confirmText?: string;
    cancelText?: string;
    danger?: boolean;
    size?: ConfirmSize;
    loading?: boolean;
    hint?: string;
    checkboxLabel?: string;
    checked?: boolean;
    actions?: readonly ConfirmAction[];
    showCancel?: boolean;
    preset?: ConfirmPreset;
    presetData?: Record<string, unknown>;
  }>(),
  {
    message: "",
    icon: "warning",
    confirmText: "确定",
    cancelText: "取消",
    danger: true,
    size: "md",
    loading: false,
    checked: false,
    showCancel: true,
  },
);

const emit = defineEmits<{
  cancel: [];
  confirm: [];
  action: [actionId: string];
  "update:checked": [value: boolean];
}>();

const iconSvg = computed(() => CONFIRM_ICON_SVG[props.icon]);
const iconTone = computed(() => CONFIRM_ICON_TONE[props.icon]);
const useCustomActions = computed(() => (props.actions?.length ?? 0) > 0);
const isStackPreset = computed(() => (
  props.preset === "upload-notice" || props.preset === "cross-transfer-probe-notice"
));

function onCheckboxInput(event: Event) {
  emit("update:checked", (event.target as HTMLInputElement).checked);
}
</script>

<template>
  <div
    class="confirm-modal"
    :class="`confirm-modal--${size}`"
    role="dialog"
    aria-modal="true"
    :aria-labelledby="title ? 'confirm-dialog-title' : undefined"
  >
    <header class="confirm-modal__head">
      <h3 id="confirm-dialog-title" class="confirm-modal__title">{{ title }}</h3>
      <ModalCloseButton :disabled="loading" @click="emit('cancel')" />
    </header>

    <div class="confirm-modal__body">
      <slot name="body">
        <div v-if="isStackPreset" class="confirm-modal__confirm confirm-modal__confirm--stack">
          <ConfirmDialogPresets :preset="preset!" :preset-data="presetData" />
          <label v-if="checkboxLabel" class="confirm-modal__option">
            <input type="checkbox" :checked="checked" @change="onCheckboxInput" />
            <span>{{ checkboxLabel }}</span>
          </label>
        </div>
        <div v-else class="confirm-modal__confirm">
          <div class="confirm-modal__icon-wrap">
            <div class="confirm-modal__flat-icon" :class="`confirm-modal__flat-icon--${iconTone}`">
              <SvgIcon :name="iconSvg" :size="18" class-name="confirm-modal__icon" />
            </div>
          </div>
          <div class="confirm-modal__content">
            <slot name="message">
              <ConfirmDialogPresets
                v-if="preset === 'upload-conflict'"
                preset="upload-conflict"
                :preset-data="presetData"
              />
              <p v-else-if="message" class="confirm-modal__message">{{ message }}</p>
            </slot>
            <p v-if="hint" class="confirm-modal__hint">{{ hint }}</p>
            <label v-if="checkboxLabel && !isStackPreset" class="confirm-modal__option">
              <input type="checkbox" :checked="checked" @change="onCheckboxInput" />
              <span>{{ checkboxLabel }}</span>
            </label>
            <slot />
          </div>
        </div>
      </slot>
    </div>

    <footer class="confirm-modal__foot" :class="{ 'confirm-modal__foot--multi': useCustomActions }">
      <slot name="footer">
        <template v-if="useCustomActions">
          <AppButton
            v-for="action in actions"
            :key="action.id"
            :variant="action.variant || 'cancel'"
            class="confirm-modal__btn"
            :disabled="loading"
            @click="emit('action', action.id)"
          >
            {{ action.label }}
          </AppButton>
        </template>
        <template v-else>
          <AppButton
            v-if="showCancel"
            variant="cancel"
            class="confirm-modal__btn"
            :disabled="loading"
            @click="emit('cancel')"
          >
            {{ cancelText }}
          </AppButton>
          <AppButton
            :variant="danger ? 'danger' : 'primary'"
            class="confirm-modal__btn"
            :disabled="loading"
            @click="emit('confirm')"
          >
            {{ loading ? `${confirmText}中…` : confirmText }}
          </AppButton>
        </template>
      </slot>
    </footer>
  </div>
</template>

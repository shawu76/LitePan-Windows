import { computed, reactive, ref, type ComputedRef, type Ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import { normalizeBoolSetting, settingItemChanged } from "@/utils/settingsDirty";

export type SettingsFieldCompare<T extends object> = (
  key: keyof T,
  current: T[keyof T],
  original: T[keyof T],
) => boolean;

export function useSettingsForm<T extends object>(
  initial: T,
  options?: {
    compareField?: SettingsFieldCompare<T>;
  },
) {
  const settings = reactive({ ...initial }) as T;
  const original = reactive({ ...initial }) as T;

  function isFieldChanged(key: keyof T): boolean {
    if (options?.compareField) {
      return options.compareField(key, settings[key], original[key]);
    }
    return settings[key] !== original[key];
  }

  const isDirty = computed(() =>
    (Object.keys(settings) as Array<keyof T>).some((key) => isFieldChanged(key)),
  );

  function snapshotBaseline() {
    Object.assign(original, settings);
  }

  function applyBaseline(data: Partial<T>) {
    Object.assign(settings, data);
    snapshotBaseline();
  }

  function revert() {
    Object.assign(settings, original);
  }

  return {
    settings,
    original,
    isDirty,
    isFieldChanged,
    snapshotBaseline,
    applyBaseline,
    revert,
  };
}

export function useSettingsKVForm() {
  const values = reactive<Record<string, string>>({});
  const baseline = reactive<Record<string, string>>({});

  function assignEntry(key: string, value: string, type: string) {
    const next = type === "bool" ? normalizeBoolSetting(value) : value;
    values[key] = next;
    baseline[key] = next;
  }

  function revertEntries(keys: string[]) {
    for (const key of keys) {
      if (key in baseline) values[key] = baseline[key];
    }
  }

  function isEntryChanged(key: string, type: string) {
    return settingItemChanged(type, values[key] ?? "", baseline[key] ?? "");
  }

  function isAnyChanged(items: Array<{ key: string; type: string }>) {
    return items.some((it) => isEntryChanged(it.key, it.type));
  }

  return {
    values,
    baseline,
    assignEntry,
    revertEntries,
    isEntryChanged,
    isAnyChanged,
  };
}

type SettingsSaveOptions = {
  successMessage?: string;
  errorMessage?: string;
  silent?: boolean;
  rethrow?: boolean;
};

export function useSettingsSave() {
  const saving = ref(false);

  async function runSave(task: () => Promise<void>, options: SettingsSaveOptions = {}): Promise<boolean> {
    if (saving.value) return false;
    saving.value = true;
    try {
      await task();
      if (options.successMessage && !options.silent) toast.success(options.successMessage);
      return true;
    } catch (error) {
      toast.error(getApiErrorMessage(error, options.errorMessage || "保存失败"));
      if (options.rethrow) throw error;
      return false;
    } finally {
      saving.value = false;
    }
  }

  return { saving, runSave };
}

export type SettingsPanelExpose = {
  isDirty?: () => boolean;
  saving?: Ref<boolean> | boolean;
  save?: () => Promise<void>;
  reload?: () => void | Promise<void>;
  revert?: () => void;
  getDirty?: () => boolean;
  getDefaultScanInterval?: () => number;
  saveSettings?: (silent?: boolean) => Promise<void>;
};

export function readPanelSaving(saving: SettingsPanelExpose["saving"]): boolean {
  if (saving == null) return false;
  return typeof saving === "object" && "value" in saving ? saving.value : Boolean(saving);
}

export function bindSettingsPanelExpose(
  state: {
    isDirty: ComputedRef<boolean>;
    saving: Ref<boolean>;
    save: () => Promise<void>;
    reload: () => void | Promise<void>;
    revert: () => void;
  },
  extras?: Record<string, unknown>,
) {
  return {
    isDirty: () => state.isDirty.value,
    saving: state.saving,
    save: state.save,
    reload: state.reload,
    revert: state.revert,
    ...extras,
  };
}

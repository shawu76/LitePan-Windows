import { computed, onUnmounted, toValue, watch, type MaybeRefOrGetter } from "vue";
import { lockPageScroll, unlockPageScroll } from "@/utils/scrollLock";
import { isTopModal, popModal, pushModal } from "@/composables/modalStack";

// 只允许栈顶弹窗响应 ESC，并统一管理页面滚动锁。
export function useModalDismiss(isOpen: MaybeRefOrGetter<boolean>, onDismiss: () => void) {
  const token = Symbol("modal");
  let active = false;

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape" && isTopModal(token)) onDismiss();
  }

  function sync(open: boolean) {
    if (open && !active) {
      active = true;
      pushModal(token);
      window.addEventListener("keydown", onKey);
      lockPageScroll();
    } else if (!open && active) {
      active = false;
      popModal(token);
      window.removeEventListener("keydown", onKey);
      unlockPageScroll();
    }
  }

  const open = computed(() => toValue(isOpen));
  watch(open, sync, { immediate: true });

  onUnmounted(() => {
    if (!active) return;
    active = false;
    popModal(token);
    window.removeEventListener("keydown", onKey);
    unlockPageScroll();
  });
}

/**
 * 信息面板的显示 / 隐藏转场：两边都用最简单的淡入淡出。
 *
 * 曾试过「收缩吸入 ☰」「帘幕上卷」等动效，用户最终选了最朴素的方案，
 * 因此这里只保留淡入淡出，不再做位移、缩放、高度收口与按钮脉冲。
 * 尊重 prefers-reduced-motion：直接指令式切换，不播动画。
 */

const FADE_MS = 150;

function reducedMotion(): boolean {
  try {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  } catch {
    return false;
  }
}

function canAnimate(el: HTMLElement | null): el is HTMLElement {
  return !!el && typeof el.animate === "function" && !reducedMotion();
}

async function fade(el: HTMLElement, from: number, to: number): Promise<void> {
  const animation = el.animate([{ opacity: from }, { opacity: to }], { duration: FADE_MS, easing: "ease-out" });
  await animation.finished.catch(() => undefined);
  animation.cancel();
}

/** 隐藏面板：淡出。调用方负责动画结束后卸载面板。 */
export async function collapseBand(el: HTMLElement | null): Promise<void> {
  if (!canAnimate(el)) return;
  await fade(el, 1, 0);
}

/** 显示面板：淡入。 */
export async function expandBand(el: HTMLElement | null): Promise<void> {
  if (!canAnimate(el)) return;
  await fade(el, 0, 1);
}

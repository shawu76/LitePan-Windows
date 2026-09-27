import { BADGE_SVG_MAP } from "./badgeIcons";
import { HAND_SVG_MAP } from "./handIcons";
import { FA_SVG_MAP } from "./faIcons";

// badge-* 为彩色徽章，hand-* 为手绘图标，其余为 Font Awesome 图标。
const FALLBACK_SVG = HAND_SVG_MAP["hand-file"];

export function getSvg(name: string): string {
  const svg = BADGE_SVG_MAP[name] ?? HAND_SVG_MAP[name] ?? FA_SVG_MAP[name];
  if (svg) return svg;
  if (import.meta.env.DEV) {
    console.warn(`[icons] 未注册的图标名 "${name}"，已回退为手绘文件图标`);
  }
  return FALLBACK_SVG;
}

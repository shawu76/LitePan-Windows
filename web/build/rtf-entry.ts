/**
 * rtf.js 的入口替身：导出形状与包内 index.js 完全一致，
 * 但指向包内自带的 TypeScript 源码（src/），以便构建时用 build/codepage-shim.ts
 * 替掉预打包产物里内联的 769 KB codepage 表。详见 build/rtf-slim.ts。
 */
import * as RTFJS from "rtf.js/src/rtfjs/index";
import * as EMFJS from "rtf.js/src/emfjs/index";
import * as WMFJS from "rtf.js/src/wmfjs/index";

export { RTFJS, EMFJS, WMFJS };

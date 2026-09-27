import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath, URL } from "node:url";
import type { Plugin } from "vite";

const RTF_SRC = fileURLToPath(new URL("../node_modules/rtf.js/src/", import.meta.url));
const SHIM = fileURLToPath(new URL("./codepage-shim.ts", import.meta.url));
const LEGACY_TABLE_MARKER = '"__RTF_LEGACY_CODEPAGES__"';

function legacyCodepageTables(): string {
  const cptable = createRequire(import.meta.url)("codepage") as Record<string, { dec?: (string | undefined)[] }>;
  const tables = Object.fromEntries(Object.entries(cptable)
    .filter(([codepage, table]) => /^\d+$/.test(codepage) && table.dec?.length === 0x100)
    .map(([codepage, table]) => [codepage, table.dec!.slice(0x80).map((char) => char ?? "\uFFFD")]));
  return JSON.stringify(tables);
}

/**
 * 上游 rtf.js 的预打包产物 dist/RTFJS.bundle.js 里内联了整张 codepage 表
 * （249,242 个 U+FFFD，gzip 后 769 KB），而 rtf.js 自己只用到一个查表接口。
 * 改用 npm 包内自带的 rtf.js/src（10,144 行 TS）重新打包，即可把 codepage
 * 换成 TextDecoder 垫片：RTF 预览分块 gzip 从 819 KB 降到 34 KB。
 *
 * 本插件负责三件事：
 *   1. 把裸模块 codepage 指向垫片；
 *   2. 把 rtf.js/src 里裸写的 EMFJS / WMFJS（上游是 webpack externals）指向源码入口；
 *   3. 给上游源码补 `type` 修饰符——它自己的 ts-loader 不报错，rolldown 会把
 *      `import { ISettings }` 当成值导入而构建失败。
 */
export function rtfjsSource(): Plugin {
  return {
    name: "rtfjs-source",
    enforce: "pre",
    configResolved() {
      if (!existsSync(RTF_SRC)) {
        throw new Error(`未找到 ${RTF_SRC}，rtf.js 版本可能已不再随包发布 src 目录`);
      }
    },
    resolveId(source) {
      if (source === "codepage") return SHIM;
      if (source === "EMFJS") return `${RTF_SRC}emfjs/index.ts`;
      if (source === "WMFJS") return `${RTF_SRC}wmfjs/index.ts`;
      return null;
    },
    load(id) {
      if (id === SHIM) {
        return readFileSync(id, "utf8").replace(LEGACY_TABLE_MARKER, legacyCodepageTables());
      }
      if (!id.startsWith(RTF_SRC) || !id.endsWith(".ts")) return null;
      const code = readFileSync(id, "utf8");
      const patched = code.replace(
        /\b(import|export)\s*\{([^}]*)\}/g,
        (_match, keyword: string, body: string) =>
          `${keyword} {${body.replace(
            /(^|,)(\s*)(I[A-Z][A-Za-z0-9_]*)/g,
            (_specifier, separator: string, space: string, name: string) =>
              `${separator}${space}type ${name}`,
          )}}`,
      );
      return patched === code ? null : patched;
    },
  };
}

/**
 * rtf.js 对 codepage 的唯一用法是 `cptable[cp].dec[code]` 查表（parser/Parser.ts 两处）。
 *
 * 上游把整张 cptable（150 个代码页，约 2 MB 源码 / gzip 后 769 KB）内联进了预打包产物。
 * 这里用 TextDecoder 处理主流编码，并在构建时注入紧凑的单字节编码表，兼顾体积与老 RTF。
 *
 * dec 的下标语义与 codepage 保持一致：
 *   - 单字节代码页：下标 = 字节值（0x00-0xFF）
 *   - 双字节代码页：下标 = (首字节 << 8) | 尾字节，0x100 以上未映射位保持 undefined
 *     —— rtf.js 依赖 `dec[code] !== undefined` 判断双字节组合是否有效，不能填占位符
 *   - 0x00-0xFF 段未映射的位置填 U+FFFD（与 codepage 对 windows-125x 的处理一致，
 *     避免 rtf.js 把 undefined 拼成字面量 "undefined" 输出到页面上）
 *
 * 映射不到 TextDecoder 的代码页回退到 windows-1252（上游此时取到 undefined 会直接报错）。
 */

const LABELS: Record<number, string> = {
  866: "ibm866",
  874: "windows-874",
  932: "shift_jis",
  936: "gbk",
  949: "euc-kr",
  950: "big5",
  1250: "windows-1250",
  1251: "windows-1251",
  1252: "windows-1252",
  1253: "windows-1253",
  1254: "windows-1254",
  1255: "windows-1255",
  1256: "windows-1256",
  1257: "windows-1257",
  1258: "windows-1258",
  20866: "koi8-r",
  21866: "koi8-u",
  10000: "macintosh",
  10007: "x-mac-cyrillic",
  20932: "euc-jp",
  51932: "euc-jp",
  51949: "euc-kr",
  54936: "gb18030",
  28591: "iso-8859-1",
  28592: "iso-8859-2",
  28593: "iso-8859-3",
  28594: "iso-8859-4",
  28595: "iso-8859-5",
  28596: "iso-8859-6",
  28597: "iso-8859-7",
  28598: "iso-8859-8",
  28599: "iso-8859-9",
  28603: "iso-8859-13",
  28604: "iso-8859-14",
  28605: "iso-8859-15",
  28606: "iso-8859-16",
};

/** 需要按双字节查表的代码页。 */
const DOUBLE_BYTE = new Set([932, 936, 949, 950, 20932, 51932, 51949, 54936]);

const FALLBACK = 1252;
const REPLACEMENT = "\uFFFD";
// 构建时由 rtf-slim.ts 注入全部单字节编码的后 128 位，避免携带原库的大型稀疏数组。
const LEGACY_HIGH_HALF = "__RTF_LEGACY_CODEPAGES__" as unknown as Record<number, string[]>;

interface DecTable {
  dec: (string | undefined)[];
}

const cache = new Map<number, DecTable>();

function buildTable(codepage: number): DecTable {
  const legacy = LEGACY_HIGH_HALF[codepage];
  if (legacy !== undefined) {
    const dec = Array.from({ length: 0x80 }, (_, byte) => String.fromCharCode(byte));
    dec.push(...legacy);
    return { dec };
  }
  const doubleByte = DOUBLE_BYTE.has(codepage);
  const decoder = new TextDecoder(LABELS[codepage] ?? LABELS[FALLBACK]);
  const dec: (string | undefined)[] = new Array(doubleByte ? 0x10000 : 0x100).fill(undefined);

  // 单字节段：解码结果与 codepage 的 windows-125x 表一致，未定义/C1 控制符一律记 U+FFFD
  for (let byte = 0; byte < 0x100; byte++) {
    const char = decoder.decode(Uint8Array.of(byte));
    dec[byte] = char.length === 1 && char !== REPLACEMENT && (char < "\u0080" || char > "\u009F")
      ? char
      : REPLACEMENT;
  }

  if (doubleByte) {
    for (let lead = 0x81; lead < 0xff; lead++) {
      for (let trail = 0x40; trail < 0xff; trail++) {
        if (trail === 0x7f) continue;
        const char = decoder.decode(Uint8Array.of(lead, trail));
        if (char.length === 1 && char !== REPLACEMENT) dec[(lead << 8) | trail] = char;
      }
    }
  }

  return { dec };
}

const cptable = new Proxy({} as Record<number, DecTable>, {
  get(_target, key) {
    const codepage = typeof key === "string" ? Number(key) : Number.NaN;
    if (!Number.isFinite(codepage)) return undefined;
    let table = cache.get(codepage);
    if (table === undefined) {
      table = buildTable(codepage);
      cache.set(codepage, table);
    }
    return table;
  },
});

export default cptable;

import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { rtfjsSource } from "./build/rtf-slim.ts";

// 构建产物写入 Go 内嵌目录，开发态将 /api 代理到后端。
export default defineConfig({
  plugins: [
    vue({
      template: {
        compilerOptions: {
          isCustomElement: (tag) => tag.startsWith("media-"),
        },
      },
    }),
    // rtf.js 改走包内源码重新打包，去掉预打包产物里内联的 769 KB codepage 表。
    rtfjsSource(),
  ],
  resolve: {
    alias: [
      { find: "@", replacement: fileURLToPath(new URL("./src", import.meta.url)) },
      {
        find: /^rtf\.js$/,
        replacement: fileURLToPath(new URL("./build/rtf-entry.ts", import.meta.url)),
      },
    ],
  },
  optimizeDeps: {
    // rtf.js 及其源码必须走普通源码管线，否则 Vite 预打包阶段不走 rtfjsSource 插件，
    // 上游源码里缺 type 修饰符的接口导出会直接构建失败。
    exclude: ["rtf.js", "codepage"],
  },
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.LITEPAN_API_PROXY || "http://127.0.0.1:5211",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "../internal/api/web",
    emptyOutDir: true,
    // 解码器按需分包，阈值略高于当前最大独立产物。
    chunkSizeWarningLimit: 3200,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules/three')) {
            return 'three-vendor';
          }
          if (id.includes('node_modules/vue') || id.includes('node_modules/pinia') || id.includes('node_modules/vue-router')) {
            return 'vue-vendor';
          }
        }
      }
    }
  },
});

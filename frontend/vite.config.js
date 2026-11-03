import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 可编辑 Vue 界面单独输出，避免覆盖正式界面的 frontend/dist。
export default defineConfig({
  plugins: [vue()],
  base: './',
  build: {
    outDir: 'editable-dist',
    emptyOutDir: true,
    target: 'chrome105', // WebView2 基线，等于 Edge 105 内核
    assetsInlineLimit: 0, // 资源一律落盘，避免 base64 撑大 JS
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        // 把体积大的可选依赖单独切出，避免主 chunk 过大影响首屏
        manualChunks(id) {
          if (id.includes('node_modules/hls.js')) return 'hls'
          if (id.includes('node_modules/vue') || id.includes('node_modules/@vue')) return 'vue'
          if (id.includes('node_modules/pinia')) return 'pinia'
          return undefined
        },
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    // 浏览器调试（非 Wails 环境）时 bridge.js 会回落 mock，无需代理上游
  },
  preview: {
    port: 4173,
  },
})

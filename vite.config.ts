import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  build: {
    // three 及其 loader 是单个按需 chunk（~562 kB），已经和资产库首屏（~142 kB）分离。
    // 它无法再切得更有意义，把阈值放到它的量级，避免每次构建都报一条已知的噪音警告。
    chunkSizeWarningLimit: 600,
  },
  server: {
    // 开发时前端跑 Vite，API 交给本机的 Go 服务（默认 :8080）
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})

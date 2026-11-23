import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  server: {
    // 前端独立调试（npm run dev）时把 /api 代理到本机 Web 模式后端：
    // 先启动 `go run . --web --port 8989`，再访问 vite 的开发地址
    proxy: {
      '/api': 'http://127.0.0.1:8989',
    },
  },
})

/**
 * 应用入口。
 *
 * 装配顺序有讲究：先建 pinia（store 初始化依赖它），再挂 router
 * （router 守卫会读 account store 的登录态）。
 */
import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import router, { applyStartPage, defaultRoute } from './router'
import { IS_MOCK, ReportError } from './api/bridge'

import './assets/theme.css'
import './assets/views.css'

const app = createApp(App)
const pinia = createPinia()

app.use(pinia)
app.use(router)

// 全局错误兜底：把 Vue 层的异常回传后端，便于排查
app.config.errorHandler = (err, _instance, info) => {
  if (IS_MOCK) {
    console.error('[vue error]', info, err)
    return
  }
  ReportError('vue', String(err && err.message ? err.message : err), String(info)).catch(() => {})
}

// 未捕获的 Promise 拒绝也上报一次
window.addEventListener('unhandledrejection', (e) => {
  const reason = e.reason
  const msg = reason && reason.message ? reason.message : String(reason)
  // 超时不值得上报，避免噪声
  if (/请求超时/.test(msg)) return
  if (IS_MOCK) {
    console.error('[unhandled]', reason)
    return
  }
  ReportError('promise', msg, '').catch(() => {})
})

app.mount('#app')

// 启动页：localStorage 里已有就直接跳（首帧即生效），
// 没有则保持默认 /recommend。设置页保存时会同时写这里与后端 prefs。
router.isReady()
  .then(() => {
    const want = applyStartPage(localStorage.getItem('hg.startPage') || '')
    // 只在还停在默认推荐页时才跳，避免打断用户已开始的导航。
    if (router.currentRoute.value.path === '/recommend' && want !== '/recommend') {
      router.replace(defaultRoute())
    }
  })
  .catch(() => {
    /* 拿不到就按默认页走 */
  })

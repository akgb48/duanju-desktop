<script setup>
/**
 * 应用外壳。
 *
 * 布局：
 *   ┌──────────────────────────────────────┐
 *   │ TitleBar（无边框窗口的自定义标题栏）  │
 *   ├────────┬─────────────────────────────┤
 *   │Sidebar │ <router-view>               │
 *   └────────┴─────────────────────────────┘
 *   播放页与登录页走 chromeless，不显示侧栏。
 *
 * 同时负责全局 Toast 与确认框的渲染，以及后端事件的顶层订阅
 * （auth:expired / update:state / notice:new 这类与具体页面无关的事件）。
 */
import { computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TitleBar from './components/TitleBar.vue'
import Sidebar from './components/Sidebar.vue'
import { useUiStore } from './stores/ui'
import { useAccountStore } from './stores/account'
import { EVENTS, on, off, IS_MOCK } from './api/bridge'

const ui = useUiStore()
const account = useAccountStore()
const route = useRoute()
const router = useRouter()

/** 播放页使用无侧栏布局。 */
const chromeless = computed(() => !!(route.meta && (route.meta.bare || route.meta.chromeless)))

/** 全局事件取消函数集合。 */
const unsubs = []

onMounted(async () => {
  ui.init()

  // 顶层事件：与具体页面无关的那些
  unsubs.push(
    on(EVENTS.AUTH_EXPIRED, () => {
      account.loggedIn = false
      account.user = null
      ui.toastWarn('登录已失效，请重新登录', 4000)
      router.push({ name: 'login', query: { redirect: route.fullPath } })
    }),
  )

  unsubs.push(
    on(EVENTS.AUTH_MFA, (payload) => {
      const p = payload || {}
      ui.toast(`需要二次验证：${p.content || p.sendTo || ''}`, 'warn', 6000)
    }),
  )

  unsubs.push(
    on(EVENTS.NOTICE_NEW, () => {
      // 公告的具体展示交给 Notices 相关页面；这里只提示有新的
      ui.toast('收到新公告', 'info', 3000)
    }),
  )

  unsubs.push(
    on(EVENTS.DEPS_HEVC, (payload) => {
      const p = payload || {}
      if (p.state === 'failed' || p.status === 'failed' || p.error) {
        ui.toastErr('硬解组件安装失败，已回落软解', 6000)
      } else if (p.state === 'installed' || p.status === 'installed') {
        ui.toastOk('硬解组件已就绪', 4000)
      } else if (p.state === 'progress' || p.progress !== undefined) {
        // 下载进度不弹 toast，避免刷屏
      }
    }),
  )

  unsubs.push(
    on(EVENTS.PLAY_SPARE, () => {
      ui.toast('本机设备取不到播放信息，已自动换用备用设备', 'warn', 4500)
    }),
  )

  unsubs.push(
    on(EVENTS.RISK_VERIFY, () => {
      ui.toastErr('触发了风控验证，请稍后在手机端完成验证', 5000)
    }),
  )

  unsubs.push(
    on(EVENTS.WINDOW_MINIMISE, () => {
      // 播放页自己处理暂停；外壳层只更新 UI 状态
    }),
  )

  // 首次拉登录态
  await account.refresh()
})

onBeforeUnmount(() => {
  for (const u of unsubs) {
    try {
      if (typeof u === 'function') u()
    } catch {
      /* 忽略 */
    }
  }
  unsubs.length = 0
})
</script>

<template>
  <div class="shell" :class="{ 'is-chromeless': chromeless }">
    <TitleBar />

    <div class="shell-main">
      <Sidebar v-if="!chromeless" />

      <main class="shell-view">
        <router-view v-slot="{ Component }">
          <transition name="fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
    </div>

    <!-- 全局提示 -->
    <div class="toast-stack" role="status" aria-live="polite">
      <transition-group name="toast">
        <div
          v-for="t in ui.toasts"
          :key="t.id"
          class="toast"
          :class="`toast-${t.type}`"
          @click="ui.dismiss(t.id)"
        >
          <span class="toast-dot" />
          <span class="toast-text">{{ t.text }}</span>
        </div>
      </transition-group>
    </div>

    <!-- 确认框 -->
    <transition name="fade">
      <div v-if="ui.modal" class="modal-mask" @click.self="ui.settleModal(false)">
        <div class="modal">
          <h3 class="modal-title">{{ ui.modal.title }}</h3>
          <p v-if="ui.modal.text" class="modal-text">{{ ui.modal.text }}</p>
          <div class="modal-actions">
            <button class="btn" @click="ui.settleModal(false)">
              {{ ui.modal.cancelText }}
            </button>
            <button
              class="btn"
              :class="ui.modal.danger ? 'btn-danger' : 'btn-primary'"
              @click="ui.settleModal(true)"
            >
              {{ ui.modal.confirmText }}
            </button>
          </div>
        </div>
      </div>
    </transition>

    <!-- 浏览器调试角标 -->
    <div v-if="IS_MOCK" class="mock-badge" title="当前无 Wails 后端，数据来自本地 mock">
      调试模式 · mock
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--c-bg);
  overflow: hidden;
}

.shell-main {
  flex: 1 1 auto;
  display: flex;
  min-height: 0;
}

.shell-view {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  position: relative;
  overflow: hidden;
}

/* ---- Toast ---- */
.toast-stack {
  position: fixed;
  right: var(--s-5);
  bottom: var(--s-5);
  z-index: var(--z-toast);
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
  pointer-events: none;
  max-width: 380px;
}
.toast {
  display: flex;
  align-items: flex-start;
  gap: var(--s-2);
  padding: var(--s-3) var(--s-4);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line);
  border-radius: var(--r-3);
  box-shadow: var(--shadow-3);
  font-size: var(--f-3);
  line-height: 1.5;
  pointer-events: auto;
  cursor: pointer;
}
.toast-dot {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  margin-top: 7px;
  border-radius: 50%;
  background: var(--c-info);
}
.toast-ok .toast-dot {
  background: var(--c-ok);
}
.toast-warn .toast-dot {
  background: var(--c-warn);
}
.toast-error .toast-dot {
  background: var(--c-err);
}
.toast-error {
  border-color: var(--c-err-dim);
}
.toast-text {
  flex: 1 1 auto;
  word-break: break-word;
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity var(--t-base), transform var(--t-base);
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}

/* ---- 模态框 ---- */
.modal-mask {
  position: fixed;
  inset: 0;
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(2px);
}
.modal {
  width: 360px;
  max-width: calc(100vw - 48px);
  padding: var(--s-5);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line);
  border-radius: var(--r-4);
  box-shadow: var(--shadow-3);
}
.modal-title {
  font-size: var(--f-5);
  margin-bottom: var(--s-3);
}
.modal-text {
  font-size: var(--f-3);
  color: var(--c-text-dim);
  line-height: 1.6;
  margin-bottom: var(--s-5);
  white-space: pre-wrap;
}
.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--s-2);
}

/* ---- 调试角标 ---- */
.mock-badge {
  position: fixed;
  left: var(--s-3);
  bottom: var(--s-3);
  z-index: var(--z-sticky);
  padding: 3px 8px;
  font-size: var(--f-1);
  color: var(--c-accent);
  background: var(--c-accent-dim);
  border-radius: var(--r-1);
  pointer-events: none;
}
</style>

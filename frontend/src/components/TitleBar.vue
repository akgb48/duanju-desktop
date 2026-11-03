<script setup>
/**
 * 无边框窗口的自定义标题栏。
 *
 * 三件事：
 *   1. 拖拽区（-webkit-app-region: drag），按钮区要 drag:none
 *   2. 最小化 / 最大化 / 关闭
 *   3. 展示当前页标题与窗口状态
 *
 * Wails 侧走 runtime 的窗口 API（经 bridge 的 Window* 包装），不是 App 绑定。
 */
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { useUiStore } from '../stores/ui'
import {
  IS_MOCK,
  WindowMinimise,
  WindowToggleMaximise,
  WindowQuit,
  WindowIsMaximised,
} from '../api/bridge'

const ui = useUiStore()
const route = useRoute()

const isMax = ref(false)

const title = computed(() => (route.meta && route.meta.title) || '短剧电脑版')

async function minimise() {
  // 播放页需要先暂停，由 window:minimise 事件通知，这里只发指令
  try {
    await WindowMinimise()
  } catch {
    /* 忽略 */
  }
  if (IS_MOCK) ui.toast('已最小化（调试模式下无效）', 'info', 1200)
}

async function toggleMax() {
  try {
    await WindowToggleMaximise()
  } catch {
    /* 忽略 */
  }
  // 回读真实状态，避免和 Wails 侧不同步
  try {
    isMax.value = !!(await WindowIsMaximised())
  } catch {
    isMax.value = !isMax.value
  }
}

async function close() {
  const ok = await ui.confirm({
    title: '关闭应用',
    text: '确定要退出短剧电脑版吗？',
    confirmText: '退出',
    danger: true,
  })
  if (!ok) return
  try {
    await WindowQuit()
  } catch {
    /* 忽略 */
  }
}

/** 双击标题栏切换最大化。 */
function onDblClick() {
  toggleMax()
}

let offMaxWatch = null

onMounted(() => {
  // 初始同步最大化状态
  Promise.resolve(WindowIsMaximised())
    .then((v) => {
      isMax.value = !!v
    })
    .catch(() => {})

  // 窗口尺寸变化时同步最大化状态（拖拽边缘手势）
  const onResize = () => {
    Promise.resolve(WindowIsMaximised())
      .then((v) => {
        isMax.value = !!v
      })
      .catch(() => {})
  }
  window.addEventListener('resize', onResize)
  offMaxWatch = () => window.removeEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  if (offMaxWatch) offMaxWatch()
})
</script>

<template>
  <header class="titlebar" @dblclick="onDblClick">
    <!-- 左侧：品牌 + 当前页名 -->
    <div class="tb-left">
      <img class="tb-logo" src="/app-icon.png" alt="" aria-hidden="true" />
      <span class="tb-title">{{ title }}</span>
    </div>

    <!-- 中间：留空，交给拖拽 -->
    <div class="tb-drag" />

    <!-- 右侧：窗口按钮 -->
    <div v-if="ui.windowButtonsVisible" class="tb-actions">
      <button class="tb-btn" title="最小化" aria-label="最小化" @click.stop="minimise">
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M1 5h8" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
        </svg>
      </button>

      <button
        class="tb-btn"
        :title="isMax ? '还原' : '最大化'"
        :aria-label="isMax ? '还原' : '最大化'"
        @click.stop="toggleMax"
      >
        <svg v-if="!isMax" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
          <rect
            x="1.5"
            y="1.5"
            width="7"
            height="7"
            fill="none"
            stroke="currentColor"
            stroke-width="1.2"
          />
        </svg>
        <svg v-else width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
          <rect
            x="1"
            y="3"
            width="6"
            height="6"
            fill="none"
            stroke="currentColor"
            stroke-width="1.2"
          />
          <path d="M3 3V1h6v6H7" fill="none" stroke="currentColor" stroke-width="1.2" />
        </svg>
      </button>

      <button
        class="tb-btn tb-close"
        title="关闭"
        aria-label="关闭"
        @click.stop="close"
      >
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
          <path
            d="M1.5 1.5l7 7M8.5 1.5l-7 7"
            stroke="currentColor"
            stroke-width="1.2"
            stroke-linecap="round"
          />
        </svg>
      </button>
    </div>
  </header>
</template>

<style scoped>
.titlebar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  height: var(--h-titlebar);
  padding-left: var(--s-3);
  background: var(--c-bg-elev);
  border-bottom: 1px solid var(--c-line-soft);
  z-index: var(--z-titlebar);
  /* 无边框窗口拖拽 */
  -webkit-app-region: drag;
  user-select: none;
}

.tb-left {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  flex: 0 0 auto;
  min-width: 0;
}

.tb-logo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: var(--r-1);
  object-fit: contain;
  color: #fff;
  font-size: var(--f-2);
  font-weight: 700;
  line-height: 1;
}

.tb-title {
  font-size: var(--f-2);
  color: var(--c-text-dim);
  letter-spacing: 0.2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 240px;
}

.tb-drag {
  flex: 1 1 auto;
  height: 100%;
  min-width: var(--s-6);
}

.tb-actions {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
  -webkit-app-region: no-drag;
}

.tb-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  height: var(--h-titlebar);
  color: var(--c-text-dim);
  transition: background var(--t-fast), color var(--t-fast);
}
.tb-btn:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.tb-close:hover {
  background: var(--c-err);
  color: #fff;
}
</style>

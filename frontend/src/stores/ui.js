import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

/**
 * 界面状态：主题、侧栏、全局提示、模态框。
 *
 * 主题写在 <html data-theme>，CSS 变量在 theme.css 里按该属性切换。
 */
export const useUiStore = defineStore('ui', () => {
  const THEME_KEY = 'hg:ui:theme'
  const SIDEBAR_KEY = 'hg:ui:sidebar'

  /** 主题：dark / light。 */
  const theme = ref(readLocal(THEME_KEY, 'dark'))
  /** 侧栏是否收起（只留图标）。 */
  const sidebarMini = ref(readLocal(SIDEBAR_KEY, false) === true)
  /** 提示队列：[{id, text, type, timeout}]。 */
  const toasts = ref([])
  /** 当前模态框：null 或 {title, text, confirmText, cancelText, resolve}。 */
  const modal = ref(null)
  /** 窗口是否最大化（TitleBar 用）。 */
  const maximised = ref(false)
  /** 是否显示窗口按钮（后端可隐藏，配合迷你窗）。 */
  const windowButtonsVisible = ref(true)

  let toastSeq = 0
  let toastTimers = new Map()

  function readLocal(key, fallback) {
    try {
      const raw = localStorage.getItem(key)
      if (raw === null) return fallback
      return JSON.parse(raw)
    } catch {
      return fallback
    }
  }

  function writeLocal(key, v) {
    try {
      localStorage.setItem(key, JSON.stringify(v))
    } catch {
      /* 忽略 */
    }
  }

  /** 应用主题到 DOM。 */
  function applyTheme(t) {
    theme.value = t === 'light' ? 'light' : 'dark'
    document.documentElement.setAttribute('data-theme', theme.value)
    writeLocal(THEME_KEY, theme.value)
  }

  /** 切换主题。 */
  function toggleTheme() {
    applyTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  /** 切换侧栏。 */
  function toggleSidebar() {
    sidebarMini.value = !sidebarMini.value
    writeLocal(SIDEBAR_KEY, sidebarMini.value)
  }

  const sidebarWidth = computed(() => (sidebarMini.value ? 64 : 200))

  /**
   * 弹提示。
   * @param {string} text
   * @param {'info'|'ok'|'warn'|'error'} type
   * @param {number} ms 显示时长
   */
  function toast(text, type = 'info', ms = 2600) {
    const id = ++toastSeq
    toasts.value = [...toasts.value, { id, text, type }]
    const timer = setTimeout(() => dismiss(id), ms)
    toastTimers.set(id, timer)
    // 最多同时 4 条，超出丢最旧的
    if (toasts.value.length > 4) {
      const oldest = toasts.value[0]
      dismiss(oldest.id)
    }
    return id
  }

  function dismiss(id) {
    const t = toastTimers.get(id)
    if (t) {
      clearTimeout(t)
      toastTimers.delete(id)
    }
    toasts.value = toasts.value.filter((x) => x.id !== id)
  }

  /** 便捷包装。 */
  const toastOk = (t, ms) => toast(t, 'ok', ms)
  const toastErr = (t, ms) => toast(t, 'error', ms || 4000)
  const toastWarn = (t, ms) => toast(t, 'warn', ms)

  /**
   * 确认对话框，返回 Promise<boolean>。
   * 不引入组件库，自己在 App.vue 里渲染。
   */
  function confirm(opts) {
    const o = typeof opts === 'string' ? { text: opts } : opts || {}
    return new Promise((resolve) => {
      modal.value = {
        title: o.title || '请确认',
        text: o.text || '',
        confirmText: o.confirmText || '确定',
        cancelText: o.cancelText || '取消',
        danger: !!o.danger,
        resolve,
      }
    })
  }

  /** 模态框回执。 */
  function settleModal(ok) {
    const m = modal.value
    modal.value = null
    if (m && typeof m.resolve === 'function') m.resolve(ok)
  }

  /** 从后端事件同步窗口按钮可见性。 */
  function setWindowButtonsVisible(v) {
    windowButtonsVisible.value = !!v
  }

  /** 初始化：把持久化的主题套上去。 */
  function init() {
    applyTheme(theme.value)
  }

  return {
    theme,
    sidebarMini,
    sidebarWidth,
    toasts,
    modal,
    maximised,
    windowButtonsVisible,
    init,
    applyTheme,
    toggleTheme,
    toggleSidebar,
    toast,
    toastOk,
    toastErr,
    toastWarn,
    dismiss,
    confirm,
    settleModal,
    setWindowButtonsVisible,
  }
})

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api/bridge'
import { BridgeError } from '../api/bridge'

/**
 * 账号与登录态。
 *
 * 后端是唯一真相来源：本地不长期缓存 token，只在内存里保存
 * Status() 的快照，页面刷新后重新问一次后端。
 */
export const useAccountStore = defineStore('account', () => {
  /** 是否已登录。 */
  const loggedIn = ref(false)
  /** 当前用户资料（model.User）。 */
  const user = ref(null)
  /** 设备上下文快照。 */
  const device = ref(null)
  /** 可选登录区域。 */
  const regions = ref([])
  /** 当前是否有请求在跑。 */
  const loading = ref(false)
  /** 最近一次错误信息。 */
  const error = ref('')

  /** 昵称，未登录时给个占位。 */
  const nickname = computed(() => (user.value && user.value.nickname) || '未登录')
  /** 头像 URL。 */
  const avatar = computed(() => (user.value && user.value.avatar) || '')
  /** 是否 VIP。 */
  const isVip = computed(() => !!(user.value && user.value.isVip))

  /** 把后端返回的 Status 应用到 store。 */
  function applyStatus(s) {
    if (!s || typeof s !== 'object') {
      loggedIn.value = false
      user.value = null
      return
    }
    loggedIn.value = !!s.loggedIn
    user.value = s.user || s.User || null
    device.value = s.device || s.Device || null
  }

  /** 从后端拉一次登录态。 */
  async function refresh() {
    try {
      const s = await api.Status()
      applyStatus(s)
      return s
    } catch (e) {
      // 后端未就绪时不要把界面打红，静默降级为未登录
      loggedIn.value = false
      return null
    }
  }

  /** 拉取可选登录区域。 */
  async function loadRegions() {
    try {
      const r = await api.LoginRegions()
      regions.value = Array.isArray(r) ? r : []
    } catch {
      regions.value = []
    }
    return regions.value
  }

  /**
   * 手机号 + 验证码登录。
   * @returns {Promise<{ok:boolean, error?:string, mfa?:boolean}>}
   */
  async function login(phone, code) {
    loading.value = true
    error.value = ''
    try {
      const u = await api.Login(phone, code)
      if (u) {
        user.value = u
        loggedIn.value = true
      } else {
        // 后端登录成功但未带用户信息，补一次
        await refresh()
      }
      return { ok: true }
    } catch (e) {
      const be = e instanceof BridgeError ? e : new BridgeError(-1, String(e))
      error.value = be.message
      // 二次验证：后端会经 auth:mfa 事件推送，登录调用本身返回该错误码
      if (/二次验证|mfa/i.test(be.message) || be.code === 10010) {
        return { ok: false, mfa: true, error: be.message }
      }
      return { ok: false, error: be.message }
    } finally {
      loading.value = false
    }
  }

  /** 发送短信验证码。 */
  async function sendCode(phone) {
    try {
      await api.SendCode(phone)
      return { ok: true }
    } catch (e) {
      return { ok: false, error: e.message || '发送失败' }
    }
  }

  /** 退出登录。 */
  async function logout() {
    try {
      await api.Logout()
    } catch {
      // 即便后端失败也要清本地态
    }
    loggedIn.value = false
    user.value = null
  }

  /** 扫码登录：开始。 */
  async function qrStart(region) {
    try {
      const r = await api.QRLoginStart(region || '')
      return { ok: true, session: r }
    } catch (e) {
      return { ok: false, error: e.message || '生成二维码失败' }
    }
  }

  /** 扫码登录：轮询。 */
  async function qrPoll() {
    try {
      const r = await api.QRLoginPoll()
      // 状态机：waiting / scanned / confirmed / expired / canceled
      if (r && (r.state === 'confirmed' || r.confirmed === true)) {
        await refresh()
      }
      return { ok: true, session: r }
    } catch (e) {
      return { ok: false, error: e.message || '轮询失败' }
    }
  }

  /** 扫码登录：取消。 */
  async function qrCancel() {
    try {
      await api.QRLoginCancel()
    } catch {
      /* 忽略 */
    }
  }

  /** 需要登录的操作前调用，返回是否可继续。 */
  function ensureLogin() {
    return loggedIn.value
  }

  return {
    loggedIn,
    user,
    device,
    regions,
    loading,
    error,
    nickname,
    avatar,
    isVip,
    refresh,
    loadRegions,
    login,
    sendCode,
    logout,
    qrStart,
    qrPoll,
    qrCancel,
    ensureLogin,
  }
})

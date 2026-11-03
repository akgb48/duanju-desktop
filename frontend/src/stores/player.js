import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api/bridge'

/**
 * 播放状态。
 *
 * 职责：
 *   - 保存当前系列的播放信息、清晰度列表
 *   - 记录播放进度并节流上报（SaveProgress）
 *   - 记住播放偏好（音量、倍速、静音、清晰度）
 *
 * 注意：<video> 元素本身由 Play.vue 持有，store 只管状态，
 * 因为元素生命周期跟随路由，放进 store 会造成泄漏。
 */
export const usePlayerStore = defineStore('player', () => {
  /** 当前播放的系列 id。 */
  const seriesId = ref('')
  /** 当前集号（1 基）。 */
  const episode = ref(1)
  /** 播放信息（model.VideoDetail）。 */
  const detail = ref(null)
  /** 当前选中的清晰度。 */
  const quality = ref('720p')
  /** 是否正在加载播放地址。 */
  const loading = ref(false)
  /** 错误信息。 */
  const error = ref('')

  /** 播放偏好，持久化到 localStorage（纯前端状态）。 */
  const prefs = ref(loadPrefs())

  /** 上次上报的时间点，用于节流。 */
  let lastReportAt = 0
  /** 已上报过的 positionMs，避免重复。 */
  let lastReportedMs = -1

  /** 可选清晰度列表。 */
  const sources = computed(() => {
    const d = detail.value
    if (!d || !Array.isArray(d.sources)) return []
    return d.sources
  })

  /** 当前清晰度对应的地址。 */
  const currentSource = computed(() => {
    const list = sources.value
    if (!list.length) return null
    const hit = list.find((s) => s.quality === quality.value)
    return hit || list[0]
  })

  /** 播放地址。 */
  const src = computed(() => (currentSource.value ? currentSource.value.url : ''))

  /** 总集数（如果后端给了）。 */
  const totalEpisodes = computed(() => {
    const d = detail.value
    return (d && (d.episodeCount || d.EpisodeCnt)) || 0
  })

  function loadPrefs() {
    const def = { volume: 0.8, muted: false, rate: 1, autoNext: true, danmaku: true, opacity: 80 }
    try {
      const raw = localStorage.getItem('hg:player:prefs')
      if (!raw) return def
      const obj = JSON.parse(raw)
      return { ...def, ...obj }
    } catch {
      return def
    }
  }

  function savePrefs() {
    try {
      localStorage.setItem('hg:player:prefs', JSON.stringify(prefs.value))
    } catch {
      /* 忽略：隐私模式或配额满 */
    }
    // 同步给后端，让下载/预取用同一套偏好
    api.SetPlaybackPrefs(JSON.stringify(prefs.value)).catch(() => {})
  }

  /** 更新单个偏好字段。 */
  function setPref(key, value) {
    prefs.value = { ...prefs.value, [key]: value }
    savePrefs()
  }

  /**
   * 加载播放信息。
   * @param {string} sid    系列 id
   * @param {number} ep     集号
   */
  async function load(sid, ep) {
    seriesId.value = sid
    episode.value = Number(ep) || 1
    loading.value = true
    error.value = ''
    try {
      const d = await api.PlayInfo(sid, episode.value)
      detail.value = d || null
      // 选择清晰度：优先用户偏好，其次列表中存在的最高档
      const list = (d && d.sources) || []
      if (list.length) {
        const preferred = list.find((s) => s.quality === prefs.value.defaultQuality)
        const fallback = list[list.length - 1]
        quality.value = (preferred || fallback).quality
      }
      // 预取下一集，提升连播体验
      if (prefs.value.autoNext) {
        api.PrefetchPlay(sid).catch(() => {})
      }
      return { ok: true, detail: d }
    } catch (e) {
      error.value = e.message || '取播放地址失败'
      return { ok: false, error: error.value }
    } finally {
      loading.value = false
    }
  }

  /** 切换清晰度。 */
  function setQuality(q) {
    quality.value = q
  }

  /**
   * 上报进度（自带节流）。
   *
   * 节流策略：位置变化超过 5 秒或距上次上报超过 15 秒才发一次，
   * 播放结束（done）时强制上报。
   */
  function report(positionMs, durationMs, force = false) {
    const now = Date.now()
    const pos = Math.max(0, Math.round(positionMs || 0))
    const dur = Math.max(0, Math.round(durationMs || 0))
    const done = dur > 0 && pos >= dur - 2000

    if (!force && !done) {
      if (Math.abs(pos - lastReportedMs) < 5000 && now - lastReportAt < 15000) return
    }
    lastReportAt = now
    lastReportedMs = pos

    // 失败不打扰用户，本地还有 LocalPending 兜底
    api
      .SaveProgress(seriesId.value, episode.value, pos, dur, done)
      .catch((e) => {
        if (import.meta.env.DEV) console.warn('[progress] 上报失败', e.message)
      })
  }

  /** 切集。 */
  function setEpisode(ep) {
    episode.value = Number(ep) || 1
    lastReportedMs = -1
    lastReportAt = 0
  }

  /** 清空当前播放态（离开播放页时调用）。 */
  function reset() {
    detail.value = null
    error.value = ''
    loading.value = false
    lastReportedMs = -1
    lastReportAt = 0
  }

  return {
    seriesId,
    episode,
    detail,
    quality,
    loading,
    error,
    prefs,
    sources,
    currentSource,
    src,
    totalEpisodes,
    setPref,
    load,
    setQuality,
    report,
    setEpisode,
    reset,
  }
})

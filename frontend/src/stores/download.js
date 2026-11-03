import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api/bridge'
import { EVENTS, on, off } from '../api/bridge'

/**
 * 下载队列。
 *
 * 后端有两类事件会推动这里更新：
 *   - download:list —— 任务集合发生变化（增删、全部暂停/继续），需要全量重拉
 *   - download:task —— 单个任务进度变化，按 videoId 增量更新
 *
 * 全量刷新做 200 ms 去抖，避免批量添加时把后端打爆。
 */
export const useDownloadStore = defineStore('download', () => {
  /** 任务列表（model 侧的 DownloadTask）。 */
  const tasks = ref([])
  /** 是否正在加载。 */
  const loading = ref(false)
  /** 错误信息。 */
  const error = ref('')
  /** 是否已订阅事件。 */
  let subscribed = false
  /** 去抖计时器。 */
  let debounceTimer = null

  /** 进行中。 */
  const active = computed(() =>
    tasks.value.filter((t) => t.state === 'downloading' || t.state === 'pending'),
  )
  /** 已暂停。 */
  const paused = computed(() => tasks.value.filter((t) => t.state === 'paused'))
  /** 已完成。 */
  const done = computed(() => tasks.value.filter((t) => t.state === 'done'))
  /** 失败。 */
  const failed = computed(() => tasks.value.filter((t) => t.state === 'error'))

  /** 总进度（0–1），空队列返回 0。 */
  const overall = computed(() => {
    const list = tasks.value
    if (!list.length) return 0
    let got = 0
    let total = 0
    for (const t of list) {
      got += Number(t.receivedBytes || t.received || 0)
      total += Number(t.totalBytes || t.total || 0)
    }
    return total > 0 ? Math.min(1, got / total) : 0
  })

  /** 全量拉取。 */
  async function refresh() {
    loading.value = true
    error.value = ''
    try {
      const r = await api.Downloads()
      tasks.value = Array.isArray(r) ? r : (r && r.items) || []
      return tasks.value
    } catch (e) {
      error.value = e.message || '拉取下载列表失败'
      return []
    } finally {
      loading.value = false
    }
  }

  /** 去抖全量刷新。 */
  function scheduleRefresh() {
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => {
      debounceTimer = null
      refresh()
    }, 200)
  }

  /** 增量应用单条任务进度。 */
  function applyTask(t) {
    if (!t) return
    const key = t.videoId || t.id
    if (!key) return
    const i = tasks.value.findIndex((x) => (x.videoId || x.id) === key)
    if (i >= 0) {
      tasks.value[i] = { ...tasks.value[i], ...t }
    } else {
      // 新任务：整表拉一次，保证字段完整
      scheduleRefresh()
    }
  }

  /** 添加下载。 */
  async function add(seriesId, from, to) {
    try {
      await api.AddDownloads(seriesId, Number(from) || 1, Number(to) || 0)
      await refresh()
      return { ok: true }
    } catch (e) {
      return { ok: false, error: e.message || '添加下载失败' }
    }
  }

  /** 整剧下载。 */
  async function addSeries(seriesId) {
    try {
      await api.DownloadSeries(seriesId)
      await refresh()
      return { ok: true }
    } catch (e) {
      return { ok: false, error: e.message || '整剧下载失败' }
    }
  }

  /** 删除任务（可批量）。 */
  async function remove(ids) {
    const list = Array.isArray(ids) ? ids : [ids]
    try {
      await api.DeleteDownloads(list)
      await refresh()
      return { ok: true }
    } catch (e) {
      return { ok: false, error: e.message || '删除失败' }
    }
  }

  /** 暂停。 */
  async function pause(ids) {
    const list = Array.isArray(ids) ? ids : [ids]
    try {
      await api.PauseDownloads(list)
      await refresh()
      return { ok: true }
    } catch (e) {
      return { ok: false, error: e.message || '暂停失败' }
    }
  }

  /** 继续。 */
  async function resume(ids) {
    const list = Array.isArray(ids) ? ids : [ids]
    try {
      await api.ResumeDownloads(list)
      await refresh()
      return { ok: true }
    } catch (e) {
      return { ok: false, error: e.message || '继续失败' }
    }
  }

  /** 打开下载目录。 */
  async function openDir(id) {
    try {
      await api.OpenDownloadDir(id || '')
    } catch {
      /* 后端会给出提示 */
    }
  }

  /** 订阅后端下载事件。 */
  function subscribe() {
    if (subscribed) return
    subscribed = true
    on(EVENTS.DOWNLOAD_LIST, () => scheduleRefresh())
    on(EVENTS.DOWNLOAD_TASK, (payload) => {
      // 事件负载可能是单条，也可能是数组
      if (Array.isArray(payload)) payload.forEach(applyTask)
      else if (payload && typeof payload === 'object') applyTask(payload)
    })
  }

  /** 退订。 */
  function unsubscribe() {
    if (!subscribed) return
    subscribed = false
    off(EVENTS.DOWNLOAD_LIST)
    off(EVENTS.DOWNLOAD_TASK)
  }

  return {
    tasks,
    loading,
    error,
    active,
    paused,
    done,
    failed,
    overall,
    refresh,
    add,
    addSeries,
    remove,
    pause,
    resume,
    openDir,
    subscribe,
    unsubscribe,
  }
})

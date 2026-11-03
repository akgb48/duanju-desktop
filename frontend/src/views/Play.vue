<script setup>
/**
 * 播放页。
 *
 * 布局：左侧 16:9 播放器 + 控制条 + 弹幕层；右侧选集与信息。
 *
 * 视频技术选择：
 *   - 优先原生 <video>（mp4 直链）
 *   - 若 src 以 .m3u8 结尾或 format === 'hls'，尝试动态 import hls.js；
 *     hls.js 缺失或初始化失败时回落直接把 src 交给 <video>（Safari/Edge 原生支持）
 *
 * 进度上报：timeupdate 里调 player.report()，store 内部节流。
 */
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PlayerControls from '../components/PlayerControls.vue'
import DanmakuLayer from '../components/DanmakuLayer.vue'
import EpisodeList from '../components/EpisodeList.vue'
import CommentPanel from '../components/CommentPanel.vue'
import * as api from '../api/bridge'
import { EVENTS, on, off, IS_MOCK } from '../api/bridge'
import { usePlayerStore } from '../stores/player'
import { useAccountStore } from '../stores/account'
import { useDownloadStore } from '../stores/download'
import { useUiStore } from '../stores/ui'

const route = useRoute()
const router = useRouter()
const player = usePlayerStore()
const account = useAccountStore()
const downloader = useDownloadStore()
const ui = useUiStore()

const seriesId = computed(() => String(route.params.seriesId || ''))

//
// 兼容处理：videoId 可能是纯数字集号（内部跳转用），也可能是上游的
// video_id 字符串（从详情页/历史记录跳过来）。是数字就当集号，
// 否则先按 1 集加载，等剧集列表回来再按 videoId 反查真实集号。
const videoIdParam = computed(() => String(route.params.videoId || ''))
const episodeNo = computed(() => {
  const v = videoIdParam.value
  if (!v) return 1
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : 1
})

/** DOM 引用。 */
const videoEl = ref(null)
const stageEl = ref(null)
const danmakuRef = ref(null)

/** 播放器本地状态（与 <video> 同步）。 */
const current = ref(0)
const duration = ref(0)
const buffered = ref(0)
const playing = ref(false)
const muted = ref(false)
const volume = ref(0.8)
const rate = ref(1)
const fullscreen = ref(false)
const controlsVisible = ref(true)
const videoError = ref('')
/** 调试模式下后端给的地址是空串：不报错，显示占位。 */
const mockNoSource = ref(false)

/** 剧集列表与系列信息。 */
const seriesInfo = ref(null)
const episodes = ref([])
const loadingSeries = ref(false)

/** 弹幕。 */
const danmakuList = ref([])
const danmakuEnabled = ref(true)

/** 评论面板。 */
const sideTab = ref('episodes') // episodes | comments

/** hls 实例。 */
let hls = null
/** 控制条自动隐藏计时器。 */
let hideTimer = null
/** 进度上报的 last save。 */
let lastTimeUpdate = 0
/** 事件取消函数。 */
const unsubs = []

/** 当前集。 */
const currentEpisode = computed(() => {
  const list = episodes.value
  return list.find((e) => (e.index || e.Index) === episodeNo.value) || null
})

const hasPrev = computed(() => episodeNo.value > 1)
const hasNext = computed(() => {
  const list = episodes.value
  if (!list.length) return false
  return episodeNo.value < (list[list.length - 1].index || list.length)
})

const videoSrc = computed(() => player.src || '')

/** 是否 HLS。 */
const isHls = computed(() => {
  const s = player.currentSource
  if (!s) return false
  if (s.format === 'hls') return true
  return /\.m3u8(\?|$)/i.test(s.url || '')
})

/** 标题。 */
const pageTitle = computed(() => {
  const t = (seriesInfo.value && seriesInfo.value.title) || '播放'
  return `${t} · 第 ${episodeNo.value} 集`
})

// ---------------------------------------------------------------------------
// 数据加载
// ---------------------------------------------------------------------------

async function loadSeries() {
  if (!seriesId.value) return
  loadingSeries.value = true
  try {
    const d = await api.SeriesDetail(seriesId.value)
    seriesInfo.value = d || null
    // 后端可能把 episodes 内嵌在详情里，也可能要单独拉
    let list = (d && (d.episodes || d.Episodes)) || []
    if (!list.length) {
      try {
        const r = await api.SeriesSeasons(seriesId.value)
        // SeriesSeasons 返回季列表；若只有一季则直接取季内集
        if (Array.isArray(r) && r.length === 1 && Array.isArray(r[0].episodes)) {
          list = r[0].episodes
        }
      } catch {
        /* 忽略 */
      }
    }
    // 兜底：按 episodeCount 造出集号，保证选集可用
    if (!list.length && d && d.episodeCount) {
      list = Array.from({ length: Number(d.episodeCount) }, (_, i) => ({
        seriesId: seriesId.value,
        index: i + 1,
        title: `第 ${i + 1} 集`,
        isFree: true,
      }))
    }
    episodes.value = list
  } catch (e) {
    ui.toastErr(e.message || '加载剧集信息失败')
  } finally {
    loadingSeries.value = false
  }
}

async function loadPlay() {
  videoError.value = ''
  const r = await player.load(seriesId.value, episodeNo.value)
  if (!r.ok) {
    videoError.value = r.error
    return
  }
  await nextTick()
  attachSource()
}

/** 把地址挂到 <video> 上。 */
async function attachSource() {
  const el = videoEl.value
  if (!el) return

  // 先拆旧的 hls
  if (hls) {
    try {
      hls.destroy()
    } catch {
      /* 忽略 */
    }
    hls = null
  }
  el.removeAttribute('src')
  el.load()

  const src = videoSrc.value
  if (!src) {
    // 调试模式下后端给的地址是空串，这是预期的，不要报错
    if (IS_MOCK) {
      mockNoSource.value = true
      return
    }
    videoError.value = '当前清晰度没有可用地址'
    return
  }
  mockNoSource.value = false

  if (isHls.value) {
    try {
      const mod = await import('hls.js')
      const Hls = mod.default || mod.Hls
      if (Hls && Hls.isSupported && Hls.isSupported()) {
        hls = new Hls({
          maxBufferLength: 30,
          enableWorker: true,
          lowLatencyMode: false,
        })
        hls.loadSource(src)
        hls.attachMedia(el)
        hls.on(Hls.Events.ERROR, (_evt, data) => {
          if (data && data.fatal) {
            // 致命错误：回落原生
            try {
              hls.destroy()
            } catch {
              /* 忽略 */
            }
            hls = null
            el.src = src
            el.load()
          }
        })
        return
      }
    } catch {
      // hls.js 不可用：走原生
    }
  }

  // 原生
  el.src = src
  el.load()
}

/** 弹幕加载。 */
async function loadDanmaku() {
  try {
    const r = await api.Danmaku(seriesId.value, player.detail && player.detail.videoId, 0, 500)
    const list = Array.isArray(r) ? r : (r && r.items) || []
    danmakuList.value = list.map((d) => ({
      id: d.id,
      text: d.text,
      offsetMs: Number(d.offsetMs || 0),
      color: d.color || '',
    }))
  } catch {
    danmakuList.value = []
  }
}

// ---------------------------------------------------------------------------
// 播放控制
// ---------------------------------------------------------------------------

function doPlay() {
  const el = videoEl.value
  if (!el) return
  el.play().catch((e) => {
    videoError.value = /NotAllowed|denied/i.test(e.message) ? '浏览器阻止了自动播放，请点击播放' : e.message
  })
}

function doPause() {
  const el = videoEl.value
  if (el) el.pause()
}

function togglePlay() {
  if (playing.value) doPause()
  else doPlay()
}

function doSeek(sec) {
  const el = videoEl.value
  if (!el) return
  el.currentTime = Math.max(0, Math.min(sec, duration.value || sec))
  // 拖动后强制上报一次
  player.report(el.currentTime * 1000, (duration.value || 0) * 1000, true)
}

function setVolume(v) {
  const el = videoEl.value
  const val = Math.max(0, Math.min(1, v))
  volume.value = val
  if (el) {
    el.volume = val
    el.muted = val === 0
  }
  muted.value = val === 0
  player.setPref('volume', val)
  player.setPref('muted', val === 0)
}

function toggleMute() {
  const el = videoEl.value
  if (!el) return
  const next = !el.muted
  el.muted = next
  muted.value = next
  player.setPref('muted', next)
}

function setRate(r) {
  const el = videoEl.value
  if (!el) return
  el.playbackRate = r
  rate.value = r
  player.setPref('rate', r)
}

function setQuality(q) {
  if (q === player.quality) return
  const t = videoEl.value ? videoEl.value.currentTime : 0
  player.setQuality(q)
  nextTick(async () => {
    await attachSource()
    const el = videoEl.value
    if (el) {
      el.currentTime = t
      if (playing.value) doPlay()
    }
  })
}

function toggleDanmaku(v) {
  danmakuEnabled.value = v === undefined ? !danmakuEnabled.value : !!v
  player.setPref('danmaku', danmakuEnabled.value)
}

function toggleFullscreen() {
  const el = stageEl.value
  if (!el) return
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {})
  } else {
    el.requestFullscreen().catch(() => {
      ui.toastWarn('当前环境不支持全屏')
    })
  }
}

function goEpisode(ep) {
  const n = ep.index || ep.Index || ep
  if (!n || n === episodeNo.value) return
  // 切集前保存进度
  if (videoEl.value && duration.value) {
    player.report(videoEl.value.currentTime * 1000, duration.value * 1000, true)
  }
  router.push({ name: 'play', params: { seriesId: seriesId.value, videoId: String(n) } })
}

function prevEp() {
  if (hasPrev.value) goEpisode(episodeNo.value - 1)
}
function nextEp() {
  if (hasNext.value) goEpisode(episodeNo.value + 1)
}

/** 发弹幕。 */
async function sendDanmaku(text) {
  if (!account.loggedIn) {
    ui.toastWarn('请先登录')
    return
  }
  const ms = Math.round((videoEl.value ? videoEl.value.currentTime : 0) * 1000)
  try {
    await api.SendDanmaku(seriesId.value, (player.detail && player.detail.videoId) || '', text, ms, '')
    danmakuList.value = [
      ...danmakuList.value,
      { id: `local-${Date.now()}`, text, offsetMs: ms, color: '' },
    ]
  } catch (e) {
    ui.toastErr(e.message || '弹幕发送失败')
  }
}

/** 收藏。 */
async function toggleFavorite() {
  if (!account.loggedIn) {
    ui.toastWarn('请先登录')
    return
  }
  const cur = !!(seriesInfo.value && seriesInfo.value.isFavorite)
  try {
    await api.SetFavorite(seriesId.value, !cur)
    if (seriesInfo.value) seriesInfo.value.isFavorite = !cur
    ui.toastOk(cur ? '已取消追剧' : '已加入追剧')
  } catch (e) {
    ui.toastErr(e.message || '操作失败')
  }
}

/** 下载当前集。 */
async function downloadCurrent() {
  const r = await downloader.add(seriesId.value, episodeNo.value, episodeNo.value)
  if (r.ok) ui.toastOk('已加入下载队列')
  else ui.toastErr(r.error)
}

// ---------------------------------------------------------------------------
// <video> 事件
// ---------------------------------------------------------------------------

function onLoadedMetadata() {
  const el = videoEl.value
  if (!el) return
  duration.value = el.duration || 0
  // 恢复偏好
  el.volume = player.prefs.volume
  el.muted = !!player.prefs.muted
  el.playbackRate = player.prefs.rate
  volume.value = el.volume
  muted.value = el.muted
  rate.value = el.playbackRate
  // 自动播放
  if (player.prefs.autoNext !== false) doPlay()
}

function onTimeUpdate() {
  const el = videoEl.value
  if (!el) return
  current.value = el.currentTime
  // 节流：每 250ms 更新一次 UI
  const now = Date.now()
  if (now - lastTimeUpdate > 250) {
    lastTimeUpdate = now
    player.report(el.currentTime * 1000, (el.duration || 0) * 1000)
  }
  // 缓冲
  try {
    if (el.buffered.length) {
      buffered.value = el.buffered.end(el.buffered.length - 1)
    }
  } catch {
    /* 忽略 */
  }
}

function onProgress() {
  const el = videoEl.value
  if (!el) return
  try {
    if (el.buffered.length) buffered.value = el.buffered.end(el.buffered.length - 1)
  } catch {
    /* 忽略 */
  }
}

function onPlay() {
  playing.value = true
  scheduleHideControls()
}

function onPause() {
  playing.value = false
  controlsVisible.value = true
  if (hideTimer) clearTimeout(hideTimer)
  // 暂停时强制上报
  const el = videoEl.value
  if (el) player.report(el.currentTime * 1000, (el.duration || 0) * 1000, true)
}

function onEnded() {
  playing.value = false
  const el = videoEl.value
  if (el) player.report(el.duration * 1000, el.duration * 1000, true)
  if (hasNext.value) {
    ui.toast('即将播放下一集', 'info', 1800)
    setTimeout(() => nextEp(), 1200)
  }
}

function onVideoError() {
  const el = videoEl.value
  if (!el || !el.error) return
  const code = el.error.code
  const map = {
    1: '播放被中止',
    2: '网络错误，无法加载视频',
    3: '视频解码失败',
    4: '视频格式不受支持',
  }
  videoError.value = map[code] || `播放失败（code ${code}）`
}

/** 控制条自动隐藏。 */
function scheduleHideControls() {
  if (hideTimer) clearTimeout(hideTimer)
  controlsVisible.value = true
  hideTimer = setTimeout(() => {
    if (playing.value && !fullscreen.value) controlsVisible.value = false
  }, 3000)
}

function onStageMove() {
  scheduleHideControls()
}

function onFullscreenChange() {
  fullscreen.value = !!document.fullscreenElement
  if (fullscreen.value) controlsVisible.value = true
  else scheduleHideControls()
}

/** 键盘快捷键。 */
function onKeydown(e) {
  // 输入框里不拦截
  const tag = (e.target && e.target.tagName) || ''
  if (tag === 'INPUT' || tag === 'TEXTAREA') return

  switch (e.key) {
    case ' ':
    case 'k':
      e.preventDefault()
      togglePlay()
      break
    case 'ArrowLeft':
      e.preventDefault()
      doSeek((videoEl.value ? videoEl.value.currentTime : 0) - 5)
      break
    case 'ArrowRight':
      e.preventDefault()
      doSeek((videoEl.value ? videoEl.value.currentTime : 0) + 5)
      break
    case 'ArrowUp':
      e.preventDefault()
      setVolume(volume.value + 0.05)
      break
    case 'ArrowDown':
      e.preventDefault()
      setVolume(volume.value - 0.05)
      break
    case 'm':
      toggleMute()
      break
    case 'f':
      toggleFullscreen()
      break
    case 'd':
      toggleDanmaku()
      break
    case 'Escape':
      if (fullscreen.value) toggleFullscreen()
      break
    default:
      break
  }
}

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

async function bootstrap() {
  await loadSeries()
  await loadPlay()
  await loadDanmaku()
}

watch(
  () => [seriesId.value, episodeNo.value],
  async (nv, ov) => {
    if (!ov || nv[0] !== ov[0]) {
      await loadSeries()
    }
    await loadPlay()
    await loadDanmaku()
  },
)

onMounted(async () => {
  danmakuEnabled.value = player.prefs.danmaku !== false
  await bootstrap()

  window.addEventListener('keydown', onKeydown)
  document.addEventListener('fullscreenchange', onFullscreenChange)

  // 老板键 / 隐身 / 最小化：通知暂停
  unsubs.push(
    on(EVENTS.BOSS_HIDE, () => {
      if (playing.value) doPause()
    }),
  )
  unsubs.push(
    on(EVENTS.BOSS_SHOW, () => {
      if (!playing.value && videoEl.value) doPlay()
    }),
  )
  unsubs.push(
    on(EVENTS.STEALTH_HIDE, () => {
      if (playing.value) doPause()
    }),
  )
  unsubs.push(
    on(EVENTS.STEALTH_SHOW, () => {
      if (!playing.value && videoEl.value) doPlay()
    }),
  )
  unsubs.push(
    on(EVENTS.WINDOW_MINIMISE, () => {
      if (playing.value) doPause()
    }),
  )
  unsubs.push(
    on(EVENTS.WINDOW_RESTORE, () => {
      if (videoEl.value && !playing.value) doPlay()
    }),
  )

  // 播放时阻止休眠
  api.KeepAwake(true).catch(() => {})
})

onBeforeUnmount(() => {
  // 保存进度
  const el = videoEl.value
  if (el && el.duration) {
    player.report(el.currentTime * 1000, el.duration * 1000, true)
  }
  if (hls) {
    try {
      hls.destroy()
    } catch {
      /* 忽略 */
    }
    hls = null
  }
  if (hideTimer) clearTimeout(hideTimer)
  window.removeEventListener('keydown', onKeydown)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  for (const u of unsubs) {
    try {
      u()
    } catch {
      /* 忽略 */
    }
  }
  unsubs.length = 0
  api.KeepAwake(false).catch(() => {})
  player.reset()
})
</script>

<template>
  <div class="play">
    <!-- 左侧：播放器 -->
    <div class="play-stage-wrap">
      <div
        ref="stageEl"
        class="play-stage"
        @pointermove="onStageMove"
        @pointerleave="scheduleHideControls"
      >
        <video
          ref="videoEl"
          class="play-video"
          playsinline
          preload="metadata"
          @loadedmetadata="onLoadedMetadata"
          @timeupdate="onTimeUpdate"
          @progress="onProgress"
          @play="onPlay"
          @pause="onPause"
          @ended="onEnded"
          @error="onVideoError"
          @click="togglePlay"
        />

        <!-- 弹幕层 -->
        <DanmakuLayer
          v-if="danmakuEnabled"
          ref="danmakuRef"
          :list="danmakuList"
          :current-ms="current * 1000"
          :playing="playing"
          :enabled="danmakuEnabled"
          :opacity="player.prefs.opacity || 80"
          @send="sendDanmaku"
        />

        <!-- 大播放按钮（暂停时） -->
        <button
          v-if="!playing && !videoError && !mockNoSource"
          class="play-big"
          title="播放"
          @click.stop="doPlay"
        >
          <svg width="64" height="64" viewBox="0 0 64 64">
            <circle cx="32" cy="32" r="31" fill="rgba(0,0,0,.5)" />
            <path d="M26 20l18 12-18 12z" fill="#fff" />
          </svg>
        </button>

        <!-- 调试模式占位 -->
        <div v-if="mockNoSource" class="play-mock">
          <span class="play-mock-icon">▶</span>
          <p>调试模式：后端未返回播放地址</p>
          <p class="mute t-f1">接入 Wails 后端后会在这里渲染视频</p>
        </div>

        <!-- 错误 -->
        <div v-if="videoError" class="play-err">
          <span class="play-err-icon">⚠</span>
          <p>{{ videoError }}</p>
          <div class="play-err-actions">
            <button class="btn btn-sm" @click="loadPlay">重试</button>
            <button class="btn btn-sm btn-ghost" @click="downloadCurrent">先下载</button>
          </div>
        </div>

        <!-- 加载中 -->
        <div v-if="player.loading" class="play-loading">
          <span class="spinner" style="width: 28px; height: 28px" />
          <span>正在取播放地址…</span>
        </div>

        <!-- 控制条 -->
        <transition name="fade">
          <PlayerControls
            v-show="controlsVisible || !playing"
            :current="current"
            :duration="duration"
            :buffered="buffered"
            :playing="playing"
            :volume="volume"
            :muted="muted"
            :rate="rate"
            :qualities="player.sources"
            :quality="player.quality"
            :danmaku="danmakuEnabled"
            :has-prev="hasPrev"
            :has-next="hasNext"
            :fullscreen="fullscreen"
            :loading="player.loading"
            @play="doPlay"
            @pause="doPause"
            @seek="doSeek"
            @volume="setVolume"
            @mute="toggleMute"
            @rate="setRate"
            @quality="setQuality"
            @danmaku="toggleDanmaku"
            @prev="prevEp"
            @next="nextEp"
            @fullscreen="toggleFullscreen"
          />
        </transition>
      </div>

      <!-- 下方：标题与操作 -->
      <div class="play-bar">
        <div class="play-title-wrap">
          <h1 class="play-title ellipsis">{{ pageTitle }}</h1>
          <p class="play-sub">
            <span v-if="seriesInfo && seriesInfo.author">{{ seriesInfo.author }}</span>
            <span v-if="seriesInfo && seriesInfo.category" class="tag">{{ seriesInfo.category }}</span>
            <span v-if="player.detail && player.detail.videoId" class="mute t-f1 t-num">
              {{ player.detail.videoId }}
            </span>
          </p>
        </div>

        <div class="play-acts">
          <button class="btn btn-sm" @click="toggleFavorite">
            {{ seriesInfo && seriesInfo.isFavorite ? '★ 已追剧' : '☆ 追剧' }}
          </button>
          <button class="btn btn-sm" @click="downloadCurrent">⤓ 下载本集</button>
          <button
            class="btn btn-sm"
            @click="router.push({ name: 'series', params: { id: seriesId } })"
          >
            剧集详情
          </button>
        </div>
      </div>
    </div>

    <!-- 右侧：选集 / 评论 -->
    <aside class="play-side">
      <div class="v-tabs play-side-tabs">
        <button
          class="v-tab"
          :class="{ 'is-active': sideTab === 'episodes' }"
          @click="sideTab = 'episodes'"
        >
          选集
        </button>
        <button
          class="v-tab"
          :class="{ 'is-active': sideTab === 'comments' }"
          @click="sideTab = 'comments'"
        >
          评论
        </button>
      </div>

      <div class="play-side-body scroll-y">
        <!-- 选集 -->
        <template v-if="sideTab === 'episodes'">
          <div v-if="loadingSeries" class="play-side-loading">
            <span class="spinner" />
          </div>
          <EpisodeList
            v-else
            :episodes="episodes"
            :current="episodeNo"
            layout="list"
            downloadable
            @select="goEpisode"
            @download="(ep) => downloader.add(seriesId, ep.index, ep.index)"
          />
        </template>

        <!-- 评论 -->
        <template v-else>
          <CommentPanel
            v-if="seriesId"
            :series-id="seriesId"
            :video-id="(player.detail && player.detail.videoId) || ''"
          />
        </template>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.play {
  display: flex;
  height: 100%;
  min-height: 0;
  background: var(--c-bg);
}

/* ---- 舞台 ---- */
.play-stage-wrap {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  background: #000;
}

.play-stage {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #000;
  overflow: hidden;
}

.play-video {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
  cursor: pointer;
}

/* 大播放按钮 */
.play-big {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  z-index: 4;
  opacity: 0.9;
  transition: opacity var(--t-fast), transform var(--t-fast);
}
.play-big:hover {
  opacity: 1;
  transform: translate(-50%, -50%) scale(1.06);
}

/* 错误 / 加载 */
.play-err {
  position: absolute;
  inset: 0;
  z-index: 3;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--s-3);
  background: rgba(0, 0, 0, 0.72);
  color: #fff;
  text-align: center;
  padding: var(--s-4);
}
.play-err-icon {
  font-size: 32px;
}
.play-err p {
  font-size: var(--f-3);
  color: rgba(255, 255, 255, 0.85);
  max-width: 400px;
}
.play-err-actions {
  display: flex;
  gap: var(--s-2);
}

.play-loading {
  position: absolute;
  inset: 0;
  z-index: 3;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--s-3);
  background: rgba(0, 0, 0, 0.6);
  color: rgba(255, 255, 255, 0.85);
  font-size: var(--f-3);
}

/* 调试占位 */
.play-mock {
  position: absolute;
  inset: 0;
  z-index: 3;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--s-2);
  background: radial-gradient(600px 300px at 50% 45%, rgba(255, 77, 79, 0.08), transparent 70%), #000;
  color: rgba(255, 255, 255, 0.7);
  font-size: var(--f-3);
  text-align: center;
}
.play-mock-icon {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--f-6);
  border: 2px solid rgba(255, 255, 255, 0.25);
  color: rgba(255, 255, 255, 0.5);
  margin-bottom: var(--s-2);
}

/* ---- 标题条 ---- */
.play-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-4);
  padding: var(--s-3) var(--s-4);
  background: var(--c-bg-elev);
  border-top: 1px solid var(--c-line-soft);
}
.play-title-wrap {
  min-width: 0;
  flex: 1 1 auto;
}
.play-title {
  font-size: var(--f-5);
  font-weight: 600;
  margin-bottom: 2px;
}
.play-sub {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  font-size: var(--f-2);
  color: var(--c-text-mute);
}
.play-acts {
  flex: 0 0 auto;
  display: flex;
  gap: var(--s-2);
}

/* ---- 侧栏 ---- */
.play-side {
  flex: 0 0 auto;
  width: 320px;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--c-bg-elev);
  border-left: 1px solid var(--c-line-soft);
}
.play-side-tabs {
  margin: var(--s-3);
  flex: 0 0 auto;
}
.play-side-body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 0 var(--s-3) var(--s-3);
}
.play-side-loading {
  display: flex;
  justify-content: center;
  padding: var(--s-6);
}

@media (max-width: 980px) {
  .play-side {
    width: 260px;
  }
}
</style>

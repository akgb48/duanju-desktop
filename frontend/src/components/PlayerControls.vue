<script setup>
/**
 * 播放器控制条。
 *
 * 只负责展示与派发事件，不直接持有 <video>。
 * 父组件（Play.vue）把 video 的状态传进来，把用户操作接出去。
 *
 * 包含：进度条（含缓冲/悬停预览）、时间、播放/暂停、上一集/下一集、
 * 清晰度、倍速、音量、弹幕开关、全屏。
 */
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'

const props = defineProps({
  /** 当前时间（秒） */
  current: { type: Number, default: 0 },
  /** 总时长（秒） */
  duration: { type: Number, default: 0 },
  /** 已缓冲到的秒数 */
  buffered: { type: Number, default: 0 },
  /** 是否播放中 */
  playing: { type: Boolean, default: false },
  /** 音量 0-1 */
  volume: { type: Number, default: 0.8 },
  /** 是否静音 */
  muted: { type: Boolean, default: false },
  /** 倍速 */
  rate: { type: Number, default: 1 },
  /** 清晰度列表 [{quality}] */
  qualities: { type: Array, default: () => [] },
  /** 当前清晰度 */
  quality: { type: String, default: '' },
  /** 弹幕开关 */
  danmaku: { type: Boolean, default: true },
  /** 是否可上一集/下一集 */
  hasPrev: { type: Boolean, default: false },
  hasNext: { type: Boolean, default: false },
  /** 是否全屏 */
  fullscreen: { type: Boolean, default: false },
  /** 是否加载中 */
  loading: { type: Boolean, default: false },
})

const emit = defineEmits([
  'play',
  'pause',
  'seek',
  'volume',
  'mute',
  'rate',
  'quality',
  'danmaku',
  'prev',
  'next',
  'fullscreen',
])

/** 进度条拖拽状态。 */
const dragging = ref(false)
/** 拖拽中的临时值（秒）。 */
const dragValue = ref(0)
/** 悬停位置（比例 0-1）与坐标。 */
const hoverRatio = ref(-1)
const hoverX = ref(0)
const barEl = ref(null)

/** 显示用当前时间：拖拽时用临时值。 */
const shownCurrent = computed(() => (dragging.value ? dragValue.value : props.current))

/** 进度比例。 */
const progress = computed(() => {
  if (!props.duration) return 0
  return Math.min(1, Math.max(0, shownCurrent.value / props.duration))
})

/** 缓冲比例。 */
const bufRatio = computed(() => {
  if (!props.duration) return 0
  return Math.min(1, Math.max(0, props.buffered / props.duration))
})

/** 悬停位置的时间文本。 */
const hoverTime = computed(() => {
  if (hoverRatio.value < 0 || !props.duration) return ''
  return fmtTime(hoverRatio.value * props.duration)
})

/** 秒 → m:ss / h:mm:ss。 */
function fmtTime(s) {
  const v = Math.max(0, Math.floor(Number(s) || 0))
  const h = Math.floor(v / 3600)
  const m = Math.floor((v % 3600) / 60)
  const sec = v % 60
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
  return `${m}:${String(sec).padStart(2, '0')}`
}

/** 从鼠标事件算比例。 */
function ratioFromEvent(e) {
  const el = barEl.value
  if (!el) return 0
  const rect = el.getBoundingClientRect()
  const x = Math.min(Math.max(0, e.clientX - rect.left), rect.width)
  return rect.width > 0 ? x / rect.width : 0
}

function onBarDown(e) {
  dragging.value = true
  dragValue.value = ratioFromEvent(e) * props.duration
  const el = barEl.value
  if (el && el.setPointerCapture) {
    try {
      el.setPointerCapture(e.pointerId)
    } catch {
      /* 忽略 */
    }
  }
  window.addEventListener('pointermove', onBarMove)
  window.addEventListener('pointerup', onBarUp)
}

function onBarMove(e) {
  const r = ratioFromEvent(e)
  hoverRatio.value = r
  if (dragging.value) dragValue.value = r * props.duration
}

function onBarUp() {
  if (dragging.value) {
    emit('seek', dragValue.value)
    dragging.value = false
  }
  window.removeEventListener('pointermove', onBarMove)
  window.removeEventListener('pointerup', onBarUp)
}

function onBarEnter(e) {
  const el = barEl.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  hoverX.value = e.clientX - rect.left
  hoverRatio.value = ratioFromEvent(e)
}

function onBarLeave() {
  hoverRatio.value = -1
}

/** 音量滑块。 */
const volDragging = ref(false)
const volEl = ref(null)

function onVolDown(e) {
  volDragging.value = true
  applyVol(e)
  window.addEventListener('pointermove', onVolMove)
  window.addEventListener('pointerup', onVolUp)
}
function onVolMove(e) {
  if (volDragging.value) applyVol(e)
}
function onVolUp() {
  volDragging.value = false
  window.removeEventListener('pointermove', onVolMove)
  window.removeEventListener('pointerup', onVolUp)
}
function applyVol(e) {
  const el = volEl.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const x = Math.min(Math.max(0, e.clientX - rect.left), rect.width)
  emit('volume', rect.width > 0 ? x / rect.width : 0)
}

/** 倍速菜单。 */
const rateMenu = ref(false)
const rateMenuRef = ref(null)
const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2]

function pickRate(r) {
  emit('rate', r)
  rateMenu.value = false
}

/** 清晰度菜单。 */
const qualityMenu = ref(false)

function pickQuality(q) {
  emit('quality', q)
  qualityMenu.value = false
}

/** 点击外部关闭菜单。 */
function onDocClick(e) {
  if (rateMenuRef.value && !rateMenuRef.value.contains(e.target)) rateMenu.value = false
  const qm = e.target.closest && e.target.closest('.pc-menu-wrap')
  if (!qm) qualityMenu.value = false
}

onMounted(() => {
  window.addEventListener('mousedown', onDocClick)
})

onBeforeUnmount(() => {
  window.removeEventListener('mousedown', onDocClick)
  // 拖拽中卸载时把 window 上的监听摘掉，避免泄漏
  window.removeEventListener('pointermove', onBarMove)
  window.removeEventListener('pointerup', onBarUp)
  window.removeEventListener('pointermove', onVolMove)
  window.removeEventListener('pointerup', onVolUp)
})

/** 悬浮显示控制条由父组件用 CSS 控制，这里只管「鼠标在控制条上时不要自动隐藏」。 */
const hovering = ref(false)
defineExpose({ hovering })
</script>

<template>
  <div class="pctrl" @mouseenter="hovering = true" @mouseleave="hovering = false">
    <!-- ---- 进度条 ---- -->
    <div
      ref="barEl"
      class="pc-bar"
      @pointerdown="onBarDown"
      @pointermove="onBarMove"
      @pointerenter="onBarEnter"
      @pointerleave="onBarLeave"
    >
      <div class="pc-bar-track">
        <div class="pc-bar-buf" :style="{ width: bufRatio * 100 + '%' }" />
        <div class="pc-bar-fill" :style="{ width: progress * 100 + '%' }" />
        <div class="pc-bar-knob" :style="{ left: progress * 100 + '%' }" />
      </div>

      <!-- 悬停时间气泡 -->
      <div
        v-if="hoverRatio >= 0"
        class="pc-bar-tip"
        :style="{ left: hoverX + 'px' }"
      >
        {{ hoverTime }}
      </div>
    </div>

    <!-- ---- 按钮行 ---- -->
    <div class="pc-row">
      <!-- 左：播放控制 -->
      <div class="pc-left">
        <button
          class="pc-btn"
          :disabled="!hasPrev"
          title="上一集"
          @click="emit('prev')"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M4 3v10M13 3L6 8l7 5z" fill="currentColor" />
          </svg>
        </button>

        <button
          class="pc-btn pc-btn-main"
          :title="playing ? '暂停' : '播放'"
          @click="emit(playing ? 'pause' : 'play')"
        >
          <span v-if="loading" class="spinner" style="width: 14px; height: 14px" />
          <svg v-else-if="playing" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
            <rect x="3" y="2.5" width="4" height="11" rx="1" fill="currentColor" />
            <rect x="9" y="2.5" width="4" height="11" rx="1" fill="currentColor" />
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M4 2.5l10 5.5-10 5.5z" fill="currentColor" />
          </svg>
        </button>

        <button
          class="pc-btn"
          :disabled="!hasNext"
          title="下一集"
          @click="emit('next')"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M12 3v10M3 3l7 5-7 5z" fill="currentColor" />
          </svg>
        </button>

        <!-- 音量 -->
        <div class="pc-vol-wrap">
          <button class="pc-btn" :title="muted ? '取消静音' : '静音'" @click="emit('mute')">
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M2 6h3l4-3v10L5 10H2z" fill="currentColor" />
              <path
                v-if="!muted && volume > 0"
                d="M11 5.5a3.5 3.5 0 010 5"
                stroke="currentColor"
                stroke-width="1.3"
                fill="none"
                stroke-linecap="round"
              />
              <path
                v-if="!muted && volume > 0.5"
                d="M12.6 3.9a5.6 5.6 0 010 8.2"
                stroke="currentColor"
                stroke-width="1.3"
                fill="none"
                stroke-linecap="round"
              />
              <path
                v-if="muted || volume === 0"
                d="M11 6l3 4M14 6l-3 4"
                stroke="currentColor"
                stroke-width="1.3"
                stroke-linecap="round"
              />
            </svg>
          </button>
          <div
            ref="volEl"
            class="pc-vol"
            @pointerdown="onVolDown"
          >
            <div class="pc-vol-track">
              <div class="pc-vol-fill" :style="{ width: (muted ? 0 : volume) * 100 + '%' }" />
            </div>
          </div>
        </div>

        <span class="pc-time t-num">
          {{ fmtTime(shownCurrent) }} <span class="pc-time-sep">/</span> {{ fmtTime(duration) }}
        </span>
      </div>

      <!-- 右：设置 -->
      <div class="pc-right">
        <!-- 倍速 -->
        <div ref="rateMenuRef" class="pc-menu-wrap">
          <button class="pc-btn pc-btn-text" :title="`倍速 ${rate}x`" @click="rateMenu = !rateMenu">
            {{ rate === 1 ? '倍速' : `${rate}x` }}
          </button>
          <ul v-if="rateMenu" class="pc-menu">
            <li
              v-for="r in RATES"
              :key="r"
              class="pc-menu-item"
              :class="{ 'is-on': r === rate }"
              @click="pickRate(r)"
            >
              {{ r === 1 ? '正常' : `${r}x` }}
            </li>
          </ul>
        </div>

        <!-- 清晰度 -->
        <div v-if="qualities.length" class="pc-menu-wrap">
          <button class="pc-btn pc-btn-text" :title="`清晰度 ${quality}`" @click="qualityMenu = !qualityMenu">
            {{ quality || '清晰度' }}
          </button>
          <ul v-if="qualityMenu" class="pc-menu">
            <li
              v-for="q in qualities"
              :key="q.quality"
              class="pc-menu-item"
              :class="{ 'is-on': q.quality === quality }"
              @click="pickQuality(q.quality)"
            >
              {{ q.quality }}
            </li>
          </ul>
        </div>

        <!-- 弹幕 -->
        <button
          class="pc-btn pc-btn-text"
          :class="{ 'is-off': !danmaku }"
          :title="danmaku ? '关闭弹幕' : '开启弹幕'"
          @click="emit('danmaku', !danmaku)"
        >
          弹
        </button>

        <!-- 全屏 -->
        <button class="pc-btn" :title="fullscreen ? '退出全屏' : '全屏'" @click="emit('fullscreen')">
          <svg v-if="!fullscreen" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M2 6V2h4M14 6V2h-4M2 10v4h4M14 10v4h-4"
              stroke="currentColor"
              stroke-width="1.4"
              fill="none"
              stroke-linecap="round"
            />
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M6 2v4H2M10 2v4h4M6 14v-4H2M10 14v-4h4"
              stroke="currentColor"
              stroke-width="1.4"
              fill="none"
              stroke-linecap="round"
            />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pctrl {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 5;
  padding: var(--s-2) var(--s-4) var(--s-3);
  background: linear-gradient(to top, rgba(0, 0, 0, 0.82), rgba(0, 0, 0, 0.4) 60%, transparent);
  transition: opacity var(--t-base), transform var(--t-base);
}

/* ---- 进度条 ---- */
.pc-bar {
  position: relative;
  height: 18px;
  display: flex;
  align-items: center;
  cursor: pointer;
  margin-bottom: 2px;
  touch-action: none;
}
.pc-bar-track {
  position: relative;
  width: 100%;
  height: 4px;
  border-radius: var(--r-full);
  background: rgba(255, 255, 255, 0.22);
  overflow: visible;
  transition: height var(--t-fast);
}
.pc-bar:hover .pc-bar-track {
  height: 6px;
}
.pc-bar-buf {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  border-radius: var(--r-full);
  background: rgba(255, 255, 255, 0.3);
}
.pc-bar-fill {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  border-radius: var(--r-full);
  background: var(--c-brand);
}
.pc-bar-knob {
  position: absolute;
  top: 50%;
  width: 12px;
  height: 12px;
  margin-left: -6px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.5);
  transform: translateY(-50%) scale(0);
  transition: transform var(--t-fast);
}
.pc-bar:hover .pc-bar-knob,
.pc-bar:active .pc-bar-knob {
  transform: translateY(-50%) scale(1);
}

.pc-bar-tip {
  position: absolute;
  bottom: 20px;
  transform: translateX(-50%);
  padding: 2px 6px;
  font-size: var(--f-1);
  font-family: var(--font-num);
  color: #fff;
  background: rgba(0, 0, 0, 0.8);
  border-radius: var(--r-1);
  pointer-events: none;
  white-space: nowrap;
}

/* ---- 按钮行 ---- */
.pc-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-4);
}
.pc-left,
.pc-right {
  display: flex;
  align-items: center;
  gap: var(--s-1);
}

.pc-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--r-1);
  color: rgba(255, 255, 255, 0.82);
  transition: background var(--t-fast), color var(--t-fast);
}
.pc-btn:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.14);
  color: #fff;
}
.pc-btn:disabled {
  opacity: 0.3;
  cursor: default;
}
.pc-btn-main {
  width: 36px;
  height: 36px;
  color: #fff;
}
.pc-btn-text {
  width: auto;
  padding: 0 var(--s-2);
  font-size: var(--f-2);
  font-variant-numeric: tabular-nums;
}
.pc-btn-text.is-off {
  color: rgba(255, 255, 255, 0.35);
  text-decoration: line-through;
}

/* ---- 音量 ---- */
.pc-vol-wrap {
  display: flex;
  align-items: center;
}
.pc-vol {
  width: 0;
  overflow: hidden;
  transition: width var(--t-base), padding var(--t-base);
  cursor: pointer;
}
.pc-vol-wrap:hover .pc-vol {
  width: 64px;
  padding: 0 var(--s-1);
}
.pc-vol-track {
  height: 4px;
  border-radius: var(--r-full);
  background: rgba(255, 255, 255, 0.25);
}
.pc-vol-fill {
  height: 100%;
  border-radius: var(--r-full);
  background: #fff;
}

/* ---- 时间 ---- */
.pc-time {
  margin-left: var(--s-2);
  font-size: var(--f-2);
  color: rgba(255, 255, 255, 0.82);
  white-space: nowrap;
}
.pc-time-sep {
  opacity: 0.5;
  margin: 0 2px;
}

/* ---- 菜单 ---- */
.pc-menu-wrap {
  position: relative;
}
.pc-menu {
  position: absolute;
  right: 0;
  bottom: calc(100% + var(--s-2));
  min-width: 92px;
  padding: var(--s-1);
  background: rgba(20, 22, 26, 0.96);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: var(--r-2);
  box-shadow: var(--shadow-3);
  backdrop-filter: blur(8px);
}
.pc-menu-item {
  display: flex;
  align-items: center;
  height: 28px;
  padding: 0 var(--s-2);
  border-radius: var(--r-1);
  font-size: var(--f-2);
  color: rgba(255, 255, 255, 0.8);
  cursor: pointer;
  white-space: nowrap;
}
.pc-menu-item:hover {
  background: rgba(255, 255, 255, 0.12);
  color: #fff;
}
.pc-menu-item.is-on {
  color: var(--c-brand);
  font-weight: 600;
}
</style>

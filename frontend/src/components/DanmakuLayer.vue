<script setup>
/**
 * 弹幕层。
 *
 * <canvas> 之上用 DOM 逐条滚动？还是纯 canvas？
 * 这里选 DOM + transform：单条弹幕的生命周期短（8–12 秒），
 * 同时存在的条目一般不超过 60 条，DOM 完全够用，且文字渲染质量更好。
 *
 * 实现要点：
 *   - 5 条轨道（lane），按高度均分，新弹幕选当前最空的轨
 *   - requestAnimationFrame 统一驱动，避免每条一条 timer
 *   - 暂停时冻结（播放器暂停 → 弹幕停）
 *   - 透明度、字号、显示区域可配
 */
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'

const props = defineProps({
  /** 弹幕数组：[{id, text, offsetMs, color}] */
  list: { type: Array, default: () => [] },
  /** 当前播放时间（毫秒） */
  currentMs: { type: Number, default: 0 },
  /** 是否播放中 */
  playing: { type: Boolean, default: false },
  /** 是否启用 */
  enabled: { type: Boolean, default: true },
  /** 不透明度 0-100 */
  opacity: { type: Number, default: 80 },
  /** 字号 px */
  fontSize: { type: Number, default: 20 },
  /** 显示区域占播放器高度的比例 0-1 */
  area: { type: Number, default: 0.6 },
})

const emit = defineEmits(['send'])

/** 图层容器。 */
const layer = ref(null)
/** 轨道数。 */
const LANES = 5
/** 单条飞行时长（毫秒）。 */
const FLY_MS = 9000
/** 提前量：到点前多久开始飞（毫秒）。 */
const LEAD_MS = 200

/** 正在飞的弹幕：{key, text, color, lane, born, width}。 */
const flying = ref([])

/** 已发射过的弹幕 id，避免重复。 */
const fired = new Set()

/** 鼠标悬浮时暂停弹幕（不挡视线）。 */
const hovering = ref(false)

/** 轨道占用结束时间表。 */
const laneFreeAt = new Array(LANES).fill(0)

/** rAF 句柄。 */
let rafId = 0
/** 上一帧时间。 */
let lastTs = 0
/** 本地推进的时间（毫秒），只在 playing 时递增。 */
let localMs = props.currentMs

/** 自适应宽度：容器宽度决定起点和终点。 */
const layerWidth = ref(800)

/** 容器就绪后测量宽度。 */
function measure() {
  if (layer) layerWidth.value = layer.value.clientWidth || 800
}

let ro = null

onMounted(() => {
  measure()
  if (typeof ResizeObserver !== 'undefined' && layer.value) {
    ro = new ResizeObserver(measure)
    ro.observe(layer.value)
  }
  rafId = requestAnimationFrame(tick)
})

onBeforeUnmount(() => {
  if (rafId) cancelAnimationFrame(rafId)
  if (ro) ro.disconnect()
})

/** 外部时间跳变（拖动进度条）时重置。 */
watch(
  () => props.currentMs,
  (v) => {
    // 时间往回跳或跳跃超过 1.5 秒：清场
    if (v < localMs || Math.abs(v - localMs) > 1500) {
      flying.value = []
      fired.clear()
      laneFreeAt.fill(0)
      localMs = v
    } else {
      localMs = v
    }
  },
)

/** 列表换了（切集）：清场。 */
watch(
  () => props.list,
  () => {
    flying.value = []
    fired.clear()
    laneFreeAt.fill(0)
    localMs = props.currentMs
  },
)

/** 选一个最早空闲的轨道。 */
function pickLane(now) {
  let best = 0
  let bestAt = Infinity
  for (let i = 0; i < LANES; i++) {
    if (laneFreeAt[i] <= now) return i
    if (laneFreeAt[i] < bestAt) {
      bestAt = laneFreeAt[i]
      best = i
    }
  }
  return best
}

/** 主循环。 */
function tick(ts) {
  rafId = requestAnimationFrame(tick)
  const dt = lastTs ? ts - lastTs : 16
  lastTs = ts

  if (!props.enabled) {
    // 关闭时不推进，直接清空
    if (flying.value.length) flying.value = []
    return
  }

  // 悬浮暂停
  if (props.playing && !hovering.value) {
    localMs += dt
  }

  // 发射到点的弹幕
  if (props.playing && !hovering.value) {
    const now = localMs
    const batch = []
    for (const d of props.list) {
      const at = Number(d.offsetMs || 0)
      const key = d.id || `${at}-${d.text}`
      if (fired.has(key)) continue
      if (at <= now + LEAD_MS) {
        fired.add(key)
        const lane = pickLane(now)
        laneFreeAt[lane] = now + FLY_MS * 0.55 // 留出跟车间隔
        batch.push({
          key,
          text: d.text || '',
          color: d.color || '',
          lane,
          born: now,
        })
      }
    }
    if (batch.length) {
      // 单帧最多 6 条，避免突发刷屏
      flying.value = [...flying.value, ...batch.slice(0, 6)]
    }
  }

  // 回收飞出屏幕的
  if (flying.value.length) {
    const now = localMs
    const alive = flying.value.filter((f) => now - f.born < FLY_MS)
    if (alive.length !== flying.value.length) flying.value = alive
  }
}

/** 每条弹幕的样式：从右到左。 */
function styleOf(f) {
  const elapsed = Math.max(0, localMs - f.born)
  const p = Math.min(1, elapsed / FLY_MS)
  // 起点：容器右侧外；终点：左侧外
  const from = layerWidth.value + 40
  const to = -420
  const x = from + (to - from) * p
  const laneH = 100 / LANES
  return {
    transform: `translate3d(${x}px, ${f.lane * laneH}%, 0)`,
    color: f.color || undefined,
    opacity: props.opacity / 100,
  }
}

/** 本地发一条（发送后立即显示，不等后端回包）。 */
function pushLocal(text, color) {
  if (!text) return
  const now = localMs
  const lane = pickLane(now)
  laneFreeAt[lane] = now + FLY_MS * 0.55
  flying.value = [
    ...flying.value,
    { key: `local-${Date.now()}-${Math.random()}`, text, color: color || '', lane, born: now },
  ]
}

defineExpose({ pushLocal })

/** 弹幕输入。 */
const draft = ref('')
const showInput = ref(false)

function toggleInput() {
  showInput.value = !showInput.value
  if (showInput.value) nextTick(() => {
    const el = layer.value && layer.value.parentElement && layer.value.parentElement.querySelector('.dm-input')
    if (el) el.focus()
  })
}

function submit() {
  const text = draft.value.trim()
  if (!text) return
  emit('send', text)
  pushLocal(text, '')
  draft.value = ''
  showInput.value = false
}

const areaStyle = computed(() => ({ height: `${Math.round(props.area * 100)}%` }))
</script>

<template>
  <div
    class="dmlayer"
    :class="{ 'is-hover': hovering }"
    :style="areaStyle"
    @mouseenter="hovering = true"
    @mouseleave="hovering = false"
  >
    <div ref="layer" class="dm-track">
      <span
        v-for="f in flying"
        :key="f.key"
        class="dm-item"
        :style="[styleOf(f), { fontSize: fontSize + 'px' }]"
      >
        {{ f.text }}
      </span>
    </div>

    <!-- 发送入口 -->
    <div class="dm-bar" :class="{ 'is-open': showInput }">
      <template v-if="showInput">
        <input
          v-model="draft"
          class="dm-input"
          maxlength="40"
          placeholder="发条弹幕，回车发送"
          @keydown.enter.prevent="submit"
          @keydown.esc="showInput = false"
        />
        <button class="dm-send" @click="submit">发送</button>
      </template>
      <button v-else class="dm-toggle" title="发弹幕" @click="toggleInput">弹</button>
    </div>
  </div>
</template>

<style scoped>
.dmlayer {
  position: absolute;
  inset: 0 0 auto 0;
  pointer-events: none;
  overflow: hidden;
  z-index: 2;
}

.dm-track {
  position: absolute;
  inset: 0;
  pointer-events: none;
}

.dm-item {
  position: absolute;
  left: 0;
  top: 0;
  white-space: nowrap;
  font-weight: 500;
  line-height: 1.5;
  color: #fff;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.85), 0 0 4px rgba(0, 0, 0, 0.6);
  will-change: transform;
  /* 每帧由 rAF 更新 transform，这里不要加 transition */
}

/* ---- 发送条 ---- */
.dm-bar {
  position: absolute;
  right: var(--s-3);
  bottom: var(--s-3);
  display: flex;
  align-items: center;
  gap: var(--s-1);
  pointer-events: auto;
  transition: width var(--t-base);
}
.dm-bar.is-open {
  width: min(360px, 60%);
}
.dm-toggle {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  font-size: var(--f-2);
  backdrop-filter: blur(6px);
  transition: background var(--t-fast);
}
.dm-toggle:hover {
  background: rgba(0, 0, 0, 0.65);
}
.dm-input {
  flex: 1 1 auto;
  height: 30px;
  padding: 0 var(--s-3);
  font-size: var(--f-2);
  color: #fff;
  background: rgba(0, 0, 0, 0.55);
  border: 1px solid rgba(255, 255, 255, 0.18);
  border-radius: var(--r-full);
  backdrop-filter: blur(6px);
}
.dm-input::placeholder {
  color: rgba(255, 255, 255, 0.55);
}
.dm-input:focus {
  border-color: var(--c-brand);
  background: rgba(0, 0, 0, 0.7);
}
.dm-send {
  flex: 0 0 auto;
  height: 30px;
  padding: 0 var(--s-3);
  font-size: var(--f-2);
  color: #fff;
  background: var(--c-brand);
  border-radius: var(--r-full);
}
</style>

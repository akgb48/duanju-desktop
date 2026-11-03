<script setup>
/**
 * 选集列表。
 *
 * 两种排布：
 *   - grid：3:4 缩略图网格，用于详情页
 *   - list：横向列表，用于播放页侧栏
 *
 * 支持「付费集」标记与「当前集」高亮。
 */
import { computed, ref, watch } from 'vue'

const props = defineProps({
  /** Episode[] */
  episodes: { type: Array, default: () => [] },
  /** 当前集号（1 基） */
  current: { type: Number, default: 0 },
  /** grid | list */
  layout: { type: String, default: 'grid' },
  /** 是否显示下载按钮 */
  downloadable: { type: Boolean, default: false },
})

const emit = defineEmits(['select', 'download'])

/** 分块：每 30 集一段，避免超长列表。 */
const CHUNK = 30

/** 用户手动选的分块；null 表示跟随当前集。 */
const activeChunk = ref(null)

const chunks = computed(() => {
  const list = props.episodes || []
  const out = []
  for (let i = 0; i < list.length; i += CHUNK) {
    out.push({
      index: out.length,
      label: `${i + 1}-${Math.min(i + CHUNK, list.length)}`,
      items: list.slice(i, i + CHUNK),
    })
  }
  return out
})

/** 自动定位到当前集所在分块。 */
const currentChunk = computed(() => {
  if (!props.current || props.current < 1) return 0
  return Math.floor((props.current - 1) / CHUNK)
})

/** 实际生效的分块号。 */
const shownChunk = computed(() => {
  const c = chunks.value
  if (!c.length) return 0
  const want = activeChunk.value === null ? currentChunk.value : activeChunk.value
  return Math.min(Math.max(0, want), c.length - 1)
})

/** 实际显示的条目。 */
const visibleItems = computed(() => {
  const c = chunks.value
  if (!c.length) return []
  return c[shownChunk.value].items
})

/** 切集后重新跟随当前集。 */
watch(
  () => props.current,
  () => {
    activeChunk.value = null
  },
)

function pick(ep) {
  emit('select', ep)
}

function onDownload(ep, ev) {
  ev.stopPropagation()
  emit('download', ep)
}

/** 集号标签：竖排数字更省空间。 */
function epLabel(ep) {
  return ep.index || ep.Index || 0
}

/** 时长格式化：分:秒。 */
function formatDur(sec) {
  const s = Math.max(0, Math.floor(Number(sec) || 0))
  const m = Math.floor(s / 60)
  const r = s % 60
  return `${m}:${String(r).padStart(2, '0')}`
}
</script>

<template>
  <div class="eplist" :class="`is-${layout}`">
    <!-- 分块选择 -->
    <div v-if="chunks.length > 1" class="ep-chunks">
      <button
        v-for="c in chunks"
        :key="c.index"
        class="ep-chunk"
        :class="{ 'is-active': shownChunk === c.index }"
        @click="activeChunk = c.index"
      >
        {{ c.label }}
      </button>
    </div>

    <div v-if="!episodes.length" class="empty">
      <span class="empty-icon">▤</span>
      <span class="empty-text">暂无剧集</span>
    </div>

    <div v-else class="ep-items">
      <button
        v-for="ep in visibleItems"
        :key="ep.index"
        class="ep-item"
        :class="{ 'is-current': epLabel(ep) === current, 'is-locked': ep.isFree === false }"
        :title="ep.title || `第 ${epLabel(ep)} 集`"
        @click="pick(ep)"
      >
        <span class="ep-no t-num">{{ epLabel(ep) }}</span>

        <span v-if="layout === 'list'" class="ep-info">
          <span class="ep-title ellipsis">{{ ep.title || `第 ${epLabel(ep)} 集` }}</span>
          <span class="ep-sub">
            <span v-if="ep.duration" class="t-num">{{ formatDur(ep.duration) }}</span>
            <span v-if="ep.isFree === false" class="ep-vip">付费</span>
          </span>
        </span>

        <span v-if="layout === 'list' && epLabel(ep) === current" class="ep-playing">播放中</span>

        <span
          v-if="downloadable"
          class="ep-dl"
          role="button"
          tabindex="0"
          title="下载本集"
          @click="onDownload(ep, $event)"
          @keydown.enter="onDownload(ep, $event)"
        >
          ⤓
        </span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.eplist {
  display: flex;
  flex-direction: column;
  gap: var(--s-3);
  min-width: 0;
}

/* ---- 分块 ---- */
.ep-chunks {
  display: flex;
  gap: var(--s-1);
  flex-wrap: wrap;
}
.ep-chunk {
  height: 24px;
  padding: 0 var(--s-2);
  font-size: var(--f-1);
  color: var(--c-text-mute);
  border: 1px solid var(--c-line);
  border-radius: var(--r-1);
  transition: all var(--t-fast);
}
.ep-chunk:hover {
  color: var(--c-text);
  border-color: var(--c-line-strong);
}
.ep-chunk.is-active {
  color: var(--c-brand);
  border-color: var(--c-brand-line);
  background: var(--c-brand-dim);
}

/* ---- grid 布局 ---- */
.is-grid .ep-items {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(56px, 1fr));
  gap: var(--s-2);
}
.is-grid .ep-item {
  position: relative;
  aspect-ratio: 3 / 4;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--r-2);
  background: var(--c-bg-hover);
  border: 1px solid transparent;
  color: var(--c-text-dim);
  font-family: var(--font-num);
  font-size: var(--f-4);
  transition: all var(--t-fast);
}
.is-grid .ep-item:hover {
  background: var(--c-bg-active);
  color: var(--c-text);
}
.is-grid .ep-item.is-current {
  background: var(--c-brand-dim);
  border-color: var(--c-brand);
  color: var(--c-brand);
  font-weight: 600;
}
.is-grid .ep-item.is-locked {
  color: var(--c-text-mute);
}
.is-grid .ep-item.is-locked::after {
  content: '🔒';
  position: absolute;
  right: 3px;
  top: 3px;
  font-size: 8px;
  opacity: 0.7;
}

/* ---- list 布局 ---- */
.is-list .ep-items {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.is-list .ep-item {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  width: 100%;
  height: 44px;
  padding: 0 var(--s-2);
  border-radius: var(--r-2);
  text-align: left;
  color: var(--c-text-dim);
  transition: background var(--t-fast), color var(--t-fast);
}
.is-list .ep-item:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.is-list .ep-item.is-current {
  background: var(--c-brand-dim);
  color: var(--c-brand);
}
.is-list .ep-no {
  flex: 0 0 auto;
  width: 26px;
  text-align: center;
  font-size: var(--f-2);
  font-weight: 600;
}
.is-list .ep-info {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.3;
}
.is-list .ep-title {
  font-size: var(--f-2);
}
.is-list .ep-sub {
  display: flex;
  gap: var(--s-2);
  font-size: var(--f-1);
  color: var(--c-text-mute);
}
.is-list .ep-vip {
  color: var(--c-accent);
}
.is-list .ep-playing {
  flex: 0 0 auto;
  font-size: var(--f-1);
  color: var(--c-brand);
}

/* ---- 下载按钮 ---- */
.ep-dl {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: var(--r-1);
  font-size: var(--f-3);
  color: var(--c-text-mute);
  opacity: 0;
  transition: opacity var(--t-fast), background var(--t-fast), color var(--t-fast);
}
.ep-item:hover .ep-dl {
  opacity: 1;
}
.ep-dl:hover {
  background: var(--c-brand-dim);
  color: var(--c-brand);
}
.is-grid .ep-dl {
  position: absolute;
  right: 2px;
  bottom: 2px;
  width: 16px;
  height: 16px;
  font-size: var(--f-1);
}
</style>

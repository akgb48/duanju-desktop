<script setup>
/**
 * 观看历史。
 *
 * 每条显示：封面、标题、进度条、上次看到第几集、时间。
 * 支持单条删除与清空。
 */
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import * as api from '../api/bridge'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const ui = useUiStore()

const items = ref([])
const cursor = ref('')
const hasMore = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')

async function load(reset = true) {
  if (reset) {
    loading.value = true
    cursor.value = ''
    error.value = ''
  } else {
    if (!hasMore.value || loadingMore.value) return
    loadingMore.value = true
  }
  try {
    const r = await api.History(reset ? '' : cursor.value)
    const list = (r && (r.items || r.Items)) || []
    items.value = reset ? list : [...items.value, ...list]
    hasMore.value = !!(r && r.hasMore)
    cursor.value = (r && r.cursor) || ''
  } catch (e) {
    error.value = e.message || '加载历史失败'
    if (reset) items.value = []
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function play(it) {
  const sid = it.seriesId
  const ep = it.episode || 1
  if (sid) router.push({ name: 'play', params: { seriesId: sid, videoId: String(ep) } })
}

async function removeOne(it, ev) {
  ev.stopPropagation()
  const ok = await ui.confirm({
    title: '删除记录',
    text: `确定删除「${it.title || '这条记录'}」？`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await api.DeleteHistory(it.seriesId)
    items.value = items.value.filter((x) => x.seriesId !== it.seriesId)
    ui.toastOk('已删除')
  } catch (e) {
    ui.toastErr(e.message || '删除失败')
  }
}

async function clearAll() {
  const ok = await ui.confirm({
    title: '清空观看历史',
    text: '将删除全部观看记录，无法恢复。',
    confirmText: '清空',
    danger: true,
  })
  if (!ok) return
  try {
    // 后端 DeleteHistory(seriesId) 按条删除，空串表示全部
    await api.DeleteHistory('')
    items.value = []
    ui.toastOk('已清空')
  } catch (e) {
    ui.toastErr(e.message || '清空失败')
  }
}

/** 进度百分比。 */
function pct(it) {
  const pos = Number(it.positionMs || 0)
  const dur = Number(it.durationMs || 0)
  if (!dur) return 0
  return Math.min(100, Math.max(0, (pos / dur) * 100))
}

/** 剩余时长文本。 */
function progressText(it) {
  const pos = Number(it.positionMs || 0)
  const dur = Number(it.durationMs || 0)
  if (!dur) return '未开始'
  const left = Math.max(0, dur - pos)
  const m = Math.floor(left / 60000)
  if (left < 60000) return '即将看完'
  return `剩余 ${m} 分钟`
}

function relTime(ts) {
  if (!ts) return ''
  const d = Math.floor(Date.now() / 1000) - Number(ts)
  if (d < 3600) return `${Math.max(1, Math.floor(d / 60))} 分钟前`
  if (d < 86400) return `${Math.floor(d / 3600)} 小时前`
  return `${Math.floor(d / 86400)} 天前`
}

onMounted(() => load(true))
</script>

<template>
  <div class="v-root">
    <div class="v-head">
      <div>
        <h1>观看历史</h1>
        <p class="v-head-sub">接着上次看到的地方继续</p>
      </div>
      <button class="btn btn-sm" :disabled="!items.length" @click="clearAll">清空</button>
    </div>

    <div class="v-body">
      <div v-if="error" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ error }}</span>
        <button class="btn btn-sm" @click="load(true)">重试</button>
      </div>

      <div v-else-if="loading" class="hi-loading">
        <span class="spinner" />
      </div>

      <div v-else-if="!items.length" class="empty">
        <span class="empty-icon">◷</span>
        <span class="empty-text">还没有观看记录</span>
        <button class="btn btn-sm" @click="router.push({ name: 'recommend' })">去看看</button>
      </div>

      <template v-else>
        <ul class="hi-list">
          <li v-for="it in items" :key="it.seriesId" class="hi-item" @click="play(it)">
            <div class="hi-cover">
              <img v-if="it.cover" :src="it.cover" :alt="it.title" loading="lazy" />
              <div v-else class="hi-cover-ph">{{ (it.title || '?').slice(0, 1) }}</div>
              <span class="hi-progress-bar">
                <span class="hi-progress-fill" :style="{ width: pct(it) + '%' }" />
              </span>
            </div>

            <div class="hi-main">
              <h3 class="hi-title ellipsis">{{ it.title || '未命名' }}</h3>
              <p class="hi-sub mute t-f2">
                看到第 {{ it.episode || 1 }} 集 · {{ progressText(it) }}
              </p>
              <p class="hi-time mute t-f1">{{ relTime(it.updatedAt) }}</p>
            </div>

            <button class="hi-del" title="删除这条记录" @click="removeOne(it, $event)">✕</button>
          </li>
        </ul>

        <div class="v-more">
          <button v-if="hasMore" class="btn btn-sm" :disabled="loadingMore" @click="load(false)">
            {{ loadingMore ? '加载中…' : '加载更多' }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.hi-loading {
  display: flex;
  justify-content: center;
  padding: var(--s-9);
}

.hi-list {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}

.hi-item {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  padding: var(--s-2);
  border-radius: var(--r-2);
  cursor: pointer;
  transition: background var(--t-fast);
}
.hi-item:hover {
  background: var(--c-bg-hover);
}

.hi-cover {
  position: relative;
  flex: 0 0 auto;
  width: 108px;
  aspect-ratio: 16 / 9;
  border-radius: var(--r-2);
  overflow: hidden;
  background: var(--c-bg-active);
}
.hi-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.hi-cover-ph {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--f-6);
  font-weight: 700;
  color: rgba(255, 255, 255, 0.3);
  background: linear-gradient(140deg, #2a2f38, #171a20);
}

.hi-progress-bar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 3px;
  background: rgba(0, 0, 0, 0.5);
}
.hi-progress-fill {
  display: block;
  height: 100%;
  background: var(--c-brand);
}

.hi-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.hi-title {
  font-size: var(--f-4);
  font-weight: 500;
}
.hi-sub,
.hi-time {
  line-height: 1.4;
}

.hi-del {
  flex: 0 0 auto;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  color: var(--c-text-mute);
  font-size: var(--f-2);
  opacity: 0;
  transition: opacity var(--t-fast), background var(--t-fast), color var(--t-fast);
}
.hi-item:hover .hi-del {
  opacity: 1;
}
.hi-del:hover {
  background: var(--c-err-dim);
  color: var(--c-err);
}
</style>

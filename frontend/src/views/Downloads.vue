<script setup>
/**
 * 下载管理。
 *
 * 列表按状态分 tab：全部 / 下载中 / 已暂停 / 已完成 / 失败。
 * 实时进度由 download store 订阅 download:task 事件推动。
 */
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { useDownloadStore } from '../stores/download'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const downloader = useDownloadStore()
const ui = useUiStore()

const tab = ref('all') // all | active | paused | done | failed
const selected = ref(new Set())
const selecting = ref(false)

const TABS = [
  { id: 'all', label: '全部' },
  { id: 'active', label: '下载中' },
  { id: 'paused', label: '已暂停' },
  { id: 'done', label: '已完成' },
  { id: 'failed', label: '失败' },
]

/** 当前 tab 的列表。 */
const shown = computed(() => {
  switch (tab.value) {
    case 'active':
      return downloader.active
    case 'paused':
      return downloader.paused
    case 'done':
      return downloader.done
    case 'failed':
      return downloader.failed
    default:
      return downloader.tasks
  }
})

/** 各 tab 计数。 */
function countOf(id) {
  if (id === 'active') return downloader.active.length
  if (id === 'paused') return downloader.paused.length
  if (id === 'done') return downloader.done.length
  if (id === 'failed') return downloader.failed.length
  return downloader.tasks.length
}

/** 单条任务 key。 */
function keyOf(t) {
  return t.videoId || t.id || ''
}

/** 进度百分比。 */
function pct(t) {
  const got = Number(t.receivedBytes || t.received || 0)
  const total = Number(t.totalBytes || t.total || 0)
  if (!total) return t.state === 'done' ? 100 : 0
  return Math.min(100, Math.max(0, (got / total) * 100))
}

/** 大小格式化。 */
function fmtSize(bytes) {
  const b = Number(bytes) || 0
  if (b <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = b
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

/** 速度格式化。 */
function fmtSpeed(t) {
  const sp = Number(t.speed || t.speedBps || 0)
  if (sp <= 0) {
    return t.state === 'paused' ? '已暂停' : t.state === 'done' ? '已完成' : '—'
  }
  return `${fmtSize(sp)}/s`
}

/** 状态标签。 */
const STATE_LABEL = {
  downloading: '下载中',
  pending: '等待中',
  paused: '已暂停',
  done: '已完成',
  error: '失败',
}

const STATE_TAG = {
  downloading: 'tag-info',
  pending: '',
  paused: 'tag-warn',
  done: 'tag-ok',
  error: 'tag-err',
}

function toggleSelect(k) {
  const s = new Set(selected.value)
  if (s.has(k)) s.delete(k)
  else s.add(k)
  selected.value = s
}

function toggleSelectMode() {
  selecting.value = !selecting.value
  selected.value = new Set()
}

const selectedIds = computed(() => [...selected.value])

async function pauseOne(t) {
  const r = await downloader.pause([keyOf(t)])
  if (!r.ok) ui.toastErr(r.error)
}

async function resumeOne(t) {
  const r = await downloader.resume([keyOf(t)])
  if (!r.ok) ui.toastErr(r.error)
}

async function removeOne(t) {
  const ok = await ui.confirm({
    title: '删除任务',
    text: `删除「${t.title || '该任务'}」？已下载的文件不会自动清除。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  const r = await downloader.remove([keyOf(t)])
  if (r.ok) ui.toastOk('已删除')
  else ui.toastErr(r.error)
}

async function pauseAll() {
  const ids = downloader.active.map(keyOf).filter(Boolean)
  if (!ids.length) return
  const r = await downloader.pause(ids)
  if (r.ok) ui.toastOk('已全部暂停')
  else ui.toastErr(r.error)
}

async function resumeAll() {
  const ids = downloader.paused.map(keyOf).filter(Boolean)
  if (!ids.length) return
  const r = await downloader.resume(ids)
  if (r.ok) ui.toastOk('已全部继续')
  else ui.toastErr(r.error)
}

async function removeSelected() {
  const ids = selectedIds.value
  if (!ids.length) {
    ui.toastWarn('请先选择任务')
    return
  }
  const ok = await ui.confirm({
    title: '批量删除',
    text: `删除 ${ids.length} 个任务？`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  const r = await downloader.remove(ids)
  if (r.ok) {
    selected.value = new Set()
    selecting.value = false
    ui.toastOk('已删除')
  } else {
    ui.toastErr(r.error)
  }
}

function playTask(t) {
  if (t.seriesId) {
    router.push({
      name: 'play',
      params: { seriesId: t.seriesId, videoId: String(t.episode || 1) },
    })
  }
}

onMounted(() => {
  downloader.subscribe()
  downloader.refresh()
})

onBeforeUnmount(() => {
  // 不在这里 unsubscribe：其它页面（Play）也在用同一个 store 的订阅
})
</script>

<template>
  <div class="v-root">
    <div class="v-head">
      <div>
        <h1>下载管理</h1>
        <p class="v-head-sub">
          共 {{ downloader.tasks.length }} 个任务
          <span v-if="downloader.active.length"> · {{ downloader.active.length }} 个进行中</span>
        </p>
      </div>
      <div class="dl-acts">
        <template v-if="selecting">
          <span class="mute t-f2">已选 {{ selectedIds.length }}</span>
          <button class="btn btn-sm btn-danger" :disabled="!selectedIds.length" @click="removeSelected">
            删除
          </button>
          <button class="btn btn-sm" @click="toggleSelectMode">取消</button>
        </template>
        <template v-else>
          <button class="btn btn-sm" :disabled="!downloader.active.length" @click="pauseAll">全部暂停</button>
          <button class="btn btn-sm" :disabled="!downloader.paused.length" @click="resumeAll">全部继续</button>
          <button class="btn btn-sm btn-ghost" @click="downloader.openDir('')">打开目录</button>
          <button class="btn btn-sm" :disabled="!downloader.tasks.length" @click="toggleSelectMode">管理</button>
        </template>
      </div>
    </div>

    <div class="v-body">
      <!-- 总进度 -->
      <div v-if="downloader.active.length" class="dl-overall">
        <div class="dl-overall-bar">
          <span :style="{ width: downloader.overall * 100 + '%' }" />
        </div>
        <span class="dl-overall-text mute t-f1 t-num">
          {{ Math.round(downloader.overall * 100) }}%
        </span>
      </div>

      <!-- tabs -->
      <div class="v-tabs">
        <button
          v-for="t in TABS"
          :key="t.id"
          class="v-tab"
          :class="{ 'is-active': tab === t.id }"
          @click="tab = t.id"
        >
          {{ t.label }}
          <span class="t-num mute">{{ countOf(t.id) }}</span>
        </button>
      </div>

      <!-- 列表 -->
      <div v-if="downloader.error" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ downloader.error }}</span>
        <button class="btn btn-sm" @click="downloader.refresh()">重试</button>
      </div>

      <div v-else-if="downloader.loading && !downloader.tasks.length" class="dl-loading">
        <span class="spinner" />
      </div>

      <div v-else-if="!shown.length" class="empty">
        <span class="empty-icon">⤓</span>
        <span class="empty-text">
          {{ tab === 'all' ? '还没有下载任务' : '这个分类下没有任务' }}
        </span>
        <button class="btn btn-sm" @click="router.push({ name: 'recommend' })">去找剧</button>
      </div>

      <ul v-else class="dl-list">
        <li
          v-for="t in shown"
          :key="keyOf(t)"
          class="dl-item"
          :class="{ 'is-selected': selected.has(keyOf(t)) }"
        >
          <button
            v-if="selecting"
            class="dl-check"
            :class="{ 'is-on': selected.has(keyOf(t)) }"
            @click="toggleSelect(keyOf(t))"
          >
            {{ selected.has(keyOf(t)) ? '✓' : '' }}
          </button>

          <div class="dl-cover" @click="playTask(t)">
            <img v-if="t.cover" :src="t.cover" :alt="t.title" loading="lazy" />
            <span v-else class="dl-cover-ph">{{ (t.title || '?').slice(0, 1) }}</span>
          </div>

          <div class="dl-main">
            <div class="dl-title-row">
              <h3 class="dl-title ellipsis">{{ t.title || '未命名' }}</h3>
              <span class="tag" :class="STATE_TAG[t.state] || ''">
                {{ STATE_LABEL[t.state] || t.state || '未知' }}
              </span>
            </div>

            <p class="dl-sub mute t-f1">
              <span v-if="t.episode">第 {{ t.episode }} 集</span>
              <span v-if="t.quality"> · {{ t.quality }}</span>
              <span v-if="t.totalBytes || t.total"> · {{ fmtSize(t.totalBytes || t.total) }}</span>
              <span v-if="t.errorMsg" class="dl-err-msg"> · {{ t.errorMsg }}</span>
            </p>

            <div class="dl-progress">
              <div class="dl-bar">
                <span
                  class="dl-bar-fill"
                  :class="{ 'is-done': t.state === 'done', 'is-err': t.state === 'error' }"
                  :style="{ width: pct(t) + '%' }"
                />
              </div>
              <span class="dl-pct t-num">{{ Math.round(pct(t)) }}%</span>
              <span class="dl-speed t-num mute t-f1">{{ fmtSpeed(t) }}</span>
            </div>
          </div>

          <div class="dl-row-acts">
            <button
              v-if="t.state === 'downloading' || t.state === 'pending'"
              class="btn btn-sm btn-ghost"
              @click="pauseOne(t)"
            >
              暂停
            </button>
            <button
              v-else-if="t.state === 'paused' || t.state === 'error'"
              class="btn btn-sm btn-ghost"
              @click="resumeOne(t)"
            >
              {{ t.state === 'error' ? '重试' : '继续' }}
            </button>
            <button
              v-else-if="t.state === 'done'"
              class="btn btn-sm btn-ghost"
              @click="playTask(t)"
            >
              播放
            </button>
            <button class="btn btn-sm btn-ghost" title="删除" @click="removeOne(t)">✕</button>
          </div>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.dl-acts {
  display: flex;
  align-items: center;
  gap: var(--s-2);
}

/* ---- 总进度 ---- */
.dl-overall {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  margin-bottom: var(--s-3);
}
.dl-overall-bar {
  flex: 1 1 auto;
  height: 4px;
  border-radius: var(--r-full);
  background: var(--c-bg-active);
  overflow: hidden;
}
.dl-overall-bar span {
  display: block;
  height: 100%;
  background: var(--c-brand);
  transition: width var(--t-base);
}
.dl-overall-text {
  flex: 0 0 auto;
}

.dl-loading {
  display: flex;
  justify-content: center;
  padding: var(--s-9);
}

/* ---- 列表 ---- */
.dl-list {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}

.dl-item {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  padding: var(--s-2);
  border-radius: var(--r-2);
  transition: background var(--t-fast);
}
.dl-item:hover {
  background: var(--c-bg-hover);
}
.dl-item.is-selected {
  background: var(--c-brand-dim);
}

.dl-check {
  flex: 0 0 auto;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 1.5px solid var(--c-line-strong);
  color: #fff;
  font-size: var(--f-1);
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background var(--t-fast), border-color var(--t-fast);
}
.dl-check.is-on {
  background: var(--c-brand);
  border-color: var(--c-brand);
}

.dl-cover {
  flex: 0 0 auto;
  width: 72px;
  aspect-ratio: 16 / 9;
  border-radius: var(--r-2);
  overflow: hidden;
  background: var(--c-bg-active);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}
.dl-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.dl-cover-ph {
  font-size: var(--f-5);
  font-weight: 700;
  color: rgba(255, 255, 255, 0.3);
}

.dl-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.dl-title-row {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  min-width: 0;
}
.dl-title {
  font-size: var(--f-3);
  font-weight: 500;
  min-width: 0;
}

.dl-sub {
  display: flex;
  flex-wrap: wrap;
}
.dl-err-msg {
  color: var(--c-err);
}

.dl-progress {
  display: flex;
  align-items: center;
  gap: var(--s-2);
}
.dl-bar {
  flex: 1 1 auto;
  height: 3px;
  border-radius: var(--r-full);
  background: var(--c-bg-active);
  overflow: hidden;
  max-width: 320px;
}
.dl-bar-fill {
  display: block;
  height: 100%;
  background: var(--c-brand);
  transition: width 240ms linear;
}
.dl-bar-fill.is-done {
  background: var(--c-ok);
}
.dl-bar-fill.is-err {
  background: var(--c-err);
}
.dl-pct {
  flex: 0 0 auto;
  font-size: var(--f-1);
  color: var(--c-text-dim);
  min-width: 34px;
}
.dl-speed {
  flex: 0 0 auto;
  min-width: 68px;
}

.dl-row-acts {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--s-1);
  opacity: 0;
  transition: opacity var(--t-fast);
}
.dl-item:hover .dl-row-acts {
  opacity: 1;
}
</style>

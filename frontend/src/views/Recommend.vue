<script setup>
/**
 * 首页信息流。
 *
 * tab 来自 FeedCategories，每换一个 tab 重新走 RecommendTab。
 * 分页用游标，滚到底部自动加载（IntersectionObserver）。
 */
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import VideoCard from '../components/VideoCard.vue'
import SearchBar from '../components/SearchBar.vue'
import * as api from '../api/bridge'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const ui = useUiStore()

/** tab 列表。 */
const tabs = ref([])
/** 当前 tab id。 */
const activeTab = ref('')
/** 卡片列表。 */
const items = ref([])
/** 游标。 */
const cursor = ref('')
const hasMore = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')

/** 底部哨兵。 */
const sentinel = ref(null)
let io = null

async function loadTabs() {
  try {
    const r = await api.FeedCategories()
    const list = Array.isArray(r) ? r : (r && r.items) || []
    tabs.value = list.map((x, i) => ({
      id: String(x.id || x.type || i),
      name: x.name || x.title || `分类${i + 1}`,
    }))
    if (!tabs.value.length) {
      // 后端没给 tab 也要能用
      tabs.value = [{ id: '0', name: '推荐' }]
    }
    activeTab.value = tabs.value[0].id
  } catch (e) {
    tabs.value = [{ id: '0', name: '推荐' }]
    activeTab.value = '0'
  }
}

/** 拉第一页。 */
async function load(reset = true) {
  if (loading.value) return
  if (reset) {
    loading.value = true
    error.value = ''
    cursor.value = ''
  } else {
    if (!hasMore.value || loadingMore.value) return
    loadingMore.value = true
  }

  try {
    const r = await api.RecommendTab(Number(activeTab.value) || 0, reset ? '' : cursor.value)
    const list = (r && (r.items || r.Items)) || []
    items.value = reset ? list : [...items.value, ...list]
    hasMore.value = !!(r && (r.hasMore || r.HasMore))
    cursor.value = (r && (r.cursor || r.Cursor)) || ''
  } catch (e) {
    error.value = e.message || '加载失败'
    if (reset) items.value = []
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function switchTab(id) {
  if (activeTab.value === id) return
  activeTab.value = id
  items.value = []
  load(true)
}

function openSeries(item) {
  const id = item.seriesId || item.id
  if (!id) return
  router.push({ name: 'series', params: { id } })
}

function onSearch(kw) {
  router.push({ name: 'search', query: { q: kw } })
}

/** 滚动加载。 */
function setupObserver() {
  if (typeof IntersectionObserver === 'undefined' || !sentinel.value) return
  io = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting)) load(false)
    },
    { rootMargin: '240px' },
  )
  io.observe(sentinel.value)
}

async function init() {
  await loadTabs()
  await load(true)
  await nextTick()
  setupObserver()
}

onMounted(init)

onBeforeUnmount(() => {
  if (io) io.disconnect()
})

/** 刷新按钮。 */
function refresh() {
  ui.toast('已刷新', 'info', 1200)
  load(true)
}
</script>

<template>
  <div class="v-root home">
    <!-- 顶部：搜索 + tabs -->
    <header class="home-head">
      <div class="home-search">
        <SearchBar placeholder="搜剧名、类型、作者" @search="onSearch" />
      </div>
      <button class="btn btn-sm btn-ghost" title="刷新" @click="refresh">↻</button>
    </header>

    <div class="home-tabs">
      <button
        v-for="t in tabs"
        :key="t.id"
        class="home-tab"
        :class="{ 'is-active': t.id === activeTab }"
        @click="switchTab(t.id)"
      >
        {{ t.name }}
      </button>
    </div>

    <!-- 内容 -->
    <div class="v-body">
      <div v-if="error && !items.length" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ error }}</span>
        <button class="btn btn-sm" @click="load(true)">重试</button>
      </div>

      <!-- 骨架 -->
      <div v-else-if="loading" class="grid-cards">
        <div v-for="i in 12" :key="i" class="sk-card">
          <div class="skeleton sk-cover" />
          <div class="skeleton sk-line" style="width: 78%" />
          <div class="skeleton sk-line" style="width: 42%" />
        </div>
      </div>

      <div v-else-if="!items.length" class="empty">
        <span class="empty-icon">▤</span>
        <span class="empty-text">这个分类暂时没有内容</span>
      </div>

      <template v-else>
        <div class="grid-cards">
          <VideoCard
            v-for="it in items"
            :key="it.seriesId || it.id"
            :item="it"
            @click="openSeries(it)"
          />
        </div>

        <div ref="sentinel" class="v-more">
          <span v-if="loadingMore" class="spinner" />
          <span v-else-if="!hasMore" class="mute t-f2">没有更多了</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.home {
  overflow: hidden;
}

.home-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--s-2);
  padding: var(--s-4) var(--s-6) var(--s-2);
}
.home-search {
  flex: 1 1 auto;
  max-width: 520px;
}

.home-tabs {
  flex: 0 0 auto;
  display: flex;
  gap: var(--s-1);
  padding: 0 var(--s-6) var(--s-3);
  overflow-x: auto;
  scrollbar-width: none;
}
.home-tabs::-webkit-scrollbar {
  display: none;
}
.home-tab {
  flex: 0 0 auto;
  height: 30px;
  padding: 0 var(--s-3);
  border-radius: var(--r-full);
  font-size: var(--f-3);
  color: var(--c-text-dim);
  transition: background var(--t-fast), color var(--t-fast);
}
.home-tab:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.home-tab.is-active {
  background: var(--c-brand-dim);
  color: var(--c-brand);
  font-weight: 600;
}

/* 骨架卡 */
.sk-card {
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}
.sk-cover {
  aspect-ratio: 3 / 4;
  border-radius: var(--r-3);
}
.sk-line {
  height: 12px;
}
</style>

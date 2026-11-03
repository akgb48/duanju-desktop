<script setup>
/**
 * 榜单页。
 *
 * 左侧榜单维度（RankPanel），右侧榜单内容（Rank）。
 * 维度切换后清空重拉。
 */
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import VideoCard from '../components/VideoCard.vue'
import * as api from '../api/bridge'

const router = useRouter()

const panels = ref([])
const activePanel = ref('')
const items = ref([])
const cursor = ref('')
const hasMore = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')

async function loadPanels() {
  try {
    const r = await api.RankPanel()
    const list = Array.isArray(r) ? r : (r && r.items) || []
    panels.value = list.map((x, i) => ({
      id: String(x.id || x.type || i),
      name: x.name || x.title || `榜单${i + 1}`,
    }))
    if (!panels.value.length) panels.value = [{ id: '0', name: '热播榜' }]
    activePanel.value = panels.value[0].id
  } catch {
    panels.value = [{ id: '0', name: '热播榜' }]
    activePanel.value = '0'
  }
}

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
    const r = await api.Rank(Number(activePanel.value) || 0, reset ? '' : cursor.value)
    const list = (r && (r.items || r.Items)) || []
    items.value = reset ? list : [...items.value, ...list]
    hasMore.value = !!(r && r.hasMore)
    cursor.value = (r && r.cursor) || ''
  } catch (e) {
    error.value = e.message || '加载榜单失败'
    if (reset) items.value = []
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function switchPanel(id) {
  if (activePanel.value === id) return
  activePanel.value = id
  items.value = []
  load(true)
}

function openSeries(it) {
  const id = it.seriesId || it.id
  if (id) router.push({ name: 'series', params: { id } })
}

onMounted(async () => {
  await loadPanels()
  await load(true)
})
</script>

<template>
  <div class="v-root">
    <div class="v-head">
      <div>
        <h1>榜单</h1>
        <p class="v-head-sub">看看大家都在追什么</p>
      </div>
    </div>

    <div class="rank-body">
      <!-- 维度 -->
      <aside class="rank-panels">
        <button
          v-for="p in panels"
          :key="p.id"
          class="rank-panel"
          :class="{ 'is-active': p.id === activePanel }"
          @click="switchPanel(p.id)"
        >
          {{ p.name }}
        </button>
      </aside>

      <!-- 内容 -->
      <div class="v-body rank-content">
        <div v-if="error && !items.length" class="empty">
          <span class="empty-icon">⚠</span>
          <span class="empty-text">{{ error }}</span>
          <button class="btn btn-sm" @click="load(true)">重试</button>
        </div>

        <div v-else-if="loading" class="grid-cards">
          <div v-for="i in 10" :key="i" class="sk-card">
            <div class="skeleton sk-cover" />
            <div class="skeleton sk-line" style="width: 70%" />
          </div>
        </div>

        <div v-else-if="!items.length" class="empty">
          <span class="empty-icon">≡</span>
          <span class="empty-text">榜单暂时为空</span>
        </div>

        <template v-else>
          <div class="grid-cards">
            <VideoCard
              v-for="(it, i) in items"
              :key="it.seriesId || it.id || i"
              :item="it"
              @click="openSeries(it)"
            />
          </div>
          <div class="v-more">
            <button v-if="hasMore" class="btn btn-sm" :disabled="loadingMore" @click="load(false)">
              {{ loadingMore ? '加载中…' : '加载更多' }}
            </button>
            <span v-else class="mute t-f2">没有更多了</span>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rank-body {
  flex: 1 1 auto;
  display: flex;
  min-height: 0;
}

.rank-panels {
  flex: 0 0 auto;
  width: 132px;
  padding: var(--s-2) var(--s-3) var(--s-3) var(--s-6);
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
  overflow-y: auto;
}
.rank-panel {
  height: 34px;
  padding: 0 var(--s-3);
  border-radius: var(--r-2);
  font-size: var(--f-3);
  color: var(--c-text-dim);
  text-align: left;
  transition: background var(--t-fast), color var(--t-fast);
  white-space: nowrap;
}
.rank-panel:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.rank-panel.is-active {
  background: var(--c-brand-dim);
  color: var(--c-brand);
  font-weight: 600;
}

.rank-content {
  padding-left: var(--s-4);
}

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

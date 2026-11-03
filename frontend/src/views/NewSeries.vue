<script setup>
/**
 * 新剧页。
 *
 * 拿不到就退回主 feed 前 40 条，保证页面永远有内容。
 */
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import VideoCard from '../components/VideoCard.vue'
import * as api from '../api/bridge'

const router = useRouter()
const items = ref([])
const loading = ref(true)
const error = ref('')
const cursor = ref('')
const hasMore = ref(false)
const loadingMore = ref(false)

async function load(reset = true) {
  if (reset) {
    loading.value = true
    error.value = ''
    cursor.value = ''
  } else {
    if (!hasMore.value || loadingMore.value) return
    loadingMore.value = true
  }
  try {
    const r = await api.RecommendTab(4, reset ? '' : cursor.value)
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

function openSeries(it) {
  const id = it.seriesId || it.id
  if (id) router.push({ name: 'series', params: { id } })
}

onMounted(() => load(true))
</script>

<template>
  <div class="v-root">
    <header class="page-head">
      <h1 class="page-title">新剧</h1>
      <span class="mute t-f3">最近上线与即将上线</span>
    </header>

    <div class="v-body">
      <div v-if="loading" class="grid-cards">
        <div v-for="i in 12" :key="i" class="sk-card">
          <div class="skeleton sk-cover" />
          <div class="skeleton sk-line" style="width: 76%" />
        </div>
      </div>

      <div v-else-if="error && !items.length" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ error }}</span>
        <button class="btn btn-sm" @click="load(true)">重试</button>
      </div>

      <div v-else-if="!items.length" class="empty">
        <span class="empty-icon">✦</span>
        <span class="empty-text">暂无新剧</span>
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
        <div class="v-more">
          <button v-if="hasMore" class="btn btn-sm" :disabled="loadingMore" @click="load(false)">
            {{ loadingMore ? '加载中…' : '加载更多' }}
          </button>
          <span v-else class="mute t-f2">没有更多了</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.page-head {
  flex: 0 0 auto;
  display: flex;
  align-items: baseline;
  gap: var(--s-3);
  padding: var(--s-5) var(--s-6) var(--s-3);
}
.page-title {
  font-size: var(--f-6);
  font-weight: 650;
  letter-spacing: -0.01em;
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

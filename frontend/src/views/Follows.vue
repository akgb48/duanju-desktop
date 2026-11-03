<script setup>
/**
 * 追更页。
 *
 * 数据源 FollowUpdates（follow_update_list），只有登录后有内容。
 */
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import VideoCard from '../components/VideoCard.vue'
import * as api from '../api/bridge'
import { useAccountStore } from '../stores/account'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const account = useAccountStore()
const ui = useUiStore()

const items = ref([])
const loading = ref(true)
const error = ref('')
const cursor = ref('')
const hasMore = ref(false)
const loadingMore = ref(false)

const loggedIn = !!(account.user && (account.user.userId || account.user.UserID))

async function load(reset = true) {
  if (!loggedIn) {
    loading.value = false
    return
  }
  if (reset) {
    loading.value = true
    error.value = ''
    cursor.value = ''
  } else {
    if (!hasMore.value || loadingMore.value) return
    loadingMore.value = true
  }
  try {
    const r = await api.FollowUpdates(reset ? '' : cursor.value)
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

async function unfollow(it, ev) {
  ev.stopPropagation()
  const id = it.seriesId || it.id
  try {
    await api.SetFollowUpdate(id, false)
    items.value = items.value.filter((x) => (x.seriesId || x.id) !== id)
    ui.toast('已取消追更', 'success')
  } catch (e) {
    ui.toast(e.message || '取消追更失败', 'error')
  }
}

onMounted(() => load(true))
</script>

<template>
  <div class="v-root">
    <header class="page-head">
      <h1 class="page-title">追更</h1>
    </header>

    <div class="v-body">
      <div v-if="!loggedIn" class="empty">
        <span class="empty-icon">♡</span>
        <span class="empty-text">登录后可以追更</span>
        <button class="btn btn-sm btn-primary" @click="router.push('/login')">去登录</button>
      </div>

      <div v-else-if="loading" class="grid-cards">
        <div v-for="i in 8" :key="i" class="sk-card">
          <div class="skeleton sk-cover" />
          <div class="skeleton sk-line" style="width: 70%" />
        </div>
      </div>

      <div v-else-if="error && !items.length" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ error }}</span>
        <button class="btn btn-sm" @click="load(true)">重试</button>
      </div>

      <div v-else-if="!items.length" class="empty">
        <span class="empty-icon">♡</span>
        <span class="empty-text">还没有追更的剧</span>
        <button class="btn btn-sm" @click="router.push('/recommend')">去找剧</button>
      </div>

      <template v-else>
        <div class="grid-cards">
          <div v-for="it in items" :key="it.seriesId || it.id" class="fl-cell">
            <VideoCard :item="it" @click="openSeries(it)" />
            <button class="fl-off btn btn-sm btn-ghost" @click="unfollow(it, $event)">
              取消追更
            </button>
          </div>
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
  padding: var(--s-5) var(--s-6) var(--s-3);
}
.page-title {
  font-size: var(--f-6);
  font-weight: 650;
}
.fl-cell {
  position: relative;
}
.fl-off {
  position: absolute;
  top: var(--s-2);
  right: var(--s-2);
  opacity: 0;
  transition: opacity var(--t-fast);
}
.fl-cell:hover .fl-off {
  opacity: 1;
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

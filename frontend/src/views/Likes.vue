<script setup>
/**
 * 点赞页。
 *
 */
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import VideoCard from '../components/VideoCard.vue'
import * as api from '../api/bridge'
import { useAccountStore } from '../stores/account'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const account = useAccountStore()
const ui = useUiStore()

const raw = ref([])
const loading = ref(true)
const error = ref('')
const selected = ref(new Set())

const loggedIn = !!(account.user && (account.user.userId || account.user.UserID))
const allOn = computed(() => raw.value.length > 0 && selected.value.size === raw.value.length)

async function load() {
  if (!loggedIn) {
    loading.value = false
    return
  }
  loading.value = true
  error.value = ''
  try {
    const r = await api.Likes([])
    const list = Array.isArray(r) ? r : (r && (r.items || r.Items)) || []
    raw.value = list.map((x) => (typeof x === 'string' ? { seriesId: x } : x))
  } catch (e) {
    error.value = e.message || '加载失败'
    raw.value = []
  } finally {
    loading.value = false
  }
}

function toggle(id) {
  const s = new Set(selected.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  selected.value = s
}

function toggleAll() {
  if (allOn.value) selected.value = new Set()
  else selected.value = new Set(raw.value.map((x) => x.seriesId || x.id))
}

async function removeSelected() {
  const ids = [...selected.value]
  if (!ids.length) return
  try {
    await api.RemoveLikes(ids)
    raw.value = raw.value.filter((x) => !selected.value.has(x.seriesId || x.id))
    selected.value = new Set()
    ui.toast(`已取消 ${ids.length} 项点赞`, 'success')
  } catch (e) {
    ui.toast(e.message || '操作失败', 'error')
  }
}

function openSeries(it) {
  const id = it.seriesId || it.id
  if (id) router.push({ name: 'series', params: { id } })
}

onMounted(load)
</script>

<template>
  <div class="v-root">
    <header class="page-head">
      <h1 class="page-title">点赞</h1>
      <div v-if="raw.length" class="page-tools">
        <button class="btn btn-sm btn-ghost" @click="toggleAll">
          {{ allOn ? '取消全选' : '全选' }}
        </button>
        <button
          class="btn btn-sm"
          :disabled="!selected.size"
          @click="removeSelected"
        >
          删除{{ selected.size ? `(${selected.size})` : '' }}
        </button>
      </div>
    </header>

    <div class="v-body">
      <div v-if="!loggedIn" class="empty">
        <span class="empty-icon">♡</span>
        <span class="empty-text">登录后可以查看点赞</span>
        <button class="btn btn-sm btn-primary" @click="router.push('/login')">去登录</button>
      </div>

      <div v-else-if="loading" class="grid-cards">
        <div v-for="i in 8" :key="i" class="sk-card">
          <div class="skeleton sk-cover" />
          <div class="skeleton sk-line" style="width: 68%" />
        </div>
      </div>

      <div v-else-if="error && !raw.length" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ error }}</span>
        <button class="btn btn-sm" @click="load">重试</button>
      </div>

      <div v-else-if="!raw.length" class="empty">
        <span class="empty-icon">♡</span>
        <span class="empty-text">还没有点赞的记录</span>
      </div>

      <div v-else class="grid-cards">
        <div
          v-for="it in raw"
          :key="it.seriesId || it.id"
          class="lk-cell"
          :class="{ 'is-on': selected.has(it.seriesId || it.id) }"
        >
          <VideoCard :item="it" @click="openSeries(it)" />
          <button class="lk-check" @click.stop="toggle(it.seriesId || it.id)">
            {{ selected.has(it.seriesId || it.id) ? '✓' : '' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-3);
  padding: var(--s-5) var(--s-6) var(--s-3);
}
.page-title {
  font-size: var(--f-6);
  font-weight: 650;
}
.page-tools {
  display: flex;
  gap: var(--s-2);
}
.lk-cell {
  position: relative;
}
.lk-cell.is-on {
  outline: 2px solid var(--c-brand);
  outline-offset: 2px;
  border-radius: var(--r-3);
}
.lk-check {
  position: absolute;
  top: var(--s-2);
  left: var(--s-2);
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 1.5px solid rgba(255, 255, 255, 0.75);
  background: rgba(0, 0, 0, 0.4);
  color: #fff;
  font-size: 12px;
  line-height: 1;
}
.lk-cell.is-on .lk-check {
  background: var(--c-brand);
  border-color: var(--c-brand);
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

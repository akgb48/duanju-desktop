<script setup>
/**
 * 追剧书架。
 *
 * 网格展示 + 批量管理（选择模式）。
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import VideoCard from '../components/VideoCard.vue'
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

/** 批量选择。 */
const selecting = ref(false)
const selected = ref(new Set())

const selectedCount = computed(() => selected.value.size)

async function load(reset = true) {
  if (reset) {
    loading.value = true
    cursor.value = ''
    error.value = ''
    selected.value = new Set()
  } else {
    if (!hasMore.value || loadingMore.value) return
    loadingMore.value = true
  }
  try {
    const r = await api.Favorites(reset ? '' : cursor.value, reset)
    const list = (r && (r.items || r.Items)) || []
    items.value = reset ? list : [...items.value, ...list]
    hasMore.value = !!(r && r.hasMore)
    cursor.value = (r && r.cursor) || ''
  } catch (e) {
    error.value = e.message || '加载书架失败'
    if (reset) items.value = []
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function openSeries(it) {
  if (selecting.value) {
    toggleSelect(it.seriesId)
    return
  }
  if (it.seriesId) router.push({ name: 'series', params: { id: it.seriesId } })
}

function toggleSelect(id) {
  const s = new Set(selected.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  selected.value = s
}

function toggleSelectMode() {
  selecting.value = !selecting.value
  selected.value = new Set()
}

async function removeSelected() {
  const ids = [...selected.value]
  if (!ids.length) {
    ui.toastWarn('请先选择要移除的剧')
    return
  }
  const ok = await ui.confirm({
    title: '移出书架',
    text: `将 ${ids.length} 部剧从追剧书架移除？`,
    confirmText: '移除',
    danger: true,
  })
  if (!ok) return
  try {
    await api.RemoveFavorites(ids)
    items.value = items.value.filter((x) => !ids.includes(x.seriesId))
    selected.value = new Set()
    selecting.value = false
    ui.toastOk('已移除')
  } catch (e) {
    ui.toastErr(e.message || '移除失败')
  }
}

async function removeOne(it, ev) {
  ev.stopPropagation()
  try {
    await api.SetFavorite(it.seriesId, false)
    items.value = items.value.filter((x) => x.seriesId !== it.seriesId)
    ui.toastOk('已移出书架')
  } catch (e) {
    ui.toastErr(e.message || '操作失败')
  }
}

onMounted(() => load(true))
</script>

<template>
  <div class="v-root">
    <div class="v-head">
      <div>
        <h1>追剧书架</h1>
        <p class="v-head-sub">{{ items.length }} 部在追</p>
      </div>
      <div class="fv-acts">
        <template v-if="selecting">
          <span class="mute t-f2">已选 {{ selectedCount }}</span>
          <button class="btn btn-sm btn-danger" :disabled="!selectedCount" @click="removeSelected">
            移除
          </button>
          <button class="btn btn-sm" @click="toggleSelectMode">取消</button>
        </template>
        <button v-else class="btn btn-sm" :disabled="!items.length" @click="toggleSelectMode">
          管理
        </button>
      </div>
    </div>

    <div class="v-body">
      <div v-if="error" class="empty">
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
        <span class="empty-icon">★</span>
        <span class="empty-text">书架还是空的</span>
        <button class="btn btn-sm" @click="router.push({ name: 'recommend' })">去挑几部</button>
      </div>

      <template v-else>
        <div class="grid-cards">
          <div
            v-for="(it, i) in items"
            :key="it.seriesId || i"
            class="fv-card"
            :class="{ 'is-selected': selected.has(it.seriesId), 'is-selecting': selecting }"
          >
            <VideoCard :item="it" @click="openSeries(it)" />
            <button
              v-if="selecting"
              class="fv-check"
              :class="{ 'is-on': selected.has(it.seriesId) }"
              @click.stop="toggleSelect(it.seriesId)"
            >
              {{ selected.has(it.seriesId) ? '✓' : '' }}
            </button>
            <button
              v-else
              class="fv-unstar"
              title="移出书架"
              @click="removeOne(it, $event)"
            >
              ✕
            </button>
          </div>
        </div>

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
.fv-acts {
  display: flex;
  align-items: center;
  gap: var(--s-2);
}

.fv-card {
  position: relative;
}

.fv-card.is-selected :deep(.vc-cover) {
  outline: 2px solid var(--c-brand);
  outline-offset: 2px;
}

.fv-check {
  position: absolute;
  left: var(--s-2);
  top: var(--s-2);
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.55);
  border: 1.5px solid rgba(255, 255, 255, 0.6);
  color: #fff;
  font-size: var(--f-2);
  display: flex;
  align-items: center;
  justify-content: center;
  backdrop-filter: blur(4px);
  transition: background var(--t-fast), border-color var(--t-fast);
}
.fv-check.is-on {
  background: var(--c-brand);
  border-color: var(--c-brand);
}

.fv-unstar {
  position: absolute;
  right: var(--s-2);
  top: var(--s-2);
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.55);
  color: rgba(255, 255, 255, 0.85);
  font-size: var(--f-1);
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transition: opacity var(--t-fast), background var(--t-fast);
  backdrop-filter: blur(4px);
}
.fv-card:hover .fv-unstar {
  opacity: 1;
}
.fv-unstar:hover {
  background: var(--c-err);
  color: #fff;
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

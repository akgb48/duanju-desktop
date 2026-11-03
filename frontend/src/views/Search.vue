<script setup>
/**
 * 搜索入口页。
 *
 * 三块内容：
 *   1. 大搜索框
 *   2. 搜索历史（本地 localStorage，最多 12 条，可清空）
 *   3. 热门搜索（后端没专供接口，用 SearchSuggest 预热几个固定词）
 */
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import SearchBar from '../components/SearchBar.vue'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const ui = useUiStore()

const HISTORY_KEY = 'hg:search:history'
const MAX_HISTORY = 12

const history = ref([])
const searchBar = ref(null)

/** 热门词：后端无对应接口，给一组固定入口。 */
const HOT = ['总裁', '重生', '甜宠', '逆袭', '悬疑', '古装', '搞笑', '都市']

function loadHistory() {
  try {
    const raw = localStorage.getItem(HISTORY_KEY)
    history.value = raw ? JSON.parse(raw) : []
    if (!Array.isArray(history.value)) history.value = []
  } catch {
    history.value = []
  }
}

function saveHistory(list) {
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(list))
  } catch {
    /* 忽略 */
  }
}

function pushHistory(kw) {
  const q = kw.trim()
  if (!q) return
  const next = [q, ...history.value.filter((x) => x !== q)].slice(0, MAX_HISTORY)
  history.value = next
  saveHistory(next)
}

function doSearch(kw) {
  const q = (kw || '').trim()
  if (!q) return
  pushHistory(q)
  router.push({ name: 'search', query: { q } })
}

function removeOne(kw) {
  const next = history.value.filter((x) => x !== kw)
  history.value = next
  saveHistory(next)
}

async function clearHistory() {
  const ok = await ui.confirm({
    title: '清空搜索历史',
    text: '清空后无法恢复。',
    confirmText: '清空',
    danger: true,
  })
  if (!ok) return
  history.value = []
  saveHistory([])
  ui.toastOk('已清空')
}

onMounted(loadHistory)
</script>

<template>
  <div class="v-root">
    <div class="v-body search-page">
      <div class="sp-inner">
        <h1 class="sp-title">搜索</h1>

        <div class="sp-bar">
          <SearchBar
            ref="searchBar"
            size="lg"
            placeholder="搜剧名、类型、作者"
            autofocus
            @search="doSearch"
          />
        </div>

        <!-- 历史 -->
        <section v-if="history.length" class="sp-section">
          <header class="sp-sec-head">
            <h2 class="sp-sec-title">搜索历史</h2>
            <button class="btn btn-sm btn-ghost" @click="clearHistory">清空</button>
          </header>
          <div class="sp-chips">
            <span v-for="h in history" :key="h" class="sp-chip">
              <button class="sp-chip-text ellipsis" :title="h" @click="doSearch(h)">{{ h }}</button>
              <button class="sp-chip-x" title="删除" @click="removeOne(h)">✕</button>
            </span>
          </div>
        </section>

        <!-- 热门 -->
        <section class="sp-section">
          <header class="sp-sec-head">
            <h2 class="sp-sec-title">热门搜索</h2>
          </header>
          <div class="sp-chips">
            <button
              v-for="(h, i) in HOT"
              :key="h"
              class="sp-chip sp-chip-hot"
              :class="{ 'is-top': i < 3 }"
              @click="doSearch(h)"
            >
              <span class="sp-hot-no t-num">{{ i + 1 }}</span>
              {{ h }}
            </button>
          </div>
        </section>

        <p class="sp-tip mute t-f2">
          提示：在结果页可以用顶部搜索框继续换关键词，历史会自动记下。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.search-page {
  display: flex;
  justify-content: center;
}
.sp-inner {
  width: 100%;
  max-width: 640px;
  padding-top: var(--s-7);
}
.sp-title {
  font-size: var(--f-8);
  font-weight: 650;
  margin-bottom: var(--s-5);
  letter-spacing: -0.5px;
}
.sp-bar {
  margin-bottom: var(--s-7);
}

.sp-section {
  margin-bottom: var(--s-6);
}
.sp-sec-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--s-3);
}
.sp-sec-title {
  font-size: var(--f-5);
  font-weight: 600;
}

.sp-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}

.sp-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--s-1);
  height: 30px;
  padding: 0 var(--s-3);
  border-radius: var(--r-full);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  font-size: var(--f-3);
  color: var(--c-text-dim);
  transition: background var(--t-fast), color var(--t-fast), border-color var(--t-fast);
  max-width: 220px;
}
.sp-chip:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
  border-color: var(--c-line-strong);
}
.sp-chip-text {
  background: none;
  border: none;
  color: inherit;
  font-size: inherit;
  padding: 0;
  max-width: 180px;
}
.sp-chip-x {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  font-size: 9px;
  color: var(--c-text-mute);
  transition: background var(--t-fast), color var(--t-fast);
}
.sp-chip-x:hover {
  background: var(--c-err-dim);
  color: var(--c-err);
}

.sp-chip-hot {
  border-color: transparent;
  background: var(--c-bg-hover);
}
.sp-hot-no {
  font-size: var(--f-1);
  color: var(--c-text-mute);
  font-weight: 700;
}
.sp-chip-hot.is-top .sp-hot-no {
  color: var(--c-brand);
}

.sp-tip {
  margin-top: var(--s-6);
  line-height: 1.7;
}
</style>

<script setup>
/**
 * 我的预约。
 *
 * 两个视图：列表 与 日历。
 * 预约列表按上线状态分页。
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import * as api from '../api/bridge'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const ui = useUiStore()

const view = ref('list') // list | calendar
const online = ref(true)

/** 列表。 */
const items = ref([])
const cursor = ref('')
const hasMore = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const onlineTotal = ref(0)
const offlineTotal = ref(0)
let listRequest = 0

/** 日历。 */
const now = new Date()
const year = ref(now.getFullYear())
const month = ref(now.getMonth() + 1)
const calendarDays = ref([])
const calLoading = ref(false)

const monthLabel = computed(() => `${year.value} 年 ${month.value} 月`)

async function load(reset = true) {
  if (reset) {
    loading.value = true
    loadingMore.value = false
    cursor.value = ''
    error.value = ''
  } else {
    if (!hasMore.value || loadingMore.value) return
    loadingMore.value = true
  }
  const request = ++listRequest
  try {
    const r = await api.Reservations(online.value, reset ? '' : cursor.value)
    if (request !== listRequest) return
    const list = r.items
    items.value = reset ? list : [...items.value, ...list]
    hasMore.value = r.hasMore
    cursor.value = r.cursor
    onlineTotal.value = r.onlineTotal
    offlineTotal.value = r.offlineTotal
  } catch (e) {
    if (request !== listRequest) return
    error.value = e.message || '加载预约失败'
    if (reset) items.value = []
  } finally {
    if (request === listRequest) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

function switchOnline(value) {
  if (online.value === value) return
  online.value = value
  load(true)
}

async function loadCalendar() {
  calLoading.value = true
  try {
    const r = await api.Calendar(year.value, month.value)
    const days = (r && (r.days || r.items)) || []
    calendarDays.value = days
  } catch {
    calendarDays.value = []
  } finally {
    calLoading.value = false
  }
}

/** 日历网格：周一起始。 */
const calendarGrid = computed(() => {
  const y = year.value
  const m = month.value
  const first = new Date(y, m - 1, 1)
  // 周日=0 → 转成周一=0
  const lead = (first.getDay() + 6) % 7
  const daysInMonth = new Date(y, m, 0).getDate()

  // 后端给的日期集合，统一成 'YYYY-MM-DD'
  const marked = new Set()
  for (const d of calendarDays.value) {
    const key = typeof d === 'string' ? d.slice(0, 10) : formatKey(d.date || d.day || '')
    if (key) marked.add(key)
  }

  const cells = []
  for (let i = 0; i < lead; i++) cells.push({ blank: true, key: `b${i}` })
  for (let d = 1; d <= daysInMonth; d++) {
    const key = `${y}-${String(m).padStart(2, '0')}-${String(d).padStart(2, '0')}`
    cells.push({ blank: false, day: d, key, marked: marked.has(key) })
  }
  return cells
})

function formatKey(v) {
  if (!v) return ''
  const d = new Date(typeof v === 'number' ? v * 1000 : v)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function shiftMonth(delta) {
  let m = month.value + delta
  let y = year.value
  if (m < 1) {
    m = 12
    y -= 1
  } else if (m > 12) {
    m = 1
    y += 1
  }
  year.value = y
  month.value = m
  loadCalendar()
}

async function cancel(it, ev) {
  ev.stopPropagation()
  const ok = await ui.confirm({
    title: '取消预约',
    text: `不再接收「${it.title || '这部剧'}」的开播提醒？`,
    confirmText: '取消预约',
    danger: true,
  })
  if (!ok) return
  try {
    if (it.online) await api.DeleteReservations([it.itemId || it.seriesId], true)
    else await api.SetReservation(it.seriesId, false)
    await load(true)
    ui.toastOk('已取消预约')
  } catch (e) {
    ui.toastErr(e.message || '操作失败')
  }
}

function openSeries(it) {
  if (it.seriesId) router.push({ name: 'series', params: { id: it.seriesId } })
}

/** 开播倒计时文本。 */
function countdown(ts) {
  if (!ts) return ''
  const diff = Number(ts) - Math.floor(Date.now() / 1000)
  if (diff <= 0) return '已开播'
  const d = Math.floor(diff / 86400)
  const h = Math.floor((diff % 86400) / 3600)
  if (d > 0) return `${d} 天 ${h} 小时后开播`
  const m = Math.floor((diff % 3600) / 60)
  return `${h} 小时 ${m} 分后开播`
}

function switchView(v) {
  view.value = v
  if (v === 'calendar' && !calendarDays.value.length) loadCalendar()
}

onMounted(() => load(true))
</script>

<template>
  <div class="v-root">
    <div class="v-head">
      <div>
        <h1>我的预约</h1>
        <p class="v-head-sub">开播前会提醒你</p>
      </div>
      <div class="v-tabs">
        <button class="v-tab" :class="{ 'is-active': view === 'list' }" @click="switchView('list')">
          列表
        </button>
        <button class="v-tab" :class="{ 'is-active': view === 'calendar' }" @click="switchView('calendar')">
          日历
        </button>
      </div>
    </div>

    <div class="v-body">
      <!-- 列表 -->
      <template v-if="view === 'list'">
        <div class="v-tabs">
          <button class="v-tab" :class="{ 'is-active': online }" @click="switchOnline(true)">
            已上线 {{ onlineTotal || '' }}
          </button>
          <button class="v-tab" :class="{ 'is-active': !online }" @click="switchOnline(false)">
            待上线 {{ offlineTotal || '' }}
          </button>
        </div>
        <div v-if="error" class="empty">
          <span class="empty-icon">⚠</span>
          <span class="empty-text">{{ error }}</span>
          <button class="btn btn-sm" @click="load(true)">重试</button>
        </div>

        <div v-else-if="loading" class="rv-loading"><span class="spinner" /></div>

        <div v-else-if="!items.length" class="empty">
          <span class="empty-icon">◈</span>
          <span class="empty-text">{{ online ? '没有已上线的预约' : '没有待上线的预约' }}</span>
          <button class="btn btn-sm" @click="router.push({ name: 'recommend' })">去发现</button>
        </div>

        <template v-else>
          <ul class="rv-list">
            <li v-for="it in items" :key="it.seriesId" class="rv-item" @click="openSeries(it)">
              <div class="rv-cover">
                <img v-if="it.cover" :src="it.cover" :alt="it.title" loading="lazy" />
                <div v-else class="rv-cover-ph">{{ (it.title || '?').slice(0, 1) }}</div>
              </div>

              <div class="rv-main">
                <h3 class="rv-title ellipsis">{{ it.title || '未命名' }}</h3>
                <p class="rv-sub">
                  <span class="tag tag-warn">{{ it.online ? '已上线' : countdown(it.expectedPublishTime) || it.hint || '待定' }}</span>
                  <span v-if="it.episodeCount" class="mute t-f1">共 {{ it.episodeCount }} 集</span>
                </p>
              </div>

              <button class="btn btn-sm btn-ghost" @click="cancel(it, $event)">取消预约</button>
            </li>
          </ul>

          <div class="v-more">
            <button v-if="hasMore" class="btn btn-sm" :disabled="loadingMore" @click="load(false)">
              {{ loadingMore ? '加载中…' : '加载更多' }}
            </button>
          </div>
        </template>
      </template>

      <!-- 日历 -->
      <template v-else>
        <div class="cal-head">
          <button class="btn btn-sm btn-ghost" @click="shiftMonth(-1)">‹</button>
          <span class="cal-month">{{ monthLabel }}</span>
          <button class="btn btn-sm btn-ghost" @click="shiftMonth(1)">›</button>
          <span class="grow" />
          <button class="btn btn-sm btn-ghost" @click="loadCalendar">刷新</button>
        </div>

        <div v-if="calLoading" class="rv-loading"><span class="spinner" /></div>

        <div v-else class="cal">
          <div v-for="w in ['一', '二', '三', '四', '五', '六', '日']" :key="w" class="cal-dow">
            {{ w }}
          </div>
          <div
            v-for="c in calendarGrid"
            :key="c.key"
            class="cal-cell"
            :class="{ 'is-blank': c.blank, 'is-marked': c.marked }"
          >
            <span v-if="!c.blank" class="cal-day t-num">{{ c.day }}</span>
            <span v-if="c.marked" class="cal-dot" />
          </div>
        </div>

        <p class="cal-tip mute t-f2">● 表示当天有新剧开播</p>
      </template>
    </div>
  </div>
</template>

<style scoped>
.rv-loading {
  display: flex;
  justify-content: center;
  padding: var(--s-9);
}

.rv-list {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}
.rv-item {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  padding: var(--s-2);
  border-radius: var(--r-2);
  cursor: pointer;
  transition: background var(--t-fast);
}
.rv-item:hover {
  background: var(--c-bg-hover);
}

.rv-cover {
  flex: 0 0 auto;
  width: 64px;
  aspect-ratio: 3 / 4;
  border-radius: var(--r-2);
  overflow: hidden;
  background: var(--c-bg-active);
}
.rv-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.rv-cover-ph {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--f-5);
  font-weight: 700;
  color: rgba(255, 255, 255, 0.3);
  background: linear-gradient(140deg, #2a2f38, #171a20);
}

.rv-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}
.rv-title {
  font-size: var(--f-4);
  font-weight: 500;
}
.rv-sub {
  display: flex;
  align-items: center;
  gap: var(--s-2);
}

/* ---- 日历 ---- */
.cal-head {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  margin-bottom: var(--s-4);
}
.cal-month {
  font-size: var(--f-5);
  font-weight: 600;
  min-width: 120px;
  text-align: center;
}

.cal {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: var(--s-1);
  max-width: 560px;
}
.cal-dow {
  text-align: center;
  font-size: var(--f-1);
  color: var(--c-text-mute);
  padding-bottom: var(--s-2);
}
.cal-cell {
  position: relative;
  aspect-ratio: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--r-2);
  background: var(--c-bg-elev);
  border: 1px solid transparent;
  font-size: var(--f-3);
  color: var(--c-text-dim);
}
.cal-cell.is-blank {
  background: transparent;
}
.cal-cell.is-marked {
  border-color: var(--c-brand-line);
  background: var(--c-brand-dim);
  color: var(--c-brand);
  font-weight: 600;
}
.cal-dot {
  position: absolute;
  bottom: 5px;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--c-brand);
}
.cal-tip {
  margin-top: var(--s-3);
}
</style>

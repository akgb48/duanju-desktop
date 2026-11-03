<script setup>
/**
 * 剧集详情页。
 *
 * 两栏：左侧 简介 + 选集 + 评论；右侧 相关推荐 / 信息卡。
 */
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import EpisodeList from '../components/EpisodeList.vue'
import CommentPanel from '../components/CommentPanel.vue'
import VideoCard from '../components/VideoCard.vue'
import * as api from '../api/bridge'
import { useAccountStore } from '../stores/account'
import { useDownloadStore } from '../stores/download'
import { useUiStore } from '../stores/ui'

const route = useRoute()
const router = useRouter()
const account = useAccountStore()
const downloader = useDownloadStore()
const ui = useUiStore()

const seriesId = computed(() => String(route.params.id || ''))

const detail = ref(null)
const episodes = ref([])
const related = ref([])
const loading = ref(false)
const error = ref('')
const expanded = ref(false)
const tab = ref('episodes') // episodes | comments

/** 已看过的集（来自本地进度，后端 History 里取最近一条）。 */
const lastWatched = ref(null)

async function load() {
  if (!seriesId.value) return
  loading.value = true
  error.value = ''
  try {
    const d = await api.SeriesDetail(seriesId.value)
    detail.value = d || null
    let list = (d && (d.episodes || d.Episodes)) || []
    if (!list.length && d && d.episodeCount) {
      list = Array.from({ length: Number(d.episodeCount) }, (_, i) => ({
        seriesId: seriesId.value,
        index: i + 1,
        title: `第 ${i + 1} 集`,
        isFree: true,
      }))
    }
    episodes.value = list

    // 追剧状态
    if (account.loggedIn) {
      try {
        const f = await api.IsFollowingUpdate(seriesId.value)
        if (detail.value) detail.value.isFollowing = !!f
      } catch {
        /* 忽略 */
      }
    }

    // 相关推荐
    loadRelated()
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function loadRelated() {
  try {
    const r = await api.Related(seriesId.value)
    related.value = Array.isArray(r) ? r : (r && r.items) || []
  } catch {
    related.value = []
  }
}

/** 从第几集开始播。 */
function startPlay(ep) {
  const n = ep ? ep.index || ep : lastWatched.value || 1
  router.push({ name: 'play', params: { seriesId: seriesId.value, videoId: String(n) } })
}

/** 追剧。 */
async function toggleFavorite() {
  if (!account.loggedIn) {
    ui.toastWarn('请先登录')
    router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  const cur = !!(detail.value && (detail.value.isFavorite || detail.value.isFollowing))
  try {
    await api.SetFavorite(seriesId.value, !cur)
    if (detail.value) {
      detail.value.isFavorite = !cur
      detail.value.isFollowing = !cur
    }
    ui.toastOk(cur ? '已取消追剧' : '已加入追剧书架')
  } catch (e) {
    ui.toastErr(e.message || '操作失败')
  }
}

/** 预约。 */
async function toggleReservation() {
  if (!account.loggedIn) {
    ui.toastWarn('请先登录')
    return
  }
  const cur = !!(detail.value && detail.value.isReserved)
  try {
    await api.SetReservation(seriesId.value, !cur)
    if (detail.value) detail.value.isReserved = !cur
    ui.toastOk(cur ? '已取消预约' : '已预约，开播会提醒你')
  } catch (e) {
    ui.toastErr(e.message || '操作失败')
  }
}

/** 整剧下载。 */
async function downloadAll() {
  const r = await downloader.addSeries(seriesId.value)
  if (r.ok) ui.toastOk('已加入下载队列')
  else ui.toastErr(r.error)
}

/** 分享。 */
async function share() {
  try {
    const r = await api.ShareLink(seriesId.value, 1)
    const url = (r && (r.url || r.Url)) || ''
    if (!url) {
      ui.toastWarn('暂时拿不到分享链接')
      return
    }
    await navigator.clipboard.writeText(url)
    ui.toastOk('分享链接已复制')
  } catch (e) {
    ui.toastErr('复制失败，请手动复制')
  }
}

function openRelated(it) {
  const id = it.seriesId || it.id
  if (id) router.push({ name: 'series', params: { id } })
}

const desc = computed(() => (detail.value && (detail.value.description || detail.value.Description)) || '暂无简介')

const statusLabel = computed(() => {
  const s = detail.value && detail.value.status
  if (s === 'completed') return '已完结'
  if (s === 'ongoing') return '连载中'
  return ''
})

watch(seriesId, load)
onMounted(load)
</script>

<template>
  <div class="v-root">
    <div class="v-body">
      <!-- 加载 -->
      <div v-if="loading" class="sd-loading">
        <span class="spinner" style="width: 26px; height: 26px" />
        <span>加载中…</span>
      </div>

      <!-- 错误 -->
      <div v-else-if="error" class="empty">
        <span class="empty-icon">⚠</span>
        <span class="empty-text">{{ error }}</span>
        <button class="btn btn-sm" @click="load">重试</button>
      </div>

      <template v-else-if="detail">
        <div class="v-two-col">
          <!-- 左栏 -->
          <div class="sd-main">
            <!-- 头部 -->
            <div class="sd-head">
              <div class="sd-cover">
                <img v-if="detail.cover" :src="detail.cover" :alt="detail.title" />
                <div v-else class="sd-cover-ph">{{ (detail.title || '?').slice(0, 1) }}</div>
              </div>

              <div class="sd-info">
                <h1 class="sd-title">{{ detail.title }}</h1>

                <div class="sd-tags">
                  <span v-if="statusLabel" class="tag" :class="detail.status === 'completed' ? 'tag-ok' : 'tag-warn'">
                    {{ statusLabel }}
                  </span>
                  <span v-if="detail.category" class="tag">{{ detail.category }}</span>
                  <span v-for="t in detail.tags || []" :key="t" class="tag">{{ t }}</span>
                  <span v-if="detail.score" class="sd-score t-num">{{ detail.score }} 分</span>
                </div>

                <p class="sd-meta mute t-f2">
                  <span v-if="detail.author">作者：{{ detail.author }}</span>
                  <span v-if="detail.episodeCount"> · 共 {{ detail.episodeCount }} 集</span>
                  <span v-if="detail.playCount"> · {{ detail.playCount }} 次播放</span>
                </p>

                <p class="sd-desc" :class="{ 'is-expanded': expanded }">{{ desc }}</p>
                <button v-if="desc.length > 80" class="sd-expand" @click="expanded = !expanded">
                  {{ expanded ? '收起' : '展开全部' }}
                </button>

                <div class="sd-acts">
                  <button class="btn btn-primary btn-lg" @click="startPlay(lastWatched)">
                    {{ lastWatched ? `继续观看 第 ${lastWatched} 集` : '开始观看' }}
                  </button>
                  <button class="btn" @click="toggleFavorite">
                    {{ detail.isFavorite || detail.isFollowing ? '★ 已追剧' : '☆ 追剧' }}
                  </button>
                  <button class="btn" @click="toggleReservation">
                    {{ detail.isReserved ? '◈ 已预约' : '◈ 预约' }}
                  </button>
                  <button class="btn" title="整剧下载" @click="downloadAll">⤓ 下载</button>
                  <button class="btn btn-ghost" title="复制分享链接" @click="share">⤴ 分享</button>
                </div>
              </div>
            </div>

            <!-- Tabs -->
            <div class="v-tabs sd-tabs">
              <button class="v-tab" :class="{ 'is-active': tab === 'episodes' }" @click="tab = 'episodes'">
                选集 <span class="t-num mute">({{ episodes.length }})</span>
              </button>
              <button class="v-tab" :class="{ 'is-active': tab === 'comments' }" @click="tab = 'comments'">
                评论
              </button>
            </div>

            <!-- 选集 -->
            <div v-if="tab === 'episodes'" class="sd-eps">
              <EpisodeList
                :episodes="episodes"
                :current="lastWatched || 0"
                layout="grid"
                downloadable
                @select="startPlay"
                @download="(ep) => downloader.add(seriesId, ep.index, ep.index)"
              />
            </div>

            <!-- 评论 -->
            <div v-else class="sd-comments">
              <CommentPanel :series-id="seriesId" :video-id="''" />
            </div>
          </div>

          <!-- 右栏 -->
          <aside class="sd-aside">
            <section class="sd-card">
              <h3 class="sd-card-title">信息</h3>
              <dl class="sd-dl">
                <div class="sd-dl-row">
                  <dt>状态</dt>
                  <dd>{{ statusLabel || '未知' }}</dd>
                </div>
                <div class="sd-dl-row">
                  <dt>总集数</dt>
                  <dd>{{ detail.episodeCount || episodes.length }}</dd>
                </div>
                <div v-if="detail.author" class="sd-dl-row">
                  <dt>作者</dt>
                  <dd class="ellipsis">{{ detail.author }}</dd>
                </div>
                <div v-if="detail.updatedAt" class="sd-dl-row">
                  <dt>更新</dt>
                  <dd>{{ new Date(Number(detail.updatedAt) * 1000).toLocaleDateString() }}</dd>
                </div>
                <div class="sd-dl-row">
                  <dt>ID</dt>
                  <dd class="mute t-f1 ellipsis">{{ seriesId }}</dd>
                </div>
              </dl>
            </section>

            <section v-if="related.length" class="sd-card">
              <h3 class="sd-card-title">相关推荐</h3>
              <div class="sd-related">
                <VideoCard
                  v-for="(r, i) in related"
                  :key="r.seriesId || r.id || i"
                  :item="r"
                  layout="landscape"
                  @click="openRelated(r)"
                />
              </div>
            </section>
          </aside>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.sd-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--s-3);
  padding: var(--s-9);
  color: var(--c-text-mute);
  font-size: var(--f-3);
}

/* ---- 头部 ---- */
.sd-head {
  display: flex;
  gap: var(--s-5);
  margin-bottom: var(--s-6);
}

.sd-cover {
  flex: 0 0 auto;
  width: 180px;
  aspect-ratio: 3 / 4;
  border-radius: var(--r-3);
  overflow: hidden;
  background: var(--c-bg-hover);
}
.sd-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.sd-cover-ph {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 48px;
  font-weight: 700;
  color: rgba(255, 255, 255, 0.3);
  background: linear-gradient(140deg, #2a2f38, #171a20);
}

.sd-info {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}

.sd-title {
  font-size: var(--f-8);
  font-weight: 650;
  letter-spacing: -0.5px;
  line-height: 1.25;
}

.sd-tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--s-2);
}
.sd-score {
  font-size: var(--f-5);
  font-weight: 700;
  color: var(--c-accent);
}

.sd-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-1);
}

.sd-desc {
  font-size: var(--f-3);
  color: var(--c-text-dim);
  line-height: 1.7;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  white-space: pre-wrap;
}
.sd-desc.is-expanded {
  -webkit-line-clamp: unset;
  display: block;
}
.sd-expand {
  align-self: flex-start;
  font-size: var(--f-2);
  color: var(--c-brand);
}

.sd-acts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  margin-top: var(--s-2);
}

/* ---- Tabs ---- */
.sd-tabs {
  margin-bottom: var(--s-4);
}

.sd-eps,
.sd-comments {
  padding-bottom: var(--s-6);
}

/* ---- 右栏 ---- */
.sd-aside {
  display: flex;
  flex-direction: column;
  gap: var(--s-4);
}
.sd-card {
  padding: var(--s-4);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-3);
}
.sd-card-title {
  font-size: var(--f-3);
  font-weight: 600;
  margin-bottom: var(--s-3);
}

.sd-dl {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}
.sd-dl-row {
  display: flex;
  gap: var(--s-3);
  font-size: var(--f-2);
  line-height: 1.9;
}
.sd-dl-row dt {
  flex: 0 0 auto;
  width: 52px;
  color: var(--c-text-mute);
}
.sd-dl-row dd {
  flex: 1 1 auto;
  min-width: 0;
  margin: 0;
  color: var(--c-text-dim);
}

.sd-related {
  display: flex;
  flex-direction: column;
  gap: var(--s-3);
}
</style>

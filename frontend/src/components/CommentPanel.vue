<script setup>
/**
 * 评论面板。
 *
 * 结构：列表（分页游标） + 发表框 + 表情选择。
 * 支持：点赞、回复、删除（仅自己的）、展开回复。
 *
 * 与后端的交互全部走 bridge：Comments / SendComment / LikeComment /
 * DeleteComment / Replies / SendReply / Emoticons。
 */
import { ref, computed, onMounted, watch } from 'vue'
import * as api from '../api/bridge'
import { useAccountStore } from '../stores/account'
import { useUiStore } from '../stores/ui'

const props = defineProps({
  seriesId: { type: String, required: true },
  videoId: { type: String, default: '' },
  /** 4=评论 20=弹幕；评论面板固定 4 */
  dataType: { type: Number, default: 4 },
})

const account = useAccountStore()
const ui = useUiStore()

const items = ref([])
const cursor = ref('')
const hasMore = ref(false)
const loading = ref(false)
const error = ref('')

const draft = ref('')
const sending = ref(false)
/** 回复目标：null 表示顶层评论。 */
const replyTo = ref(null)

/** 展开的回复：Map<commentId, {items, cursor, hasMore, loading}>。 */
const replyMap = ref({})

/** 表情面板。 */
const emoticons = ref([])
const showEmoticons = ref(false)

const canSend = computed(() => draft.value.trim().length > 0 && !sending.value && account.loggedIn)

/** 加载首页。 */
async function load(reset = false) {
  if (loading.value) return
  if (!reset && !hasMore.value && items.value.length) return
  loading.value = true
  error.value = ''
  try {
    // 前端绑定 arity = 3：Comments(seriesId, videoId, cursor)
    // 评论固定 dataType=4，弹幕走 Danmaku()
    const r = await api.Comments(props.seriesId, props.videoId, reset ? '' : cursor.value)
    const list = (r && (r.items || r.Items)) || []
    if (reset) {
      items.value = list
      replyMap.value = {}
    } else {
      items.value = [...items.value, ...list]
    }
    hasMore.value = !!(r && (r.hasMore || r.HasMore))
    cursor.value = (r && (r.cursor || r.Cursor)) || ''
  } catch (e) {
    error.value = e.message || '加载评论失败'
  } finally {
    loading.value = false
  }
}

/** 发评论 / 回复。 */
async function submit() {
  if (!canSend.value) return
  const text = draft.value.trim()
  sending.value = true
  try {
    if (replyTo.value) {
      // SendReply 后端签名 9 个参数：
      // (seriesId, videoId, commentId, replyToReplyId, replyToUserId, text, imageUrl, dataType, offsetMs)
      await api.SendReply(
        props.seriesId,
        props.videoId,
        replyTo.value.id,
        replyTo.value.replyId || '',
        replyTo.value.userId || '',
        text,
        '',
        props.dataType,
        0,
      )
      // 回复成功后刷新该条的回复列表
      await loadReplies(replyTo.value.id, true)
      replyMap.value = { ...replyMap.value }
    } else {
      await api.SendComment(props.seriesId, props.videoId, text, '', props.dataType)
      await load(true)
    }
    draft.value = ''
    replyTo.value = null
    ui.toastOk('已发布')
  } catch (e) {
    ui.toastErr(e.message || '发布失败')
  } finally {
    sending.value = false
  }
}

/** 点赞 / 取消。 */
async function toggleLike(c) {
  if (!account.loggedIn) {
    ui.toastWarn('请先登录')
    return
  }
  const next = !c.isLiked
  // 乐观更新
  const old = { isLiked: c.isLiked, likeCount: c.likeCount }
  c.isLiked = next
  c.likeCount = Math.max(0, Number(c.likeCount || 0) + (next ? 1 : -1))
  try {
    await api.LikeComment(c.id, next)
  } catch (e) {
    c.isLiked = old.isLiked
    c.likeCount = old.likeCount
    ui.toastErr(e.message || '操作失败')
  }
}

/** 删除自己的评论。 */
async function remove(c) {
  const ok = await ui.confirm({
    title: '删除评论',
    text: '删除后无法恢复，确定继续？',
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await api.DeleteComment(c.id, props.dataType)
    items.value = items.value.filter((x) => x.id !== c.id)
    ui.toastOk('已删除')
  } catch (e) {
    ui.toastErr(e.message || '删除失败')
  }
}

/** 展开/收起回复。 */
async function loadReplies(commentId, reset = false) {
  const cur = replyMap.value[commentId] || { items: [], cursor: '', hasMore: true, loading: false }
  if (cur.loading) return
  if (!reset && !cur.hasMore && cur.items.length) return
  replyMap.value = { ...replyMap.value, [commentId]: { ...cur, loading: true } }
  try {
    const r = await api.Replies(commentId, reset ? '' : cur.cursor, 20, 0)
    const list = (r && (r.items || r.Items)) || []
    const next = {
      items: reset ? list : [...cur.items, ...list],
      cursor: (r && r.cursor) || '',
      hasMore: !!(r && r.hasMore),
      loading: false,
    }
    replyMap.value = { ...replyMap.value, [commentId]: next }
  } catch (e) {
    replyMap.value = { ...replyMap.value, [commentId]: { ...cur, loading: false } }
  }
}

function toggleReplies(c) {
  const cur = replyMap.value[c.id]
  if (cur && cur.items.length) {
    // 收起
    const next = { ...replyMap.value }
    delete next[c.id]
    replyMap.value = next
    return
  }
  loadReplies(c.id, true)
}

/** 打开表情面板。 */
async function openEmoticons() {
  showEmoticons.value = !showEmoticons.value
  if (!showEmoticons.value || emoticons.value.length) return
  try {
    const r = await api.Emoticons(props.seriesId, '')
    emoticons.value = Array.isArray(r) ? r : (r && r.items) || []
  } catch {
    emoticons.value = []
  }
}

/** 插入表情文本。 */
function insertEmoticon(e) {
  const name = e.name || e.text || `[${e.id || ''}]`
  draft.value = `${draft.value}${name}`
  showEmoticons.value = false
}

/** 设置回复目标。 */
function startReply(c, name, userId, replyId) {
  replyTo.value = {
    id: c.id,
    name: name || c.nickname || '',
    userId: userId || c.userId || '',
    replyId: replyId || '',
  }
}

function cancelReply() {
  replyTo.value = null
}

/** 时间格式化：x 分钟前。 */
function relTime(ts) {
  if (!ts) return ''
  const sec = Number(ts)
  const now = Math.floor(Date.now() / 1000)
  const d = now - sec
  if (d < 60) return '刚刚'
  if (d < 3600) return `${Math.floor(d / 60)} 分钟前`
  if (d < 86400) return `${Math.floor(d / 3600)} 小时前`
  if (d < 86400 * 30) return `${Math.floor(d / 86400)} 天前`
  const dt = new Date(sec * 1000)
  return `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, '0')}-${String(dt.getDate()).padStart(2, '0')}`
}

/** 数字缩写。 */
function shortNum(n) {
  const v = Number(n) || 0
  if (v >= 10000) return `${(v / 10000).toFixed(1)}万`
  return String(v)
}

watch(
  () => [props.seriesId, props.videoId],
  () => {
    cursor.value = ''
    hasMore.value = true
    load(true)
  },
)

onMounted(() => load(true))

defineExpose({ reload: () => load(true) })
</script>

<template>
  <section class="cpanel">
    <header class="cp-head">
      <h3 class="cp-title">评论 <span class="cp-count t-num">{{ items.length }}</span></h3>
      <button class="btn btn-sm btn-ghost" :disabled="loading" @click="load(true)">
        {{ loading ? '刷新中…' : '刷新' }}
      </button>
    </header>

    <!-- 发表框 -->
    <div class="cp-editor">
      <div v-if="replyTo" class="cp-reply-hint">
        回复 <b>@{{ replyTo.name }}</b>
        <button class="cp-reply-cancel" title="取消回复" @click="cancelReply">✕</button>
      </div>
      <textarea
        v-model="draft"
        class="input textarea cp-input"
        :placeholder="account.loggedIn ? '说点什么…' : '登录后可以发表评论'"
        :disabled="!account.loggedIn"
        maxlength="500"
        @keydown.ctrl.enter="submit"
      />
      <div class="cp-editor-bar">
        <button class="btn btn-sm btn-ghost" @click="openEmoticons">☺ 表情</button>
        <span class="grow" />
        <span class="cp-len t-num">{{ draft.length }}/500</span>
        <button class="btn btn-sm btn-primary" :disabled="!canSend" @click="submit">
          {{ sending ? '发布中…' : replyTo ? '回复' : '发布' }}
        </button>
      </div>

      <!-- 表情面板 -->
      <div v-if="showEmoticons" class="cp-emoticons">
        <div v-if="!emoticons.length" class="cp-emo-empty">暂无可用表情</div>
        <button
          v-for="e in emoticons"
          :key="e.id || e.name"
          class="cp-emo"
          :title="e.name"
          @click="insertEmoticon(e)"
        >
          <img v-if="e.url" :src="e.url" :alt="e.name" />
          <span v-else>{{ e.name || e.text }}</span>
        </button>
      </div>
    </div>

    <!-- 列表 -->
    <div v-if="error" class="cp-error">{{ error }}</div>

    <div v-if="!items.length && !loading" class="empty">
      <span class="empty-icon">💬</span>
      <span class="empty-text">还没有评论，来说第一句</span>
    </div>

    <ul class="cp-list">
      <li v-for="c in items" :key="c.id" class="cp-item">
        <span class="cp-avatar">
          <img v-if="c.avatar" :src="c.avatar" :alt="c.nickname" loading="lazy" />
          <span v-else>{{ (c.nickname || '?').slice(0, 1) }}</span>
        </span>

        <div class="cp-main">
          <div class="cp-meta">
            <span class="cp-nick ellipsis">{{ c.nickname || '匿名用户' }}</span>
            <span class="cp-time">{{ relTime(c.createdAt) }}</span>
          </div>

          <p class="cp-text">{{ c.text }}</p>

          <div class="cp-actions">
            <button
              class="cp-act"
              :class="{ 'is-on': c.isLiked }"
              :title="c.isLiked ? '取消点赞' : '点赞'"
              @click="toggleLike(c)"
            >
              ♥ {{ c.likeCount ? shortNum(c.likeCount) : '' }}
            </button>

            <button v-if="account.loggedIn" class="cp-act" @click="startReply(c, c.nickname)">
              回复
            </button>

            <button
              v-if="c.replyCount > 0"
              class="cp-act"
              @click="toggleReplies(c)"
            >
              {{ replyMap[c.id] ? '收起' : `${c.replyCount} 条回复` }}
            </button>

            <button
              v-if="account.loggedIn && account.user && c.userId === account.user.userId"
              class="cp-act cp-act-del"
              @click="remove(c)"
            >
              删除
            </button>
          </div>

          <!-- 回复列表 -->
          <div v-if="replyMap[c.id]" class="cp-replies">
            <div v-if="replyMap[c.id].loading" class="cp-rep-loading">
              <span class="spinner" />
            </div>
            <div v-for="r in replyMap[c.id].items" :key="r.id" class="cp-reply">
              <span class="cp-reply-nick">{{ r.nickname || '匿名' }}</span>
              <span class="cp-reply-text">{{ r.text }}</span>
              <button class="cp-act" @click="startReply(c, r.nickname, r.userId, r.id)">回复</button>
            </div>
            <button
              v-if="replyMap[c.id].hasMore"
              class="cp-rep-more"
              @click="loadReplies(c.id)"
            >
              加载更多回复
            </button>
          </div>
        </div>
      </li>
    </ul>

    <!-- 分页 -->
    <div v-if="hasMore" class="cp-more">
      <button class="btn btn-sm" :disabled="loading" @click="load(false)">
        {{ loading ? '加载中…' : '加载更多' }}
      </button>
    </div>
    <div v-else-if="items.length" class="cp-end">没有更多了</div>
  </section>
</template>

<style scoped>
.cpanel {
  display: flex;
  flex-direction: column;
  gap: var(--s-4);
}

.cp-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.cp-title {
  font-size: var(--f-5);
  font-weight: 600;
}
.cp-count {
  color: var(--c-text-mute);
  font-size: var(--f-3);
  font-weight: 400;
}

/* ---- 编辑器 ---- */
.cp-editor {
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
  padding: var(--s-3);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-3);
}
.cp-reply-hint {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  font-size: var(--f-2);
  color: var(--c-text-dim);
}
.cp-reply-hint b {
  color: var(--c-brand);
}
.cp-reply-cancel {
  color: var(--c-text-mute);
  font-size: var(--f-2);
}
.cp-input {
  min-height: 72px;
}
.cp-editor-bar {
  display: flex;
  align-items: center;
  gap: var(--s-2);
}
.cp-len {
  font-size: var(--f-1);
  color: var(--c-text-mute);
}

/* ---- 表情 ---- */
.cp-emoticons {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  padding: var(--s-2);
  background: var(--c-bg-input);
  border-radius: var(--r-2);
  max-height: 140px;
  overflow-y: auto;
}
.cp-emo-empty {
  font-size: var(--f-2);
  color: var(--c-text-mute);
}
.cp-emo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--r-1);
  font-size: var(--f-3);
  transition: background var(--t-fast);
}
.cp-emo:hover {
  background: var(--c-bg-hover);
}
.cp-emo img {
  width: 24px;
  height: 24px;
}

/* ---- 列表 ---- */
.cp-list {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}
.cp-item {
  display: flex;
  gap: var(--s-3);
  padding: var(--s-3) 0;
  border-bottom: 1px solid var(--c-line-soft);
}
.cp-item:last-child {
  border-bottom: none;
}

.cp-avatar {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  overflow: hidden;
  background: var(--c-bg-active);
  color: var(--c-text-dim);
  font-size: var(--f-3);
  font-weight: 600;
}
.cp-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.cp-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}

.cp-meta {
  display: flex;
  align-items: baseline;
  gap: var(--s-2);
  min-width: 0;
}
.cp-nick {
  font-size: var(--f-2);
  color: var(--c-text-dim);
  font-weight: 500;
  max-width: 160px;
}
.cp-time {
  font-size: var(--f-1);
  color: var(--c-text-mute);
}

.cp-text {
  font-size: var(--f-3);
  line-height: 1.65;
  color: var(--c-text);
  word-break: break-word;
  white-space: pre-wrap;
}

.cp-actions {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  margin-top: 2px;
}
.cp-act {
  font-size: var(--f-1);
  color: var(--c-text-mute);
  transition: color var(--t-fast);
  white-space: nowrap;
}
.cp-act:hover {
  color: var(--c-text);
}
.cp-act.is-on {
  color: var(--c-brand);
}
.cp-act-del:hover {
  color: var(--c-err);
}

/* ---- 回复 ---- */
.cp-replies {
  margin-top: var(--s-2);
  padding: var(--s-2) var(--s-3);
  background: var(--c-bg-hover);
  border-radius: var(--r-2);
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}
.cp-rep-loading {
  display: flex;
  justify-content: center;
  padding: var(--s-2);
}
.cp-reply {
  display: flex;
  align-items: baseline;
  gap: var(--s-2);
  font-size: var(--f-2);
  line-height: 1.5;
}
.cp-reply-nick {
  flex: 0 0 auto;
  color: var(--c-info);
  font-weight: 500;
}
.cp-reply-text {
  flex: 1 1 auto;
  min-width: 0;
  color: var(--c-text-dim);
  word-break: break-word;
}
.cp-rep-more {
  align-self: flex-start;
  font-size: var(--f-1);
  color: var(--c-text-mute);
}
.cp-rep-more:hover {
  color: var(--c-brand);
}

/* ---- 底部 ---- */
.cp-more {
  display: flex;
  justify-content: center;
  padding: var(--s-3) 0;
}
.cp-end {
  text-align: center;
  font-size: var(--f-2);
  color: var(--c-text-mute);
  padding: var(--s-3) 0;
}
.cp-error {
  padding: var(--s-3);
  font-size: var(--f-2);
  color: var(--c-err);
  background: var(--c-err-dim);
  border-radius: var(--r-2);
}
</style>

<script setup>
/**
 * 我的。
 *
 * 登录态展示 + 各功能入口 + 统计 + 退出登录 + 占位入口。
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import * as api from '../api/bridge'
import { useAccountStore } from '../stores/account'
import { useDownloadStore } from '../stores/download'
import { useUiStore } from '../stores/ui'

const router = useRouter()
const account = useAccountStore()
const downloader = useDownloadStore()
const ui = useUiStore()

const notices = ref([])
const appInfo = ref(null)
const loadingNotices = ref(false)

/** 功能入口。 */
const ENTRIES = [
  { name: 'history', icon: '◷', label: '历史', desc: '最近看过的剧' },
  { name: 'favorites', icon: '★', label: '收藏', desc: '在追的剧集' },
  { name: 'follows', icon: '♡', label: '追更', desc: '更新了会提醒你' },
  { name: 'likes', icon: '♥', label: '点赞', desc: '点过赞的剧集' },
  { name: 'reservations', icon: '◈', label: '预约', desc: '开播提醒' },
  { name: 'downloads', icon: '⤓', label: '下载', desc: '离线观看' },
  { name: 'settings', icon: '⚙', label: '设置', desc: '播放与外观' },
]

const version = computed(() => (appInfo.value && (appInfo.value.version || appInfo.value.Version)) || '—')

const activeDownloads = computed(() => downloader.active.length)

async function loadNotices() {
  loadingNotices.value = true
  try {
    const r = await api.Notices()
    notices.value = Array.isArray(r) ? r : (r && r.items) || []
  } catch {
    notices.value = []
  } finally {
    loadingNotices.value = false
  }
}

async function markRead(n) {
  try {
    await api.MarkNoticesRead([n.id])
    notices.value = notices.value.filter((x) => x.id !== n.id)
  } catch {
    /* 忽略 */
  }
}

async function logout() {
  const ok = await ui.confirm({
    title: '退出登录',
    text: '退出后观看历史与书架需要重新登录才能同步。',
    confirmText: '退出',
    danger: true,
  })
  if (!ok) return
  await account.logout()
  ui.toastOk('已退出登录')
  router.push({ name: 'recommend' })
}

function go(name) {
  router.push({ name })
}

onMounted(async () => {
  await account.refresh()
  loadNotices()
  downloader.refresh()
  try {
    appInfo.value = await api.AppInfo()
  } catch {
    appInfo.value = null
  }
})
</script>

<template>
  <div class="v-root">
    <div class="v-body">
      <div class="pf-inner">
        <!-- 账号卡 -->
        <section class="pf-user">
          <div class="pf-avatar">
            <img v-if="account.avatar" :src="account.avatar" :alt="account.nickname" />
            <span v-else>{{ account.nickname.slice(0, 1) }}</span>
          </div>

          <div class="pf-user-main">
            <h1 class="pf-nick ellipsis">{{ account.nickname }}</h1>
            <p class="pf-sign mute t-f2 ellipsis">
              {{ (account.user && account.user.signature) || (account.loggedIn ? '这个人很懒，什么都没写' : '登录后同步观看记录与书架') }}
            </p>
            <div class="pf-badges">
              <span v-if="account.isVip" class="tag tag-warn">VIP 会员</span>
              <span v-else-if="account.loggedIn" class="tag">普通用户</span>
              <span v-if="account.user && account.user.userId" class="tag mute t-f1">
                ID {{ account.user.userId }}
              </span>
            </div>
          </div>

          <div class="pf-user-acts">
            <button v-if="!account.loggedIn" class="btn btn-primary" @click="go('login')">
              登录 / 注册
            </button>
            <template v-else>
              <button class="btn btn-sm" @click="account.refresh()">刷新</button>
              <button class="btn btn-sm btn-danger" @click="logout">退出</button>
            </template>
          </div>
        </section>

        <!-- 统计 -->
        <section class="pf-stats">
          <div class="pf-stat">
            <span class="pf-stat-num t-num">{{ activeDownloads }}</span>
            <span class="pf-stat-label">下载中</span>
          </div>
          <div class="pf-stat">
            <span class="pf-stat-num t-num">{{ downloader.done.length }}</span>
            <span class="pf-stat-label">已完成</span>
          </div>
          <div class="pf-stat">
            <span class="pf-stat-num t-num">{{ notices.length }}</span>
            <span class="pf-stat-label">未读公告</span>
          </div>
          <div class="pf-stat">
            <span class="pf-stat-num t-num">{{ version }}</span>
            <span class="pf-stat-label">版本</span>
          </div>
        </section>

        <!-- 入口 -->
        <section class="pf-grid">
          <button v-for="e in ENTRIES" :key="e.name" class="pf-entry" @click="go(e.name)">
            <span class="pf-entry-icon">{{ e.icon }}</span>
            <span class="pf-entry-main">
              <span class="pf-entry-label">{{ e.label }}</span>
              <span class="pf-entry-desc mute t-f1">{{ e.desc }}</span>
            </span>
            <span class="pf-entry-arrow">›</span>
          </button>
        </section>

        <!-- 公告 -->
        <section v-if="notices.length" class="pf-section">
          <h2 class="pf-sec-title">公告</h2>
          <ul class="pf-notices">
            <li v-for="n in notices" :key="n.id" class="pf-notice">
              <span class="tag" :class="n.level === 'warn' ? 'tag-warn' : 'tag-info'">
                {{ n.level || '公告' }}
              </span>
              <span class="pf-notice-body">
                <b class="pf-notice-title">{{ n.title }}</b>
                <span class="pf-notice-text clamp-2 mute t-f2">{{ n.content }}</span>
              </span>
              <button class="btn btn-sm btn-ghost" @click="markRead(n)">已读</button>
            </li>
          </ul>
        </section>

        <!-- 设备信息 -->
        <section class="pf-section">
          <h2 class="pf-sec-title">设备</h2>
          <dl class="pf-dl">
            <div class="pf-dl-row">
              <dt>运行模式</dt>
              <dd>{{ account.loggedIn ? '已登录' : '未登录' }}</dd>
            </div>
            <div v-if="account.device" class="pf-dl-row">
              <dt>设备 ID</dt>
              <dd class="mute t-f1 ellipsis">{{ account.device.deviceId || account.device.id || '—' }}</dd>
            </div>
            <div class="pf-dl-row">
              <dt>后端</dt>
              <dd>{{ version === '—' ? '未连接' : '已连接' }}</dd>
            </div>
          </dl>
          <button class="btn btn-sm" style="margin-top: var(--s-3)" @click="api.RetryDeviceRegister()">
            重新注册设备
          </button>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.v-body {
  display: flex;
  justify-content: center;
}
.pf-inner {
  width: 100%;
  max-width: 780px;
  padding-bottom: var(--s-8);
}

/* ---- 账号卡 ---- */
.pf-user {
  display: flex;
  align-items: center;
  gap: var(--s-4);
  padding: var(--s-5);
  background: linear-gradient(135deg, var(--c-bg-elev), var(--c-bg-card));
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-4);
  margin-bottom: var(--s-4);
}

.pf-avatar {
  flex: 0 0 auto;
  width: 64px;
  height: 64px;
  border-radius: 50%;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--c-bg-active);
  font-size: var(--f-7);
  font-weight: 700;
  color: var(--c-text-dim);
}
.pf-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.pf-user-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
}
.pf-nick {
  font-size: var(--f-7);
  font-weight: 650;
}
.pf-sign {
  line-height: 1.5;
}
.pf-badges {
  display: flex;
  gap: var(--s-2);
  flex-wrap: wrap;
}
.pf-user-acts {
  flex: 0 0 auto;
  display: flex;
  gap: var(--s-2);
}

/* ---- 统计 ---- */
.pf-stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--s-2);
  margin-bottom: var(--s-5);
}
.pf-stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: var(--s-3);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-3);
}
.pf-stat-num {
  font-size: var(--f-6);
  font-weight: 700;
  color: var(--c-text);
}
.pf-stat-label {
  font-size: var(--f-1);
  color: var(--c-text-mute);
}

/* ---- 入口 ---- */
.pf-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--s-2);
  margin-bottom: var(--s-5);
}
.pf-entry {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  padding: var(--s-3) var(--s-4);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-3);
  text-align: left;
  transition: background var(--t-fast), border-color var(--t-fast), transform var(--t-fast);
}
.pf-entry:hover {
  background: var(--c-bg-hover);
  border-color: var(--c-line-strong);
  transform: translateY(-1px);
}
.pf-entry-icon {
  flex: 0 0 auto;
  width: 30px;
  font-size: var(--f-5);
  text-align: center;
  color: var(--c-brand);
}
.pf-entry-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  line-height: 1.35;
}
.pf-entry-label {
  font-size: var(--f-3);
  font-weight: 500;
}
.pf-entry-desc {
  color: var(--c-text-mute);
}
.pf-entry-arrow {
  flex: 0 0 auto;
  color: var(--c-text-mute);
  font-size: var(--f-5);
}

/* ---- 区块 ---- */
.pf-section {
  margin-bottom: var(--s-5);
}
.pf-sec-title {
  font-size: var(--f-4);
  font-weight: 600;
  margin-bottom: var(--s-3);
}

.pf-notices {
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}
.pf-notice {
  display: flex;
  align-items: flex-start;
  gap: var(--s-3);
  padding: var(--s-3);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-2);
}
.pf-notice-body {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.pf-notice-title {
  font-size: var(--f-3);
  font-weight: 600;
}
.pf-notice-text {
  line-height: 1.55;
}

.pf-dl {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--s-3);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-2);
}
.pf-dl-row {
  display: flex;
  gap: var(--s-3);
  font-size: var(--f-2);
  line-height: 1.9;
}
.pf-dl-row dt {
  flex: 0 0 auto;
  width: 68px;
  color: var(--c-text-mute);
}
.pf-dl-row dd {
  flex: 1 1 auto;
  min-width: 0;
  margin: 0;
  color: var(--c-text-dim);
}
</style>

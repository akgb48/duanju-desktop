<script setup>
/**
 * 左侧导航栏。
 *
 * 分三段：主导航（浏览）、我的（需要登录）、底部工具。
 * 迷你模式下只显示图标，靠 title 提供提示。
 */
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUiStore } from '../stores/ui'
import { useAccountStore } from '../stores/account'

const ui = useUiStore()
const account = useAccountStore()
const route = useRoute()
const router = useRouter()

/** 主导航：
 *  推荐 / 找剧 / 排行榜 / 新剧 / 历史 / 收藏 / 追更 */
const NAV_MAIN = [
  { name: 'recommend', icon: '⌂', label: '推荐' },
  { name: 'search', icon: '⌕', label: '找剧' },
  { name: 'rank', icon: '≡', label: '排行榜' },
  { name: 'new', icon: '✦', label: '新剧' },
  { name: 'history', icon: '◷', label: '历史' },
  { name: 'favorites', icon: '★', label: '收藏' },
  { name: 'follows', icon: '♡', label: '追更' },
]

/** 我的：需要登录。 */
const NAV_MINE = [
  { name: 'likes', icon: '♥', label: '点赞' },
  { name: 'reservations', icon: '◈', label: '预约' },
  { name: 'downloads', icon: '⤓', label: '下载' },
]

/** 底部工具。 */
const NAV_TOOL = [
  { name: 'mine', icon: '☺', label: '我的' },
  { name: 'settings', icon: '⚙', label: '设置' },
]

const currentName = computed(() => route.name)

function isActive(name) {
  if (currentName.value === name) return true
  // 剧集详情与播放页归到来源页，保持高亮不丢。
  if (name === 'recommend' && ['series', 'play'].includes(currentName.value)) return true
  return false
}

function go(name) {
  if (currentName.value === name) return
  router.push({ name })
}

/** 未登录点「我的」类入口：先去登录。 */
function goGuarded(name) {
  const needsAuth = NAV_MINE.some((n) => n.name === name)
  if (needsAuth && !account.loggedIn) {
    router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  go(name)
}
</script>

<template>
  <aside class="sidebar" :class="{ 'is-mini': ui.sidebarMini }">
    <nav class="sb-scroll">
      <div class="sb-group">
        <button
          v-for="item in NAV_MAIN"
          :key="item.name"
          class="sb-item"
          :class="{ 'is-active': isActive(item.name) }"
          :title="item.label"
          @click="go(item.name)"
        >
          <span class="sb-icon">{{ item.icon }}</span>
          <span class="sb-label">{{ item.label }}</span>
        </button>
      </div>

      <div class="sb-divider" />

      <div class="sb-group">
        <button
          v-for="item in NAV_MINE"
          :key="item.name"
          class="sb-item"
          :class="{ 'is-active': isActive(item.name) }"
          :title="item.label"
          @click="goGuarded(item.name)"
        >
          <span class="sb-icon">{{ item.icon }}</span>
          <span class="sb-label">{{ item.label }}</span>
          <span v-if="!account.loggedIn" class="sb-lock">·</span>
        </button>
      </div>
    </nav>

    <!-- 底部：账号入口 + 折叠 -->
    <div class="sb-foot">
      <button
        class="sb-account"
        :class="{ 'is-active': isActive('mine') }"
        :title="account.nickname"
        @click="go('mine')"
      >
        <span class="sb-avatar">
          <img v-if="account.avatar" :src="account.avatar" alt="" />
          <span v-else>{{ account.nickname.slice(0, 1) }}</span>
        </span>
        <span class="sb-account-text">
          <span class="sb-nick ellipsis">{{ account.nickname }}</span>
          <span class="sb-sub">{{ account.loggedIn ? (account.isVip ? 'VIP' : '已登录') : '未登录' }}</span>
        </span>
      </button>

      <button class="sb-collapse" :title="ui.sidebarMini ? '展开侧栏' : '收起侧栏'" @click="ui.toggleSidebar">
        {{ ui.sidebarMini ? '»' : '«' }}
      </button>
    </div>

    <div class="sb-tools">
      <button
        v-for="item in NAV_TOOL"
        :key="item.name"
        class="sb-item sb-item-sm"
        :class="{ 'is-active': isActive(item.name) }"
        :title="item.label"
        @click="go(item.name)"
      >
        <span class="sb-icon">{{ item.icon }}</span>
        <span class="sb-label">{{ item.label }}</span>
      </button>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  width: var(--w-sidebar);
  background: var(--c-bg-elev);
  border-right: 1px solid var(--c-line-soft);
  z-index: var(--z-sidebar);
  transition: width var(--t-base);
  overflow: hidden;
}
.sidebar.is-mini {
  width: var(--w-sidebar-mini);
}

.sb-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: var(--s-3) var(--s-2);
}

.sb-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.sb-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--s-3);
  height: 38px;
  padding: 0 var(--s-3);
  border-radius: var(--r-2);
  color: var(--c-text-dim);
  font-size: var(--f-3);
  white-space: nowrap;
  transition: background var(--t-fast), color var(--t-fast);
  text-align: left;
  width: 100%;
}
.sb-item:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.sb-item.is-active {
  background: var(--c-brand-dim);
  color: var(--c-brand);
  font-weight: 500;
}
.sb-item.is-active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 16px;
  border-radius: 0 2px 2px 0;
  background: var(--c-brand);
}

.sb-icon {
  flex: 0 0 auto;
  width: 18px;
  text-align: center;
  font-size: var(--f-5);
  line-height: 1;
}

.sb-label {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: opacity var(--t-fast);
}

.sb-lock {
  flex: 0 0 auto;
  color: var(--c-text-mute);
  font-weight: 700;
}

.sb-divider {
  height: 1px;
  margin: var(--s-2) var(--s-2);
  background: var(--c-line-soft);
}

.sb-item-sm {
  height: 32px;
  font-size: var(--f-2);
}

.sb-tools {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--s-2);
  border-top: 1px solid var(--c-line-soft);
}

.sb-foot {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--s-1);
  padding: var(--s-2);
  border-top: 1px solid var(--c-line-soft);
}

.sb-account {
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  gap: var(--s-2);
  min-width: 0;
  padding: var(--s-1);
  border-radius: var(--r-2);
  transition: background var(--t-fast);
  text-align: left;
}
.sb-account:hover,
.sb-account.is-active {
  background: var(--c-bg-hover);
}

.sb-avatar {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  overflow: hidden;
  background: var(--c-bg-active);
  color: var(--c-text-dim);
  font-size: var(--f-3);
  font-weight: 600;
}
.sb-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.sb-account-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.25;
}
.sb-nick {
  font-size: var(--f-2);
  color: var(--c-text);
}
.sb-sub {
  font-size: var(--f-1);
  color: var(--c-text-mute);
}

.sb-collapse {
  flex: 0 0 auto;
  width: 22px;
  height: 22px;
  border-radius: var(--r-1);
  color: var(--c-text-mute);
  font-size: var(--f-3);
  transition: background var(--t-fast), color var(--t-fast);
}
.sb-collapse:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}

/* 迷你模式：隐藏文字 */
.is-mini .sb-label,
.is-mini .sb-lock,
.is-mini .sb-account-text {
  display: none;
}
.is-mini .sb-item,
.is-mini .sb-account {
  justify-content: center;
  padding: 0;
}
.is-mini .sb-collapse {
  display: none;
}
.is-mini .sb-foot {
  justify-content: center;
  flex-direction: column;
}
</style>

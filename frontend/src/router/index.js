import { createRouter, createWebHashHistory } from 'vue-router'

// 路由与导航配置；首页由本地 startPage 设置决定。

const routes = [
  { path: '/', redirect: () => defaultRoute() },
  { path: '/recommend', name: 'recommend', component: () => import('../views/Recommend.vue') },
  { path: '/search', name: 'search', component: () => import('../views/Search.vue') },
  { path: '/rank', name: 'rank', component: () => import('../views/Rank.vue') },
  { path: '/new', name: 'new', component: () => import('../views/NewSeries.vue') },
  { path: '/series/:id', name: 'series', component: () => import('../views/SeriesDetail.vue'), props: true },
  { path: '/history', name: 'history', component: () => import('../views/History.vue') },
  { path: '/favorites', name: 'favorites', component: () => import('../views/Favorites.vue') },
  { path: '/likes', name: 'likes', component: () => import('../views/Likes.vue') },
  { path: '/reservations', name: 'reservations', component: () => import('../views/Reservations.vue') },
  { path: '/follows', name: 'follows', component: () => import('../views/Follows.vue') },
  { path: '/downloads', name: 'downloads', component: () => import('../views/Downloads.vue') },
  { path: '/login', name: 'login', component: () => import('../views/Login.vue') },
  { path: '/mine', name: 'mine', component: () => import('../views/Mine.vue') },
  { path: '/settings', name: 'settings', component: () => import('../views/Settings.vue') },
  {
    path: '/play/:seriesId/:videoId?',
    name: 'play',
    component: () => import('../views/Play.vue'),
    props: true,
    meta: { bare: true },
  },
  { path: '/:pathMatch(.*)*', name: 'notfound', component: () => import('../views/NotFound.vue') },
]

export const NAV = [
  { path: '/recommend', label: '推荐' },
  { path: '/search', label: '找剧' },
  { path: '/rank', label: '排行榜' },
  { path: '/new', label: '新剧' },
  { path: '/history', label: '历史' },
  { path: '/favorites', label: '收藏' },
  { path: '/follows', label: '追更' },
]

export const BOTTOM_NAV = [
  { path: '/downloads', label: '下载' },
  { path: '/reservations', label: '预约' },
  { path: '/likes', label: '点赞' },
  { path: '/mine', label: '我的' },
  { path: '/settings', label: '设置' },
]

//
// 两级来源：先看本地缓存（快，首帧就生效），启动后再由 main.js
// 用后端设置覆盖一次，保证跨设备/重装后仍是用户选的那页。
export function defaultRoute() {
  try {
    const saved = localStorage.getItem('hg.startPage')
    if (saved && NAV.some((n) => n.path === saved)) return saved
  } catch {
    /* 隐私模式下 localStorage 可能不可用 */
  }
  return '/recommend'
}

// applyStartPage 用后端设置里的 startPage 覆盖本地缓存。
// 由 main.js 在拿到设置后调用，返回最终应落地的路径。
export function applyStartPage(value) {
  if (!value) return defaultRoute()
  const path = String(value).trim()
  if (!NAV.some((n) => n.path === path)) return defaultRoute()
  try {
    localStorage.setItem('hg.startPage', path)
  } catch {
    /* 存不下也不影响本次生效 */
  }
  return path
}

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router

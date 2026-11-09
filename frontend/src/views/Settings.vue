<script setup>
/**
 * 设置。
 *
 * 分组：外观 / 播放 / 下载 / 窗口与隐私 / 数据与维护 / 关于入口。
 * 每项改动立即生效（写本地 + 同步后端），不做「保存」按钮。
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import * as api from '../api/bridge'
import { useUiStore } from '../stores/ui'
import { usePlayerStore } from '../stores/player'
import { useDownloadStore } from '../stores/download'

const router = useRouter()
const ui = useUiStore()
const player = usePlayerStore()
const downloader = useDownloadStore()

/** 数据目录信息。 */
const dataDir = ref(null)
/** 更新状态。 */
const updateState = ref(null)
/** 系统信息。 */
const integrity = ref(null)
/** 远端配置。 */
const remoteConfig = ref(null)
/** 忙碌标记。 */
const busy = ref('')

/** 老板键。 */
const bossKey = ref('')
/** 全屏观感。 */
const fullscreenLook = ref(false)
/** 窗口按钮可见。 */
const windowButtons = ref(true)
/** 数据目录迁移提示。 */
const dirNotice = ref(false)

/** 清晰度选项。 */
const QUALITIES = ['360p', '480p', '540p', '720p', '1080p', '原画']

/** 下载并发。 */
const CONCURRENCY = [1, 2, 3, 4, 6]

const themeLabel = computed(() => (ui.theme === 'dark' ? '深色' : '浅色'))

const updateStatusText = computed(() => {
  const s = updateState.value || {}
  if (busy.value === 'check-update') return '检查中…'
  switch (s.phase || s.state || s.status) {
    case 'checking':
      return '检查中…'
    case 'downloading':
      return `下载中 ${Math.round(Number(s.progress || 0) * 100)}%`
    case 'ready':
      return '更新已就绪'
    case 'idle':
      return '已是最新版本'
    case 'failed':
      return s.error || '检查失败'
    default:
      return '点击检查'
  }
})

async function loadAll() {
  try {
    dataDir.value = await api.GetDataDirInfo()
  } catch {
    dataDir.value = null
  }
  try {
    updateState.value = await api.UpdateState()
  } catch {
    updateState.value = null
  }
  try {
    integrity.value = await api.Integrity()
  } catch {
    integrity.value = null
  }
  try {
    remoteConfig.value = await api.RemoteConfig()
  } catch {
    remoteConfig.value = null
  }
  downloader.refresh()
}

/** 播放偏好改动。 */
function setPref(key, val) {
  player.setPref(key, val)
}

// 启动页可选值，与 router 的 NAV 保持一致。
const START_PAGES = [
  { path: '/recommend', label: '推荐' },
  { path: '/search', label: '找剧' },
  { path: '/rank', label: '排行榜' },
  { path: '/new', label: '新剧' },
  { path: '/history', label: '历史' },
  { path: '/favorites', label: '收藏' },
  { path: '/follows', label: '追更' },
]

const startPage = ref('/recommend')

/** 切换启动页：写本地缓存 + 走后端唯一的偏好持久化通道。 */
async function setStartPage(path) {
  startPage.value = path
  try {
    localStorage.setItem('hg.startPage', path)
  } catch {
    /* 隐私模式忽略 */
  }
  try {
    // 后端只暴露 SetPlaybackPrefs(整个 prefs 的 JSON)，没有单独的
    // GetSettings/SaveSettings。这里把 startPage 合并进现有 prefs。
    const merged = { ...player.prefs, startPage: path }
    await api.SetPlaybackPrefs(JSON.stringify(merged))
    ui.toast('启动页已保存', 'success')
  } catch (e) {
    ui.toast(e.message || '保存失败', 'error')
  }
}

/** 切换主题。 */
function toggleTheme() {
  ui.toggleTheme()
  ui.toast(`已切换到${themeLabel.value}`, 'info', 1400)
}

/** 切换侧栏。 */
function toggleSidebar() {
  ui.toggleSidebar()
}

/** 选择数据目录。 */
async function pickDir() {
  if (busy.value) return
  busy.value = 'pick-dir'
  try {
    const p = await api.PickDataDir()
    if (p) {
      const ok = await ui.confirm({
        title: '迁移数据目录',
        text: `将数据迁移到：\n${p}\n\n迁移过程中请不要关闭应用。`,
        confirmText: '开始迁移',
      })
      if (ok) {
        busy.value = 'move-dir'
        await api.MoveDataDir(p)
        ui.toastOk('迁移完成')
        await loadAll()
      }
    }
  } catch (e) {
    ui.toastErr(e.message || '迁移失败')
  } finally {
    busy.value = ''
  }
}

async function dismissNotice() {
  try {
    await api.DismissDataDirNotice()
    dirNotice.value = false
    ui.toastOk('已关闭提示')
  } catch {
    /* 忽略 */
  }
}

/** 清理缓存。 */
async function clearCache() {
  const ok = await ui.confirm({
    title: '清理缓存',
    text: '将清空接口缓存，下次进入页面会重新拉取。不影响下载与收藏。',
    confirmText: '清理',
  })
  if (!ok) return
  busy.value = 'cache'
  try {
    await api.ClearCache()
    ui.toastOk('缓存已清理')
  } catch (e) {
    ui.toastErr(e.message || '清理失败')
  } finally {
    busy.value = ''
  }
}

/** 检查更新。 */
async function checkUpdate() {
  if (busy.value) return
  busy.value = 'check-update'
  try {
    await api.CheckUpdate()
    updateState.value = await api.UpdateState()
    const st = (updateState.value && (updateState.value.phase || updateState.value.state || updateState.value.status)) || ''
    if (st === 'latest') ui.toast('已是最新版本', 'ok')
    else if (st === 'ready') ui.toastOk('更新已就绪，可以重启应用')
  } catch (e) {
    ui.toastErr(e.message || '检查更新失败')
  } finally {
    busy.value = ''
  }
}

async function downloadUpdate() {
  busy.value = 'download-update'
  try {
    await api.DownloadUpdate()
    updateState.value = await api.UpdateState()
    ui.toastOk('开始下载更新')
  } catch (e) {
    ui.toastErr(e.message || '下载失败')
  } finally {
    busy.value = ''
  }
}

async function applyUpdate() {
  const ok = await ui.confirm({
    title: '应用更新',
    text: '应用会重启以完成更新，未观看的进度已保存。',
    confirmText: '重启更新',
  })
  if (!ok) return
  try {
    await api.ApplyUpdate()
  } catch (e) {
    ui.toastErr(e.message || '更新失败')
  }
}

/** 老板键。 */
async function saveBossKey() {
  const k = bossKey.value.trim()
  if (!k) {
    ui.toastWarn('请先按下要设置的组合键')
    return
  }
  try {
    await api.SetBossKey(k)
    ui.toastOk('老板键已设置')
  } catch (e) {
    ui.toastErr(e.message || '设置失败')
  }
}

/** 录制快捷键。 */
function captureKey(e) {
  e.preventDefault()
  const parts = []
  if (e.ctrlKey) parts.push('Ctrl')
  if (e.altKey) parts.push('Alt')
  if (e.shiftKey) parts.push('Shift')
  if (e.metaKey) parts.push('Super')
  const k = e.key
  if (!['Control', 'Alt', 'Shift', 'Meta'].includes(k)) {
    parts.push(k.length === 1 ? k.toUpperCase() : k)
  }
  if (parts.length >= 2) bossKey.value = parts.join('+')
}

/** 全屏观感。 */
async function toggleFullscreenLook() {
  fullscreenLook.value = !fullscreenLook.value
  try {
    await api.SetFullscreenLook(fullscreenLook.value)
    ui.toastOk(fullscreenLook.value ? '已开启全屏观感' : '已关闭全屏观感')
  } catch (e) {
    fullscreenLook.value = !fullscreenLook.value
    ui.toastErr(e.message || '设置失败')
  }
}

/** 窗口按钮可见。 */
async function toggleWindowButtons() {
  windowButtons.value = !windowButtons.value
  try {
    await api.SetWindowButtonsVisible(windowButtons.value)
    ui.setWindowButtonsVisible(windowButtons.value)
  } catch (e) {
    windowButtons.value = !windowButtons.value
    ui.toastErr(e.message || '设置失败')
  }
}

/** 修复类操作。 */
async function fix(what) {
  busy.value = what
  try {
    if (what === 'firewall') await api.FixFirewall()
    else if (what === 'loopback') await api.FixLoopbackProxy()
    ui.toastOk('已修复')
  } catch (e) {
    ui.toastErr(e.message || '修复失败')
  } finally {
    busy.value = ''
  }
}

/** 硬解组件。 */
async function hevcStatus() {
  try {
    const s = await api.HEVCStatus()
    const ok = s && (s.installed || s.Installed)
    ui.toast(ok ? '硬解组件已就绪' : '硬解组件未安装', ok ? 'ok' : 'warn')
  } catch {
    ui.toastWarn('无法获取硬解状态')
  }
}

async function revealHevc() {
  try {
    await api.RevealHEVCInstaller()
  } catch (e) {
    ui.toastErr(e.message || '打开失败')
  }
}

onMounted(async () => {
  loadAll()
  bossKey.value = ''
  dirNotice.value = true
  // 读回已保存的启动页，保证下拉框显示当前值。
  const v = player.prefs && player.prefs.startPage
  if (v && START_PAGES.some((n) => n.path === v)) startPage.value = v
})
</script>

<template>
  <div class="v-root">
    <div class="v-head">
      <div>
        <h1>设置</h1>
        <p class="v-head-sub">改动立即生效</p>
      </div>
    </div>

    <div class="v-body">
      <div class="st-inner">
        <!-- 外观 -->
        <section class="st-sec">
          <h2 class="st-sec-title">外观</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">主题</span>
              <span class="st-desc">当前：{{ themeLabel }}</span>
            </div>
            <button class="btn btn-sm" @click="toggleTheme">切换</button>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">侧栏收起</span>
              <span class="st-desc">只显示图标，给内容留更多空间</span>
            </div>
            <label class="st-switch">
              <input type="checkbox" :checked="ui.sidebarMini" @change="toggleSidebar" />
              <span class="st-switch-track" />
            </label>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">全屏观感</span>
              <span class="st-desc">全屏时隐藏所有界面元素，只留视频</span>
            </div>
            <label class="st-switch">
              <input type="checkbox" :checked="fullscreenLook" @change="toggleFullscreenLook" />
              <span class="st-switch-track" />
            </label>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">显示窗口按钮</span>
              <span class="st-desc">右上角最小化 / 最大化 / 关闭</span>
            </div>
            <label class="st-switch">
              <input type="checkbox" :checked="windowButtons" @change="toggleWindowButtons" />
              <span class="st-switch-track" />
            </label>
          </div>
        </section>

        <!-- 启动页设置 -->
        <section class="st-sec">
          <h2 class="st-sec-title">启动</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">启动页</span>
              <span class="st-desc">每次打开程序先落到这一页</span>
            </div>
            <select
              class="select st-select"
              :value="startPage"
              @change="setStartPage($event.target.value)"
            >
              <option v-for="n in START_PAGES" :key="n.path" :value="n.path">
                {{ n.label }}
              </option>
            </select>
          </div>
        </section>

        <!-- 播放 -->
        <section class="st-sec">
          <h2 class="st-sec-title">播放</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">默认清晰度</span>
              <span class="st-desc">打开播放页时优先选择</span>
            </div>
            <select
              class="select st-select"
              :value="player.prefs.defaultQuality || '720p'"
              @change="setPref('defaultQuality', $event.target.value)"
            >
              <option v-for="q in QUALITIES" :key="q" :value="q">{{ q }}</option>
            </select>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">自动连播</span>
              <span class="st-desc">一集结束后自动播下一集</span>
            </div>
            <label class="st-switch">
              <input
                type="checkbox"
                :checked="player.prefs.autoNext !== false"
                @change="setPref('autoNext', $event.target.checked)"
              />
              <span class="st-switch-track" />
            </label>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">弹幕</span>
              <span class="st-desc">默认开启弹幕显示</span>
            </div>
            <label class="st-switch">
              <input
                type="checkbox"
                :checked="player.prefs.danmaku !== false"
                @change="setPref('danmaku', $event.target.checked)"
              />
              <span class="st-switch-track" />
            </label>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">弹幕不透明度</span>
              <span class="st-desc">{{ player.prefs.opacity || 80 }}%</span>
            </div>
            <input
              class="st-range"
              type="range"
              min="20"
              max="100"
              step="5"
              :value="player.prefs.opacity || 80"
              @input="setPref('opacity', Number($event.target.value))"
            />
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">硬件解码</span>
              <span class="st-desc">HEVC 硬解组件状态与诊断</span>
            </div>
            <div class="st-row-acts">
              <button class="btn btn-sm" @click="hevcStatus">查看状态</button>
              <button class="btn btn-sm btn-ghost" @click="revealHevc">定位安装包</button>
            </div>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">播放期间阻止休眠</span>
              <span class="st-desc">播放时系统不会自动睡眠</span>
            </div>
            <label class="st-switch">
              <input
                type="checkbox"
                :checked="true"
                @change="api.KeepAwake($event.target.checked)"
              />
              <span class="st-switch-track" />
            </label>
          </div>
        </section>

        <!-- 下载 -->
        <section class="st-sec">
          <h2 class="st-sec-title">下载</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">同时下载数</span>
              <span class="st-desc">同时进行的下载任务上限</span>
            </div>
            <select
              class="select st-select"
              :value="player.prefs.maxConcurrent || 3"
              @change="setPref('maxConcurrent', Number($event.target.value))"
            >
              <option v-for="n in CONCURRENCY" :key="n" :value="n">{{ n }}</option>
            </select>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">下载目录</span>
              <span class="st-desc ellipsis">
                {{ (dataDir && (dataDir.downloadDir || dataDir.dir || dataDir.path)) || '默认目录' }}
              </span>
            </div>
            <button class="btn btn-sm" @click="downloader.openDir('')">打开</button>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">数据目录</span>
              <span class="st-desc ellipsis">
                {{ (dataDir && (dataDir.dataDir || dataDir.dir || dataDir.path)) || '—' }}
              </span>
            </div>
            <button class="btn btn-sm" :disabled="!!busy" @click="pickDir">
              {{ busy === 'pick-dir' ? '选择中…' : busy === 'move-dir' ? '迁移中…' : '迁移' }}
            </button>
          </div>

          <div v-if="dirNotice" class="st-note">
            <span>提示：数据目录包含缓存、数据库与下载文件，迁移前建议关闭下载任务。</span>
            <button class="st-note-x" @click="dismissNotice">✕</button>
          </div>
        </section>

        <!-- 隐私与窗口 -->
        <section class="st-sec">
          <h2 class="st-sec-title">隐私与窗口</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">老板键</span>
              <span class="st-desc">按下组合键后快速隐藏窗口</span>
            </div>
            <div class="st-row-acts">
              <input
                class="input st-keyinput"
                :value="bossKey"
                placeholder="点击后按组合键"
                readonly
                @keydown="captureKey"
                @focus="$event.target.select()"
              />
              <button class="btn btn-sm" @click="saveBossKey">保存</button>
            </div>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">隐身模式</span>
              <span class="st-desc">隐藏窗口并暂停播放</span>
            </div>
            <div class="st-row-acts">
              <button class="btn btn-sm" @click="api.StealthStart()">开启</button>
              <button class="btn btn-sm btn-ghost" @click="api.StealthStop()">关闭</button>
            </div>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">迷你窗口</span>
              <span class="st-desc">缩小成小窗，仅保留播放</span>
            </div>
            <div class="st-row-acts">
              <button class="btn btn-sm" @click="api.SetWindowMode('mini')">迷你</button>
              <button class="btn btn-sm btn-ghost" @click="api.SetWindowMode('normal')">还原</button>
            </div>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">老板键直切</span>
              <span class="st-desc">立即触发一次隐藏 / 显示</span>
            </div>
            <button class="btn btn-sm" @click="api.BossToggle()">触发</button>
          </div>
        </section>

        <!-- 数据与维护 -->
        <section class="st-sec">
          <h2 class="st-sec-title">数据与维护</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">清理接口缓存</span>
              <span class="st-desc">清空本地接口缓存</span>
            </div>
            <button class="btn btn-sm" :disabled="!!busy" @click="clearCache">
              {{ busy === 'cache' ? '清理中…' : '清理' }}
            </button>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">修复防火墙规则</span>
              <span class="st-desc">无法联网时尝试修复 Windows 防火墙</span>
            </div>
            <div class="st-row-acts">
              <button class="btn btn-sm" :disabled="!!busy" @click="fix('firewall')">修复</button>
              <button class="btn btn-sm btn-ghost" @click="api.OpenFirewallSettings()">设置页</button>
            </div>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">修复 WebView2 回环代理</span>
              <span class="st-desc">页面白屏或资源加载失败时使用</span>
            </div>
            <button class="btn btn-sm" :disabled="!!busy" @click="fix('loopback')">修复</button>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">完整性校验</span>
              <span class="st-desc ellipsis">
                {{ integrity ? (integrity.ok ? '通过' : integrity.message || '异常') : '—' }}
              </span>
            </div>
            <button class="btn btn-sm" @click="loadAll">重新校验</button>
          </div>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">本地待同步进度</span>
              <span class="st-desc">查看尚未上报到服务器的播放进度</span>
            </div>
            <button
              class="btn btn-sm"
              @click="api.LocalPending().then((r) => ui.toast(`待同步 ${Array.isArray(r) ? r.length : 0} 条`, 'info'))"
            >
              查看
            </button>
          </div>
        </section>

        <!-- 更新 -->
        <section class="st-sec">
          <h2 class="st-sec-title">更新</h2>

          <div class="st-row">
            <div class="st-row-main">
              <span class="st-label">当前状态</span>
              <span class="st-desc">{{ updateStatusText }}</span>
            </div>
            <div class="st-row-acts">
              <button class="btn btn-sm" :disabled="!!busy" @click="checkUpdate">检查</button>
              <button
                v-if="(updateState && updateState.phase) === 'available'"
                class="btn btn-sm btn-primary"
                :disabled="!!busy"
                @click="downloadUpdate"
              >
                下载
              </button>
              <button
                v-if="(updateState && updateState.phase) === 'ready'"
                class="btn btn-sm btn-primary"
                @click="applyUpdate"
              >
                重启更新
              </button>
            </div>
          </div>

          <div v-if="remoteConfig" class="st-row">
            <div class="st-row-main">
              <span class="st-label">远端配置</span>
              <span class="st-desc ellipsis">
                已加载 {{ Object.keys(remoteConfig).length }} 项
              </span>
            </div>
            <button class="btn btn-sm btn-ghost" @click="router.push({ name: 'settings' })">详情</button>
          </div>
        </section>

        <!-- 其它 -->
        <section class="st-sec">
          <h2 class="st-sec-title">其它</h2>
          <div class="st-links">
            <button class="st-link" @click="router.push({ name: 'settings' })">关于</button>
            <button class="st-link st-link-danger" @click="api.RestartApp()">重启应用</button>
          </div>
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
.st-inner {
  width: 100%;
  max-width: 720px;
  padding-bottom: var(--s-8);
}

.st-sec {
  margin-bottom: var(--s-6);
}
.st-sec-title {
  font-size: var(--f-4);
  font-weight: 600;
  margin-bottom: var(--s-3);
  padding-bottom: var(--s-2);
  border-bottom: 1px solid var(--c-line-soft);
}

.st-row {
  display: flex;
  align-items: center;
  gap: var(--s-4);
  padding: var(--s-3) 0;
  min-height: 52px;
}
.st-row + .st-row {
  border-top: 1px solid var(--c-line-soft);
}

.st-row-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.st-label {
  font-size: var(--f-3);
  color: var(--c-text);
}
.st-desc {
  font-size: var(--f-2);
  color: var(--c-text-mute);
  line-height: 1.5;
}

.st-row-acts {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--s-2);
}

.st-select {
  flex: 0 0 auto;
  width: 108px;
  height: 30px;
  font-size: var(--f-2);
}

.st-range {
  flex: 0 0 auto;
  width: 160px;
  accent-color: var(--c-brand);
}

.st-keyinput {
  width: 150px;
  height: 30px;
  font-size: var(--f-2);
  font-family: var(--font-num);
  cursor: pointer;
}

/* ---- 开关 ---- */
.st-switch {
  position: relative;
  flex: 0 0 auto;
  display: inline-block;
  width: 40px;
  height: 22px;
  cursor: pointer;
}
.st-switch input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}
.st-switch-track {
  position: absolute;
  inset: 0;
  border-radius: var(--r-full);
  background: var(--c-bg-active);
  transition: background var(--t-fast);
}
.st-switch-track::after {
  content: '';
  position: absolute;
  left: 3px;
  top: 3px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  transition: transform var(--t-fast);
}
.st-switch input:checked + .st-switch-track {
  background: var(--c-brand);
}
.st-switch input:checked + .st-switch-track::after {
  transform: translateX(18px);
}
.st-switch input:focus-visible + .st-switch-track {
  outline: 2px solid var(--c-brand);
  outline-offset: 2px;
}

/* ---- 提示条 ---- */
.st-note {
  display: flex;
  align-items: flex-start;
  gap: var(--s-2);
  padding: var(--s-2) var(--s-3);
  margin-top: var(--s-2);
  font-size: var(--f-2);
  color: var(--c-info);
  background: var(--c-info-dim);
  border-radius: var(--r-2);
  line-height: 1.6;
}
.st-note-x {
  flex: 0 0 auto;
  color: var(--c-info);
  opacity: 0.7;
  font-size: var(--f-1);
}
.st-note-x:hover {
  opacity: 1;
}

/* ---- 链接 ---- */
.st-links {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}
.st-link {
  height: 32px;
  padding: 0 var(--s-4);
  border-radius: var(--r-2);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  font-size: var(--f-2);
  color: var(--c-text-dim);
  transition: background var(--t-fast), color var(--t-fast);
}
.st-link:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.st-link-danger:hover {
  background: var(--c-err-dim);
  color: var(--c-err);
}
</style>

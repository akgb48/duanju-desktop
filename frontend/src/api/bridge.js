/**
 * bridge.js —— 前端与 Go 后端之间唯一的通道。
 *
 *   1. 后端把 `internal/app.App` 的方法绑定到 `window.go.main.App.<Method>`。
 *      注意包名是 `main`，不是 `app`——Wails 以 main 包的包名做绑定命名空间。
 *   2. 所有调用统一走 invoke()，由它负责：
 *        - 等待 Wails 就绪（WebView2 注入是异步的）
 *        - 参数/返回值 JSON 兜底
 *        - 后端返回 {code, message} 时抛出可读错误
 *        - 超时
 *        - Wails 缺失时回落 mock（纯浏览器调试）
 *   3. 组件不允许直接碰 window.go，只能 import 本文件导出的绑定对象。
 *
 * 方法总数 118，按后端职责分 9 组。
 */

// ---------------------------------------------------------------------------
// 运行环境探测
// ---------------------------------------------------------------------------

/** 浏览器调试模式下为 true：没有 Wails 注入。 */
export const IS_MOCK = false

function getBackend() {
  return typeof window !== 'undefined' && (window.go?.app?.App || window.go?.main?.App)
}

/** Wails 是否已经注入绑定（可能比首屏晚）。 */
function bindingsReady() {
  return !!getBackend()
}

/** 已解析的后端对象缓存。 */
let appRef = null

/**
 * 等待后端绑定可用。
 * WebView2 里 runtime 脚本与 go 绑定并非同步注入，首屏第一帧就可能调过来，
 * 所以这里做一次轮询等待。默认 8 秒，超过则判定失败。
 */
function waitForBindings(timeoutMs = 8000) {
  if (appRef) return Promise.resolve(appRef)
  if (bindingsReady()) {
    appRef = getBackend()
    return Promise.resolve(appRef)
  }
  return new Promise((resolve, reject) => {
    const started = Date.now()
    const tick = () => {
      if (bindingsReady()) {
        appRef = getBackend()
        return resolve(appRef)
      }
      if (Date.now() - started >= timeoutMs) {
        return reject(new BridgeError('BACKEND_UNAVAILABLE', '未能连接本地后端，请重启应用'))
      }
      setTimeout(tick, 40)
    }
    tick()
  })
}

// ---------------------------------------------------------------------------
// 错误类型
// ---------------------------------------------------------------------------

/** 后端业务错误或传输错误的统一表示。 */
export class BridgeError extends Error {
  constructor(code, message, extra) {
    super(message || '未知错误')
    this.name = 'BridgeError'
    this.code = code
    this.extra = extra || null
  }
  /** 是否属于「需要重新登录」类错误。 */
  get isAuthError() {
    return (
      this.code === 401 ||
      this.code === 10008 ||
      /未登录|登录.{0,4}失效|尚未登录/.test(this.message)
    )
  }
}

/** 传输层错误码（不来自后端）。 */
export const ERR = {
  TIMEOUT: 'TIMEOUT',
  BACKEND_UNAVAILABLE: 'BACKEND_UNAVAILABLE',
  BAD_RESPONSE: 'BAD_RESPONSE',
  UNKNOWN_METHOD: 'UNKNOWN_METHOD',
}

/** 默认超时：普通请求 20 秒。 */
const DEFAULT_TIMEOUT = 20000

/** 慢方法单独放宽超时。 */
const SLOW_TIMEOUT = {
  Search: 40000,
  SeriesDetail: 40000,
  Category: 40000,
  Rank: 40000,
  DownloadSeries: 60000,
  AddDownloads: 60000,
  NativeDecOpen: 45000,
  NativeDecPrepare: 45000,
  Login: 60000,
  ApplyUpdate: 120000,
  DownloadUpdate: 300000,
  CheckUpdate: 90000,
}

/**
 * 把后端可能抛出的各种形态规整成 BridgeError。
 *
 * Wails v2 的失败路径有两种：
 *   a) Promise reject，值通常是 Error 或 string；
 *   b) resolve 出 {code, message} 这类业务信封。
 * 两者都要转成可读错误。
 */
function normalizeError(method, raw) {
  if (raw instanceof BridgeError) return raw

  // 字符串：可能是后端 error.Error() 或 JSON
  if (typeof raw === 'string') {
    const parsed = tryJSON(raw)
    if (parsed && typeof parsed === 'object') return normalizeError(method, parsed)
    return new BridgeError(-1, raw || `${method} 调用失败`)
  }

  if (raw && typeof raw === 'object') {
    const code = pick(raw, ['code', 'Code', 'errCode', 'ErrCode', 'err_no', 'status_code'])
    const msg = pick(raw, [
      'message',
      'Message',
      'msg',
      'Msg',
      'errMsg',
      'ErrMsg',
      'err_tips',
      'detail',
      'error',
    ])
    // 明确是业务失败信封
    if (code !== undefined && code !== null && Number(code) !== 0) {
      return new BridgeError(Number(code), msg || `${method} 失败（code=${code}）`, raw)
    }
    if (msg && !code) {
      return new BridgeError(-1, String(msg), raw)
    }
  }

  if (raw instanceof Error) return new BridgeError(-1, raw.message, raw)
  return new BridgeError(-1, `${method} 调用失败`)
}

function pick(obj, keys) {
  for (const k of keys) {
    if (obj[k] !== undefined && obj[k] !== null && obj[k] !== '') return obj[k]
  }
  return undefined
}

function tryJSON(s) {
  const t = s.trim()
  if (!t || (t[0] !== '{' && t[0] !== '[')) return null
  try {
    return JSON.parse(t)
  } catch {
    return null
  }
}

/**
 * 后端的 Go 方法返回 (T, error)。Wails 把 error 走 reject，
 * 把 nil 结果走 resolve(null)。这里把 null 原样返回，让调用方决定默认值。
 */

// ---------------------------------------------------------------------------
// 核心 invoke
// ---------------------------------------------------------------------------

/**
 * 调用后端方法。
 * @param {string} name   方法名，必须与 Go 侧导出一致（首字母大写）
 * @param {...any} args   参数，顺序与 Go 方法签名一致
 * @returns {Promise<any>}
 */
export async function invoke(name, ...args) {
  if (!METHODS.has(name)) {
    throw new BridgeError(ERR.UNKNOWN_METHOD, `未注册的后端方法：${name}`)
  }

  // 浏览器预览与桌面版共用真实 Go 服务。
  if (!getBackend() && !window.runtime) {
    const response = await fetch(`/api/invoke/${encodeURIComponent(name)}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-HG-Local': '1' }, body: JSON.stringify(args),
    })
    const result = await response.json()
    if (!response.ok || result.error) throw new BridgeError(response.status, result.error || '本地服务调用失败')
    return result.data
  }

  let app
  try {
    app = await waitForBindings()
  } catch (e) {
    throw e
  }

  const fn = app[name]
  if (typeof fn !== 'function') {
    throw new BridgeError(ERR.UNKNOWN_METHOD, `后端未绑定方法：${name}`)
  }

  const timeout = SLOW_TIMEOUT[name] || DEFAULT_TIMEOUT

  return new Promise((resolve, reject) => {
    let settled = false
    const timer = setTimeout(() => {
      if (settled) return
      settled = true
      reject(new BridgeError(ERR.TIMEOUT, `${name} 请求超时（${Math.round(timeout / 1000)} 秒）`))
    }, timeout)

    const done = (fn2, v) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      fn2(v)
    }

    let ret
    try {
      ret = fn(...args)
    } catch (e) {
      return done(reject, normalizeError(name, e))
    }

    if (!ret || typeof ret.then !== 'function') {
      // 极少数同步返回的绑定
      return done(resolve, ret)
    }

    ret.then(
      (v) => {
        // resolve 出业务错误信封时也要抛
        const err = asBizError(name, v)
        if (err) done(reject, err)
        else done(resolve, v)
      },
      (e) => done(reject, normalizeError(name, e)),
    )
  })
}

/** 若 resolve 值是明确的业务失败信封则返回 BridgeError，否则 null。 */
function asBizError(method, v) {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return null
  const code = pick(v, ['code', 'Code', 'errCode', 'ErrCode'])
  if (code === undefined || code === null) return null
  // 后端成功约定：code=0 或 code=200
  if (Number(code) === 0 || Number(code) === 200) return null
  const msg = pick(v, ['message', 'Message', 'msg', 'Msg', 'errMsg', 'ErrMsg'])
  return new BridgeError(Number(code), msg || `${method} 失败（code=${code}）`, v)
}

// ---------------------------------------------------------------------------
// 事件通道（后端 → 前端，17 个）
// ---------------------------------------------------------------------------

/**
 * 后端经 runtime.EventsEmit 推送的事件名。
 */
export const EVENTS = {
  AUTH_MFA: 'auth:mfa',
  AUTH_EXPIRED: 'auth:expired',
  DOWNLOAD_LIST: 'download:list',
  DOWNLOAD_TASK: 'download:task',
  DEPS_HEVC: 'deps:hevc',
  PLAY_SPARE: 'play:spare',
  BOSS_HIDE: 'boss:hide',
  BOSS_SHOW: 'boss:show',
  WINDOW_MINIMISE: 'window:minimise',
  WINDOW_RESTORE: 'window:restore',
  NOTICE_NEW: 'notice:new',
  STEALTH_SHOW: 'stealth:show',
  STEALTH_HIDE: 'stealth:hide',
  UPDATE_STATE: 'update:state',
  NATIVEDEC: 'nativedec',
  CLIPBOARD_CHANGED: 'clipboard:changed',
  RISK_VERIFY: 'risk:verify',
}

/**
 * 订阅后端事件，返回取消函数。
 * Wails 侧用 EventsOnMultiple(name, cb, -1)，-1 表示不自动摘除，
 * 因此必须由组件在卸载时显式调用返回值。
 */
export function on(name, handler) {
  if (typeof window === 'undefined' || !window.runtime || !window.runtime.EventsOnMultiple) {
    // 浏览器调试：不订阅
    return () => {}
  }
  const off = window.runtime.EventsOnMultiple(name, handler, -1)
  return typeof off === 'function' ? off : () => {}
}

/** 取消订阅（按 Wails 提供的 EventsOff 兜底）。 */
export function off(name) {
  if (typeof window !== 'undefined' && window.runtime && window.runtime.EventsOff) {
    window.runtime.EventsOff(name)
  }
}

// ---------------------------------------------------------------------------
// 方法白名单（118 个，按职责分组）
// ---------------------------------------------------------------------------

const GROUPS = {
  // ---- 账号 ----
  account: [
    'Login', // (phone, code) 手机号+验证码登录
    'Logout', // () 退出登录，清本地账号与缓存
    'SendCode', // (phone) 发送短信验证码
    'QRLoginStart', // (region) 生成扫码登录二维码
    'QRLoginPoll', // () 轮询扫码状态
    'QRLoginCancel', // () 取消扫码会话
    'LoginRegions', // () 可选登录区域列表
    'RetryDeviceRegister', // () 重试设备注册（风控前置）
    'Status', // () 当前登录态与设备信息快照
  ],

  // ---- 内容 ----
  content: [
    'RecommendTab', // (tabType, cursor) 首页推荐信息流
    'FeedCategories', // () 首页可用 tab 列表
    'Category', // (genreId, cursor) 分类落地页
    'SelectorPanel', // (genreId) 分类筛选面板
    'Rank', // (rankType, cursor) 榜单
    'RankPanel', // () 榜单维度面板
    'NewSeries', // (cursor, tabType) 新剧集
    'Search', // (keyword, cursor) 搜索结果
    'SearchSuggest', // (keyword) 搜索联想
    'SeriesDetail', // (seriesId) 剧集详情
    'SeriesSeasons', // (seriesId) 剧集季列表
    'NextSeason', // (seriesId, seasonIndex) 下一季
    'Related', // (seriesId) 相关推荐
  ],

  // ---- 播放 ----
  play: [
    'PlayInfo', // (seriesId, episode) 取播放地址与元信息
    'PrefetchPlay', // (seriesId) 预取下一集
    'PrefetchPlayItems', // (seriesId) 批量预取
    'SaveProgress', // (seriesId, episode, positionMs, durationMs, done) 上报进度
    'History', // (cursor) 观看历史
    'DeleteHistory', // (seriesId) 删除单条历史
    'LocalPending', // () 本地待同步进度
    'DiagnoseLocalPlay', // () 本地播放诊断
    'HEVCStatus', // () 硬解组件状态
    'HEVCDiag', // () 硬解诊断详情
    'RevealHEVCInstaller', // () 在资源管理器里定位硬解安装包
    'NativeDecOpen', // (seriesId, episode) 打开原生解码会话
    'NativeDecPrepare', // () 准备解码器
    'NativeDecSeek', // (sessionId, positionMs) 原生解码定位
    'NativeDecPreload', // (seriesId, episode) 原生解码预载
    'NativeDecClose', // (sessionId) 关闭原生解码会话
    'NativeDecSetCPUOnly', // (enabled) 强制软解
    'NativeDecStatus', // () 原生解码会话状态
  ],

  // ---- 评论 ----
  comment: [
    'Comments', // (seriesId, videoId, dataType, cursor) 评论列表
    'Danmaku', // (seriesId, videoId, offsetMs, limit) 弹幕列表
    'Replies', // (commentId, cursor, pageSize, sortType) 回复列表
    'Reviews', // (seriesId, cursor) 剧评列表
    'ReviewReplies', // (reviewId, cursor, pageSize) 剧评回复
    'SendComment', // (seriesId, videoId, text, replyToId, dataType) 发评论
    'SendDanmaku', // (seriesId, videoId, text, offsetMs, color) 发弹幕
    'SendReply', // (seriesId, videoId, commentId, replyToReplyId, replyToUserId, text, imageUrl, dataType, offsetMs) 发回复
    'SendReview', // (seriesId, text, score) 发剧评
    'SendReviewReply', // (reviewId, text, replyToId, ...) 回剧评
    'DeleteComment', // (commentId, dataType) 删除
    'LikeComment', // (commentId, liked) 赞评论
    'LikeDanmaku', // (danmakuId, offsetMs, videoId, liked) 赞弹幕
    'LikeReview', // (reviewId, liked) 赞剧评
    'Likes', // (videoId, cursor) 我的点赞列表
    'RemoveLikes', // (ids) 批量取消点赞
    'SetLike', // (videoId, liked, dataType) 设置点赞
    'Emoticons', // (seriesId, packId) 表情列表
    'CollectEmoticon', // (emoticonId, collected) 收藏表情
    'UploadPicture', // (filename, base64) 上传图片（评论配图）
    'UploadLocal', // () 从本地选择图片上传
  ],

  // ---- 收藏 ----
  favorite: [
    'Favorites', // (cursor, isFirstLoad) 追剧书架
    'SetFavorite', // (seriesId, favorite) 加/取消追剧
    'RemoveFavorites', // (ids) 批量移除
    'FollowUpdates', // (cursor) 追更动态
    'IsFollowingUpdate', // (seriesId) 是否在追更
    'SetFollowUpdate', // (seriesId, follow) 设置追更
    'FollowUser', // (userId, follow) 关注作者
    'Reservations', // (cursor, tabType) 我的预约
    'SetReservation', // (seriesId, reserved) 设置预约
    'DeleteReservations', // (ids, all) 删除预约
    'Calendar', // (year, month) 预约日历
  ],

  // ---- 下载 ----
  download: [
    'Downloads', // () 下载任务全量
    'AddDownloads', // (seriesId, episodeFrom, episodeTo) 加入下载
    'DeleteDownloads', // (ids) 删除任务
    'PauseDownloads', // (ids) 暂停
    'ResumeDownloads', // (ids) 继续
    'DownloadSeries', // (seriesId) 整剧下载
    'OpenDownloadDir', // (id) 打开下载目录
  ],

  // ---- 设置 ----
  settings: [
    'BossToggle', // () 老板键切换
    'ClearCache', // () 清空接口缓存
    'DismissDataDirNotice', // () 关闭数据目录提示
    'GetDataDirInfo', // () 数据目录信息
    'KeepAwake', // (enabled) 播放期间阻止休眠
    'MoveDataDir', // (path) 迁移数据目录
    'PickDataDir', // () 选择数据目录
    'RemoteConfig', // () 远端配置快照
    'RestoreMiniWindow', // () 从迷你窗恢复
    'SetBossKey', // (accelerator) 设置老板键
    'SetFullscreenLook', // (enabled) 全屏观感
    'SetPlaybackPrefs', // (prefs) 播放偏好
    'SetWindowButtonsVisible', // (visible) 显示/隐藏窗口按钮
    'SetWindowMode', // (mode) 普通/迷你/全屏
    'StealthStart', // () 开启隐身
    'StealthStop', // () 关闭隐身
  ],

  // ---- 更新 ----
  update: [
    'CheckUpdate', // () 检查更新
    'ApplyUpdate', // () 应用已下载的更新
    'DownloadUpdate', // () 下载更新包
    'UpdateState', // () 更新器全量状态
  ],

  // ---- 系统 ----
  system: [
    'About', // () 关于信息
    'AppInfo', // () 版本/构建信息
    'CheckNotices', // () 拉取公告
    'ClearNotices', // () 清空公告
    'ClipboardSeq', // () 读剪贴板内容
    'FixFirewall', // () 修复 Windows 防火墙规则
    'FixLoopbackProxy', // () 修复 WebView2 回环代理
    'Integrity', // () 完整性校验结果
    'MarkNoticesRead', // (ids) 标记公告已读
    'MyFeedback', // () 我的反馈
    'Notices', // () 公告列表
    'OpenFirewallSettings', // () 打开防火墙设置页
    'ReplyFeedback', // (feedbackId, text) 追加反馈回复
    'ReportEnv', // (env, extra) 上报环境信息
    'ReportError', // (scope, message, stack) 上报前端错误
    'ResolveShare', // (text) 解析分享链接
    'RestartApp', // () 重启应用
    'ShareLink', // (seriesId, episode) 生成分享链接
    'SubmitFeedback', // (kind, text, contact) 提交反馈
  ],
}

/** 所有合法方法名集合。 */
const METHODS = new Set(Object.values(GROUPS).flat())

/** 方法名 → 分组名。 */
export const METHOD_GROUP = (() => {
  const m = new Map()
  for (const [g, list] of Object.entries(GROUPS)) for (const n of list) m.set(n, g)
  return m
})()

/** 分组的中文别名，供调试面板展示。 */
export const GROUP_LABEL = {
  account: '账号',
  content: '内容',
  play: '播放',
  comment: '评论',
  favorite: '收藏',
  download: '下载',
  settings: '设置',
  update: '更新',
  system: '系统',
}

/** 供调试/自检用：返回全部方法名。 */
export function methodNames() {
  return [...METHODS]
}

/** 分组统计。 */
export function methodStats() {
  return Object.entries(GROUPS).map(([g, list]) => ({
    group: g,
    label: GROUP_LABEL[g],
    count: list.length,
  }))
}

/**
 * 生成一个「按方法名调用」的代理对象，便于 store 层书写：
 *   const api = callable()
 *   await api.getFeed(...)  // 不推荐，仍以显式方法为准
 * 这里只作为兜底，正常路径都走下面的具名导出。
 */
export function callable() {
  return new Proxy(
    {},
    {
      get(_t, key) {
        if (typeof key !== 'string') return undefined
        return (...args) => invoke(key, ...args)
      },
    },
  )
}

// ---------------------------------------------------------------------------
// 具名导出（118 个） —— 组件只应 import 这些
// ---------------------------------------------------------------------------

const call = (name) => (...args) => invoke(name, ...args)

// 账号
export const Login = call('Login')
export const Logout = call('Logout')
export const SendCode = call('SendCode')
export const QRLoginStart = call('QRLoginStart')
export const QRLoginPoll = call('QRLoginPoll')
export const QRLoginCancel = call('QRLoginCancel')
export const LoginRegions = call('LoginRegions')
export const RetryDeviceRegister = call('RetryDeviceRegister')
export const Status = call('Status')

// 内容
export const RecommendTab = call('RecommendTab')
export const FeedCategories = call('FeedCategories')
export const Category = call('Category')
export const SelectorPanel = call('SelectorPanel')
export const Rank = call('Rank')
export const RankPanel = call('RankPanel')
export const NewSeries = call('NewSeries')
export const Search = call('Search')
export const SearchSuggest = call('SearchSuggest')
export const SeriesDetail = call('SeriesDetail')
export const SeriesSeasons = call('SeriesSeasons')
export const NextSeason = call('NextSeason')
export const Related = call('Related')

// 播放
export const PlayInfo = call('PlayInfo')
export const PrefetchPlay = call('PrefetchPlay')
export const PrefetchPlayItems = call('PrefetchPlayItems')
export const SaveProgress = call('SaveProgress')
export const History = call('History')
export const DeleteHistory = call('DeleteHistory')
export const LocalPending = call('LocalPending')
export const DiagnoseLocalPlay = call('DiagnoseLocalPlay')
export const HEVCStatus = call('HEVCStatus')
export const HEVCDiag = call('HEVCDiag')
export const RevealHEVCInstaller = call('RevealHEVCInstaller')
export const NativeDecOpen = call('NativeDecOpen')
export const NativeDecPrepare = call('NativeDecPrepare')
export const NativeDecSeek = call('NativeDecSeek')
export const NativeDecPreload = call('NativeDecPreload')
export const NativeDecClose = call('NativeDecClose')
export const NativeDecSetCPUOnly = call('NativeDecSetCPUOnly')
export const NativeDecStatus = call('NativeDecStatus')

// 评论
export const Comments = call('Comments')
export const Danmaku = call('Danmaku')
export const Replies = call('Replies')
export const Reviews = call('Reviews')
export const ReviewReplies = call('ReviewReplies')
export const SendComment = call('SendComment')
export const SendDanmaku = call('SendDanmaku')
export const SendReply = call('SendReply')
export const SendReview = call('SendReview')
export const SendReviewReply = call('SendReviewReply')
export const DeleteComment = call('DeleteComment')
export const LikeComment = call('LikeComment')
export const LikeDanmaku = call('LikeDanmaku')
export const LikeReview = call('LikeReview')
export const Likes = call('Likes')
export const RemoveLikes = call('RemoveLikes')
export const SetLike = call('SetLike')
export const Emoticons = call('Emoticons')
export const CollectEmoticon = call('CollectEmoticon')
export const UploadPicture = call('UploadPicture')
export const UploadLocal = call('UploadLocal')

// 收藏
export const Favorites = call('Favorites')
export const SetFavorite = call('SetFavorite')
export const RemoveFavorites = call('RemoveFavorites')
export const FollowUpdates = call('FollowUpdates')
export const IsFollowingUpdate = call('IsFollowingUpdate')
export const SetFollowUpdate = call('SetFollowUpdate')
export const FollowUser = call('FollowUser')
export const Reservations = call('Reservations')
export const SetReservation = call('SetReservation')
export const DeleteReservations = call('DeleteReservations')
export const Calendar = call('Calendar')

// 下载
export const Downloads = call('Downloads')
export const AddDownloads = call('AddDownloads')
export const DeleteDownloads = call('DeleteDownloads')
export const PauseDownloads = call('PauseDownloads')
export const ResumeDownloads = call('ResumeDownloads')
export const DownloadSeries = call('DownloadSeries')
export const OpenDownloadDir = call('OpenDownloadDir')

// 设置
export const BossToggle = call('BossToggle')
export const ClearCache = call('ClearCache')
export const DismissDataDirNotice = call('DismissDataDirNotice')
export const GetDataDirInfo = call('GetDataDirInfo')
export const KeepAwake = call('KeepAwake')
export const MoveDataDir = call('MoveDataDir')
export const PickDataDir = call('PickDataDir')
export const RemoteConfig = call('RemoteConfig')
export const RestoreMiniWindow = call('RestoreMiniWindow')
export const SetBossKey = call('SetBossKey')
export const SetFullscreenLook = call('SetFullscreenLook')
export const SetPlaybackPrefs = call('SetPlaybackPrefs')
export const SetWindowButtonsVisible = call('SetWindowButtonsVisible')
export const SetWindowMode = call('SetWindowMode')
export const StealthStart = call('StealthStart')
export const StealthStop = call('StealthStop')

// 更新
export const CheckUpdate = call('CheckUpdate')
export const ApplyUpdate = call('ApplyUpdate')
export const DownloadUpdate = call('DownloadUpdate')
export const UpdateState = call('UpdateState')

// 系统
export const About = call('About')
export const AppInfo = call('AppInfo')
export const CheckNotices = call('CheckNotices')
export const ClearNotices = call('ClearNotices')
export const ClipboardSeq = call('ClipboardSeq')
export const FixFirewall = call('FixFirewall')
export const FixLoopbackProxy = call('FixLoopbackProxy')
export const Integrity = call('Integrity')
export const MarkNoticesRead = call('MarkNoticesRead')
export const MyFeedback = call('MyFeedback')
export const Notices = call('Notices')
export const OpenFirewallSettings = call('OpenFirewallSettings')
export const ReplyFeedback = call('ReplyFeedback')
export const ReportEnv = call('ReportEnv')
export const ReportError = call('ReportError')
export const ResolveShare = call('ResolveShare')
export const RestartApp = call('RestartApp')
export const ShareLink = call('ShareLink')
export const SubmitFeedback = call('SubmitFeedback')

// ---------------------------------------------------------------------------
// 窗口控制（Wails runtime，非 App 绑定）
// ---------------------------------------------------------------------------

const rt = () => (typeof window !== 'undefined' ? window.runtime : null)

/** 最小化窗口。 */
export function WindowMinimise() {
  const r = rt()
  if (r && r.WindowMinimise) return r.WindowMinimise()
  return Promise.resolve()
}
/** 最大化/还原切换。 */
export function WindowToggleMaximise() {
  const r = rt()
  if (r && r.WindowToggleMaximise) return r.WindowToggleMaximise()
  return Promise.resolve()
}
/** 关闭窗口。 */
export function WindowQuit() {
  const r = rt()
  if (r && r.Quit) return r.Quit()
  return Promise.resolve()
}
/** 是否最大化。 */
export function WindowIsMaximised() {
  const r = rt()
  if (r && r.WindowIsMaximised) return Promise.resolve(r.WindowIsMaximised())
  return Promise.resolve(false)
}
/** 全屏切换。 */
export function WindowToggleFullscreen() {
  const r = rt()
  if (r && r.WindowToggleFullscreen) return r.WindowToggleFullscreen()
  return Promise.resolve()
}
/** 拖拽区域由 CSS -webkit-app-region 处理，无需 JS。 */

// ---------------------------------------------------------------------------
// Mock（纯浏览器调试）
// ---------------------------------------------------------------------------

/** 演示数据：用于 npm run dev 时无后端也能看界面。 */
const MOCK_SERIES = [
  {
    id: 'demo-1',
    title: '重生之我在红果当编剧',
    cover: '',
    description: '一觉醒来，他成了自己笔下最讨厌的配角。',
    author: '匿名作者',
    category: '都市',
    status: 'ongoing',
    episodeCount: 80,
    playCount: 1280000,
    likeCount: 32000,
    score: '9.2',
    tags: ['重生', '爽文'],
    updatedAt: 1767225600,
  },
  {
    id: 'demo-2',
    title: '总裁的替身新娘',
    cover: '',
    description: '她签下三年契约，却不知对方早已知晓她的身份。',
    author: '苏晚',
    category: '甜宠',
    status: 'completed',
    episodeCount: 96,
    playCount: 3400000,
    likeCount: 91000,
    score: '8.8',
    tags: ['甜宠', '契约'],
    updatedAt: 1767139200,
  },
  {
    id: 'demo-3',
    title: '退婚后我成了首富',
    cover: '',
    description: '被退婚那天，他继承了千亿家产。',
    author: '夜北',
    category: '逆袭',
    status: 'ongoing',
    episodeCount: 120,
    playCount: 5600000,
    likeCount: 143000,
    score: '9.5',
    tags: ['逆袭', '都市'],
    updatedAt: 1767052800,
  },
  {
    id: 'demo-4',
    title: '长安十二时辰之外',
    cover: '',
    description: '那一天，长安城的钟楼没有响。',
    author: '马伯庸迷',
    category: '古装',
    status: 'completed',
    episodeCount: 60,
    playCount: 2100000,
    likeCount: 56000,
    score: '9.0',
    tags: ['古装', '悬疑'],
    updatedAt: 1766966400,
  },
  {
    id: 'demo-5',
    title: '我的老板是只猫',
    cover: '',
    description: '公司新来的 CEO，坚持每天睡 18 个小时。',
    author: '喵喵',
    category: '搞笑',
    status: 'ongoing',
    episodeCount: 45,
    playCount: 890000,
    likeCount: 21000,
    score: '8.5',
    tags: ['搞笑', '奇幻'],
    updatedAt: 1766880000,
  },
  {
    id: 'demo-6',
    title: '深海来信',
    cover: '',
    description: '海边小镇每隔十年收到一封来自海底的信。',
    author: '沉舟',
    category: '悬疑',
    status: 'ongoing',
    episodeCount: 36,
    playCount: 640000,
    likeCount: 18000,
    score: '8.9',
    tags: ['悬疑', '治愈'],
    updatedAt: 1766793600,
  },
]

function mockFeedItems() {
  return MOCK_SERIES.map((s) => ({
    seriesId: s.id,
    title: s.title,
    cover: s.cover,
    subtitle: s.description,
    badge: s.status === 'ongoing' ? '更新中' : '已完结',
    playCount: s.playCount,
    category: s.category,
    score: s.score,
  }))
}

function mockEpisodes(seriesId, count = 12) {
  return Array.from({ length: count }, (_, i) => ({
    seriesId,
    index: i + 1,
    title: `第 ${i + 1} 集`,
    videoId: `${seriesId}-v${i + 1}`,
    duration: 120 + (i % 5) * 15,
    cover: '',
    playCount: 10000 * (count - i),
    isFree: i < 6,
    publishedAt: 1767225600 - i * 86400,
  }))
}

const MOCK_HANDLERS = {
  Status: () => ({
    loggedIn: true,
    user: { userId: 'demo', nickname: '演示用户', avatar: '', signature: '这是浏览器调试模式', isVip: false, vipExpire: 0 },
    mock: true,
  }),
  AppInfo: () => ({ version: '1.2.16', build: 'dev', mock: true }),
  About: () => ({ version: '1.2.16', author: '', link: '', mock: true }),
  Integrity: () => ({ ok: true, checked: ['assets', 'author'], mock: true }),
  RemoteConfig: () => ({ regions: [{ code: 'cn-gd', name: '华南', host: 'api5-normal-lq.fqnovel.com' }], mock: true }),
  LoginRegions: () => [
    { code: 'cn-gd', name: '华南', host: 'api5-normal-lq.fqnovel.com' },
    { code: 'cn-hl', name: '华北', host: 'api5-normal-hl.fqnovel.com' },
  ],
  FeedCategories: () => [
    { id: 'recommend', name: '推荐' },
    { id: 'rank', name: '榜单' },
    { id: 'new', name: '新剧' },
    { id: 'finished', name: '完结' },
  ],
  RecommendTab: (_tab, cursor) => ({
    items: mockFeedItems(),
    hasMore: !cursor || cursor === '0',
    cursor: '1',
    totalCount: MOCK_SERIES.length,
  }),
  Rank: () => ({ items: mockFeedItems(), hasMore: false, cursor: '', totalCount: MOCK_SERIES.length }),
  RankPanel: () => [
    { id: 'hot', name: '热播榜' },
    { id: 'new', name: '新剧榜' },
    { id: 'collect', name: '收藏榜' },
  ],
  NewSeries: () => ({ items: mockFeedItems(), hasMore: false, cursor: '', totalCount: MOCK_SERIES.length }),
  Category: () => ({ items: mockFeedItems(), hasMore: false, cursor: '', totalCount: MOCK_SERIES.length }),
  SelectorPanel: () => ({ genres: ['都市', '甜宠', '逆袭', '古装', '搞笑', '悬疑'], status: ['ongoing', 'completed'] }),
  Search: (kw) => ({
    series: MOCK_SERIES.filter((s) => !kw || s.title.includes(kw)),
    hasMore: false,
    cursor: '',
    keyword: kw || '',
    total: MOCK_SERIES.length,
  }),
  SearchSuggest: (kw) => (kw ? [`${kw} 全集`, `${kw} 短剧`, `${kw} 完结`] : []),
  SeriesDetail: (id) => {
    const s = MOCK_SERIES.find((x) => x.id === id) || MOCK_SERIES[0]
    return { ...s, episodes: mockEpisodes(s.id, Math.min(s.episodeCount, 12)) }
  },
  SeriesSeasons: (id) => [{ seriesId: id, season: 1, title: '第 1 季' }],
  NextSeason: () => null,
  Related: () => MOCK_SERIES.slice(0, 4),
  PlayInfo: (seriesId, episode) => ({
    seriesId,
    videoId: `${seriesId}-v${episode}`,
    title: `第 ${episode} 集`,
    episode: Number(episode) || 1,
    duration: 135,
    sources: [
      { quality: '720p', url: '', width: 720, height: 1280, bitrate: 1200, format: 'mp4', size: 0 },
    ],
    expireAt: 0,
    mock: true,
  }),
  History: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  LocalPending: () => [],
  Favorites: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  Reservations: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  FollowUpdates: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  Calendar: () => ({ days: [] }),
  Downloads: () => [],
  Comments: () => ({ items: mockComments(), hasMore: false, cursor: '', total: 3 }),
  Danmaku: () => [],
  Replies: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  Reviews: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  ReviewReplies: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  Likes: () => ({ items: [], hasMore: false, cursor: '', total: 0 }),
  Emoticons: () => [],
  Notices: () => [],
  CheckNotices: () => [],
  MarkNoticesRead: () => true,
  ClearNotices: () => true,
  MyFeedback: () => [],
  SubmitFeedback: () => ({ id: 'fb-demo', ok: true }),
  ReplyFeedback: () => true,
  CheckUpdate: () => ({ hasUpdate: false, version: '1.2.16' }),
  UpdateState: () => ({ state: 'idle', hasUpdate: false, version: '1.2.16', mock: true }),
  ApplyUpdate: () => true,
  DownloadUpdate: () => true,
  HEVCStatus: () => ({ supported: true, installed: true, hardware: false, mock: true }),
  HEVCDiag: () => ({ codecs: ['hevc'], mock: true }),
  NativeDecStatus: () => ({ active: false, mock: true }),
  GetDataDirInfo: () => ({ dir: 'C:\\Users\\demo\\AppData\\Local\\hgplayer', sizeBytes: 0, mock: true }),
  DiagnoseLocalPlay: () => ({ ok: true, items: [], mock: true }),
  ClipboardSeq: () => '',
  ResolveShare: (t) => ({ seriesId: 'demo-1', text: t, mock: true }),
  ShareLink: (id) => ({ url: `https://example.invalid/s/${id}`, mock: true }),
  Login: () => ({ userId: 'demo', nickname: '演示用户', avatar: '', signature: '', isVip: false, vipExpire: 0 }),
  Logout: () => true,
  SendCode: () => true,
  QRLoginStart: () => ({ qrId: 'demo-qr', url: 'https://example.invalid/qr', expireAt: 0 }),
  QRLoginPoll: () => ({ state: 'waiting' }),
  QRLoginCancel: () => true,
  RetryDeviceRegister: () => true,
  SaveProgress: () => true,
  DeleteHistory: () => true,
  SetFavorite: () => true,
  RemoveFavorites: () => true,
  SetFollowUpdate: () => true,
  FollowUser: () => true,
  IsFollowingUpdate: () => false,
  SetReservation: () => true,
  DeleteReservations: () => true,
  SetLike: () => true,
  RemoveLikes: () => true,
  LikeComment: () => true,
  LikeDanmaku: () => true,
  LikeReview: () => true,
  SendComment: () => 'demo-c-1',
  SendDanmaku: () => 'demo-d-1',
  SendReply: () => 'demo-r-1',
  SendReview: () => 'demo-rv-1',
  SendReviewReply: () => 'demo-rv-r-1',
  DeleteComment: () => true,
  CollectEmoticon: () => true,
  UploadPicture: () => '',
  UploadLocal: () => '',
  ClearCache: () => true,
  KeepAwake: () => true,
  MoveDataDir: () => true,
  PickDataDir: () => 'C:\\demo',
  DismissDataDirNotice: () => true,
  SetBossKey: () => true,
  BossToggle: () => true,
  SetFullscreenLook: () => true,
  SetPlaybackPrefs: () => true,
  SetWindowButtonsVisible: () => true,
  SetWindowMode: () => true,
  StealthStart: () => true,
  StealthStop: () => true,
  RestoreMiniWindow: () => true,
  AddDownloads: () => true,
  DeleteDownloads: () => true,
  PauseDownloads: () => true,
  ResumeDownloads: () => true,
  DownloadSeries: () => true,
  OpenDownloadDir: () => true,
  PrefetchPlay: () => true,
  PrefetchPlayItems: () => true,
  RevealHEVCInstaller: () => true,
  NativeDecOpen: () => null,
  NativeDecPrepare: () => null,
  NativeDecSeek: () => true,
  NativeDecPreload: () => true,
  NativeDecClose: () => true,
  NativeDecSetCPUOnly: () => true,
  FixFirewall: () => true,
  FixLoopbackProxy: () => true,
  OpenFirewallSettings: () => true,
  ReportEnv: () => true,
  ReportError: () => {
    // 把错误打到控制台，方便浏览器调试
    return true
  },
  RestartApp: () => true,
}

function mockComments() {
  const now = Math.floor(Date.now() / 1000)
  return [
    {
      id: 'c1',
      seriesId: 'demo-1',
      videoId: 'demo-1-v1',
      parentId: '',
      userId: 'u1',
      nickname: '看剧上头',
      avatar: '',
      text: '这剧真的越看越上瘾，一口气刷了二十集。',
      likeCount: 128,
      replyCount: 3,
      isLiked: false,
      createdAt: now - 3600,
      dataType: 4,
      offsetMs: 0,
    },
    {
      id: 'c2',
      seriesId: 'demo-1',
      videoId: 'demo-1-v1',
      parentId: '',
      userId: 'u2',
      nickname: '追剧小分队',
      avatar: '',
      text: '男主这个眼神绝了。',
      likeCount: 46,
      replyCount: 0,
      isLiked: true,
      createdAt: now - 7200,
      dataType: 4,
      offsetMs: 0,
    },
    {
      id: 'c3',
      seriesId: 'demo-1',
      videoId: 'demo-1-v1',
      parentId: '',
      userId: 'u3',
      nickname: '夜猫子',
      avatar: '',
      text: '第三集反转没想到。',
      likeCount: 9,
      replyCount: 1,
      isLiked: false,
      createdAt: now - 10800,
      dataType: 4,
      offsetMs: 0,
    },
  ]
}

async function mockInvoke(name, args) {
  // 模拟网络延迟，便于观察 loading 态
  await new Promise((r) => setTimeout(r, 60 + Math.random() * 120))
  const h = MOCK_HANDLERS[name]
  if (typeof h === 'function') return h(...args)
  // 未显式 mock 的写操作一律返回 true，读操作返回 null
  if (/^(Set|Send|Add|Delete|Remove|Like|Clear|Mark|Move|Pick|Keep|Boss|Stealth|Fix|Report|Restart|Apply|Download|Pause|Resume|Open|Reveal|Retry|Dismiss|Restore|Collect|Upload|Save|Prefetch|Native)/.test(name)) {
    return true
  }
  return null
}

export default {
  invoke,
  on,
  off,
  EVENTS,
  IS_MOCK,
  BridgeError,
  ERR,
  methodNames,
  methodStats,
  METHOD_GROUP,
  GROUP_LABEL,
}

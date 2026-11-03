/* 桌面界面与本机后端的接口和数据适配。 */
(() => {
  const nativeRuntime = !!window.runtime;
  const seriesCache = new Map();
  const suggestionCovers = new Map();
  let suggestionGeneration = 0, suggestionQueue = [], suggestionBusy = 0;
  function cancelSuggestionCovers() {
    suggestionGeneration++;
    for (const job of suggestionQueue) job.resolve();
    suggestionQueue = [];
  }
  function suggestionCover(id) {
    const known=seriesCache.get(id);
    if (known?.cover) return Promise.resolve({cover:known.cover,abstract:known.subInfo || ''});
    const cached = suggestionCovers.get(id);
    if (cached && Date.now() < cached.expires) return cached.promise;
    const entry = {expires:Date.now()+10*60*1000};
    entry.promise = raw('SeriesDetail',[id]).then(detail => {
      const cover=detail?.cover || '';
      if (!cover) throw new Error('剧集暂无封面');
      return {cover,abstract:detail.subInfo || ''};
    });
    suggestionCovers.set(id,entry);
    while (suggestionCovers.size>128) suggestionCovers.delete(suggestionCovers.keys().next().value);
    entry.promise.catch(()=>{if(suggestionCovers.get(id)===entry)suggestionCovers.delete(id);});
    return entry.promise;
  }
  function pumpSuggestionCovers() {
    while (suggestionBusy<3 && suggestionQueue.length) {
      const job=suggestionQueue.shift();
      if (job.generation!==suggestionGeneration) {job.resolve();continue;}
      suggestionBusy++;
      (async()=>{
        try {
          const detail=await suggestionCover(job.item.seriesId);
          if (job.generation!==suggestionGeneration) return;
          const base=await imageProxyBase();
          if (job.generation!==suggestionGeneration) return;
          // job.item is a Vue proxy from the displayed array. Mutating it
          // through that proxy makes the newly obtained poster render.
          job.item.cover=base?localImages(detail.cover,base,'cover'):detail.cover;
          if (!job.item.abstract && detail.abstract) job.item.abstract=detail.abstract;
        } catch { /* Keep the keyword usable if this drama has no metadata. */ }
        finally {suggestionBusy--;job.resolve();pumpSuggestionCovers();}
      })();
    }
  }
  function hydrateSuggestions(items) {
    cancelSuggestionCovers();
    const generation=suggestionGeneration, pending=[];
    for (const item of items || []) {
      if (!item.seriesId || item.cover) continue;
      let resolve;
      pending.push(new Promise(done=>resolve=done));
      suggestionQueue.push({item,generation,resolve});
    }
    pumpSuggestionCovers();
    return Promise.all(pending);
  }
  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
  // Only the visible player's loader uses recovery. Speculative neighbours keep
  // bounded backend calls so they cannot occupy the model fetch gate forever.
  async function recoverPlayback(load, isCurrent, notify = () => {}) {
    while (isCurrent()) {
      const result = await load();
      if (!isCurrent()) break;
      if (result.ok) { notify(''); return result; }
      const match = String(result.error || '').match(/^\[HG_PLAYBACK_RECOVERY:(\d+)\]/);
      if (!match) return result;
      const retryAt = Math.max(Date.now() + 1000, Number(match[1]));
      while (isCurrent() && Date.now() < retryAt) {
        const remaining = retryAt - Date.now();
        notify(`正在自动恢复播放，${Math.max(1, Math.ceil(remaining / 1000))} 秒后重试`);
        await sleep(Math.min(1000, remaining));
      }
    }
    return { ok: false, error: '播放已切换' };
  }
  const listeners = new Map();
  if (!window.runtime) {
    const size = () => ({ w: innerWidth, h: innerHeight });
    const methods = {
      EventsOnMultiple(name, fn) { const set = listeners.get(name) || new Set(); set.add(fn); listeners.set(name, set); return () => set.delete(fn); },
      EventsOff(...names) { names.forEach(n => listeners.delete(n)); },
      EventsEmit(name, ...data) { listeners.get(name)?.forEach(fn => fn(...data)); },
      WindowGetSize: size, WindowGetPosition: () => ({ x: 0, y: 0 }),
      WindowIsFullscreen: () => !!document.fullscreenElement,
      WindowIsMaximised: () => false, WindowIsMinimised: () => false,
      WindowFullscreen: () => document.documentElement.requestFullscreen?.().catch(() => {}),
      WindowUnfullscreen: () => document.exitFullscreen?.().catch(() => {}),
      BrowserOpenURL: url => { if (/^https?:\/\//.test(url)) window.open(url, '_blank', 'noopener,noreferrer'); },
      ClipboardGetText: async () => '', ClipboardSetText: text => navigator.clipboard?.writeText(text),
      Quit: () => window.close(), Environment: () => ({ platform: 'windows', arch: 'amd64', buildType: 'web' }),
    };
    window.runtime = new Proxy(methods, { get: (obj, key) => obj[key] || (() => Promise.resolve()) });
  }
  async function raw(name, args = []) {
    const deadline = Date.now() + 8000;
    while (true) {
      const app = window.go?.app?.App || window.go?.main?.App;
      if (app?.[name]) return app[name](...args);
      if (!nativeRuntime) {
        const res = await fetch(`/api/invoke/${encodeURIComponent(name)}`, {
          method: 'POST', headers: { 'Content-Type': 'application/json', 'X-HG-Local': '1' }, body: JSON.stringify(args),
        });
        const payload = await res.json();
        if (!res.ok || payload.error) throw new Error(payload.error || `调用失败 ${res.status}`);
        return payload.data;
      }
      if (Date.now() > deadline) throw new Error('本地后端未就绪，请重启应用');
      await sleep(30);
    }
  }
  function series(s = {}) {
    const result = { ...s, id: s.id || s.seriesId || '', intro: s.intro || s.description || s.subtitle || '',
      tags: s.tags || (s.category ? [s.category] : []), episodes: s.episodes || [], creators: s.creators || [],
      followCount: s.followCount || 0, favorited: !!s.favorited, liked: !!s.liked,
      episodeCount: s.episodeCount || 0, startEpisode: s.startEpisode || 0 };
    if (result.id) seriesCache.set(result.id, result);
    return result;
  }
  const page = (items, source = {}) => ({ ...source, items: items || [], cursor: source.cursor || '', hasMore: !!source.hasMore });
  const asList = v => Array.isArray(v) ? v : v?.items || [];
  const comment = c => ({ ...c, userName: c.userName || c.nickname || '', liked: c.liked ?? c.isLiked ?? false, createTime: c.createTime || c.createdAt || 0, images: c.images || [] });
  // Keep only a small window of recent browse pages. A pending request is also
  // cached so an arriving scroll consumes the prefetch instead of duplicating it.
  const browsePages = new Map();
  const warmingCovers = new Set();
  const browseTTL = 60000;
  let browseContext = '', browseGeneration = 0, prefetchTimer = 0, prefetchBusy = false, queuedPrefetch;
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(k => [k,canonical(value[k])])) : value;
  function browseArgs(name,args) {
    if (name === 'Search') return [String(args[0] || '').trim(),args[1] || ''];
    let filter = args[0] || {};
    if (typeof filter === 'string') { try { filter = JSON.parse(filter); } catch { return [filter,args[1] || '']; } }
    return [JSON.stringify(canonical(filter)),args[1] || ''];
  }
  const browseKey = (name,args) => JSON.stringify([name,...args]);
  const contextKey = (name,args) => JSON.stringify([name,args[0]]);
  const browseIsActive = () => !window.location || window.location.hash.startsWith('#/search');
  window.addEventListener?.('hashchange',() => {
    if (browseIsActive()) return;
    cancelSuggestionCovers();
    browseGeneration++; queuedPrefetch = undefined;
    coverQueue.clear(); for (const cancel of [...warmingCovers]) cancel();
    if (prefetchTimer) clearTimeout(prefetchTimer);
    prefetchTimer = 0;
  });
  async function cachedBrowse(name,args) {
    const key = browseKey(name,args), old = browsePages.get(key);
    if (old && Date.now() < old.expires) {
      browsePages.delete(key); browsePages.set(key,old);
      return old.promise;
    }
    const entry = { expires: Date.now() + browseTTL };
    entry.promise = raw(name,args).then(v => name === 'Search' ? page((v?.series || v?.items || []).map(series),v) : page(asList(v).map(it => series(it.series || it)),v));
    browsePages.set(key,entry);
    while (browsePages.size > 20) browsePages.delete(browsePages.keys().next().value);
    try { return await entry.promise; }
    catch (error) { if (browsePages.get(key) === entry) browsePages.delete(key); throw error; }
  }
  function foregroundBrowse(name,args) {
    const normalized = browseArgs(name,args), key = contextKey(name,normalized);
    if (key !== browseContext) { browseContext = key; browseGeneration++; queuedPrefetch = undefined; coverQueue.clear(); for (const cancel of [...warmingCovers]) cancel(); }
    return cachedBrowse(name,normalized);
  }
  function clearBrowseCache() {
    browsePages.clear(); browseGeneration++; queuedPrefetch = undefined;
    coverQueue.clear(); for (const cancel of [...warmingCovers]) cancel();
    if (prefetchTimer) clearTimeout(prefetchTimer);
    prefetchTimer = 0;
  }
  const coverQueue = new Map();
  const warmedCovers = new Map();
  const warmingURLs = new Set();
  let warmingCount = 0;
  function pumpCovers() {
    if (typeof Image !== 'function') return;
    while (warmingCount < 4 && coverQueue.size) {
      const [url,generation] = coverQueue.entries().next().value;
      coverQueue.delete(url);
      if (generation !== browseGeneration || !browseIsActive() || warmingURLs.has(url)) continue;
      warmingURLs.add(url);
      warmingCount++;
      const img = new Image(); img.decoding = 'async'; img.fetchPriority = 'low';
      let finished = false;
      const finish = () => {
        if (finished) return; finished = true;
        warmingCovers.delete(cancel); warmingURLs.delete(url); img.onload = img.onerror = null;
        warmingCount--; pumpCovers();
      };
      const cancel = () => { finish(); img.src = ''; };
      warmingCovers.add(cancel);
      img.onload = () => { warmedCovers.set(url,Date.now()); while (warmedCovers.size>216) warmedCovers.delete(warmedCovers.keys().next().value); finish(); };
      img.onerror = finish;
      img.src = url;
    }
  }
  async function warmNextCovers(value,generation) {
    if (typeof Image !== 'function' || generation !== browseGeneration || !browseIsActive()) return;
    const base = await imageProxyBase();
    if (!base || generation !== browseGeneration || !browseIsActive()) return;
    // Images have a separate bounded queue. One slow cover must never hold
    // the metadata pipeline or prevent the following page from being fetched.
    for (const s of (value?.items || [])) {
      if (!/^https?:\/\//i.test(s.cover || '')) continue;
      const url=localImages(s.cover,base,'cover');
      if (warmingURLs.has(url) || Date.now()-(warmedCovers.get(url)||0)<browseTTL) continue;
      coverQueue.set(url,generation);
    }
    while (coverQueue.size>108) coverQueue.delete(coverQueue.keys().next().value);
    pumpCovers();
  }
  async function runBrowsePrefetch() {
    prefetchTimer = 0;
    if (prefetchBusy || !queuedPrefetch || !browseIsActive()) return;
    const job = queuedPrefetch; queuedPrefetch = undefined; prefetchBusy = true;
    try {
      if (job.generation !== browseGeneration) return;
      const value = await cachedBrowse(job.name,job.args);
      void warmNextCovers(value,job.generation).catch(()=>{});
      if (job.generation===browseGeneration && job.remaining>1 && !queuedPrefetch && value?.hasMore && value.cursor && value.cursor!==job.args[1]) {
        queuedPrefetch={...job,args:[job.args[0],value.cursor],remaining:job.remaining-1};
      }
    } catch { /* Retry failed speculative pages only when requested. */ }
    finally { prefetchBusy = false; if (queuedPrefetch) prefetchTimer = setTimeout(runBrowsePrefetch,0); }
  }
  function queueBrowsePrefetch(name,args,value) {
    const normalized = browseArgs(name,args);
    if (!browseIsActive() || contextKey(name,normalized) !== browseContext || !value?.hasMore || !value.cursor || value.cursor === normalized[1]) return;
    queuedPrefetch = {name,args:[normalized[0],value.cursor],generation:browseGeneration,remaining:name==='Category'?2:1};
    if (!prefetchTimer && !prefetchBusy) prefetchTimer = setTimeout(runBrowsePrefetch,0);
  }
  async function adaptedInvoke(name, args) {
    switch (name) {
      case 'AppInfo': return { ...await raw(name), updateEnabled: false, commit: '' };
      case 'About': return { ...await raw(name), author: '', email: '', qq: '', qr: '', qrAlipay: '', note: '' };
      case 'Status': { const v = await raw(name); const device = v.device || { ready: false, registering: false, error: '设备尚未注册' }; return { ready: true, error: device.error || '', device, account: { ...(v.user || {}), loggedIn: !!v.loggedIn } }; }
      case 'FeedCategories': return (await raw(name) || []).map(t => ({ name: t.name, tabType: Number(t.id ?? t.tabType) || 0 }));
      case 'RecommendTab': { const v = await raw(name, [Number(args[0]) || 0, args[1] || '']); return page(asList(v).map(it => ({ series: series(it.series || it), episode: it.videoId ? { videoId: it.videoId, index: it.episode || 1, cover: it.cover } : null, reasons: it.reasons || [], rank: it.rank || 0, recommendSource:it.recommendSource || '' })), v); }
      case 'Search': return foregroundBrowse(name,args);
      case 'SearchSuggest': {
        const keyword=String(args[0] || '').trim();
        if (!keyword) return [];
        return (await raw(name,[keyword]) || []).slice(0,10).map(item => {
          const value=typeof item==='string'?{word:item}:item;
          const word=value.word || '';
          return {...value,word,parts:Array.isArray(value.parts)&&value.parts.length?value.parts:[{text:word,hl:false}]};
        }).filter(item=>item.word);
      }
      case 'SelectorPanel': return raw(name, args);
      case 'Category': return foregroundBrowse(name,args);
      case 'RankPanel': return raw(name);
      case 'Rank': { const filter = typeof args[0] === 'object' ? args[0] : { rank: String(args[0] || '') }; const v = await raw(name, [JSON.stringify(filter), args[1] || '']); return page(asList(v).map(it => series(it.series || it)), v); }
      case 'NewSeries': { const v = await raw(name, [JSON.stringify({gender: String(args[0] ?? '2'), cursor: args[1] || ''})]); return page(asList(v).map(it => series(it.series || it)), v); }
      case 'SeriesDetail': {
        try { return series(await raw(name, args)); }
        catch (error) { throw new Error(String(error?.message || error).replace(/^\[HG_PLAYBACK_RECOVERY:\d+\]\s*/, '')); }
      }
      case 'SeriesSeasons': return (await raw(name, args) || []).map(series);
      case 'Related': { const v = await raw(name,args); return {works:(v?.works || []).map(series),guess:(v?.guess || []).map(series)}; }
      case 'NextSeason': throw new Error('资料尚未恢复多季映射');
      case 'PlayInfo': {
        const [id, video] = args; let detail = seriesCache.get(id);
        if (!detail?.episodes?.length) detail = series(await raw('SeriesDetail', [id]));
        const ep = detail.episodes.find(e => e.videoId === video);
        if (!ep) throw new Error('未找到该视频对应的分集');
        const v = await raw(name, [id, ep.index]);
        const qualities = (v.sources || []).map(s => ({ ...s, name: s.quality }));
        return { ...v, qualities, default: qualities[0]?.name || '', resumeSec: v.resumeSec || 0 };
      }
      case 'PrefetchPlay': {
        const ids = Array.isArray(args[0]) ? args[0].filter(v => typeof v === 'string' && v) : [];
        return ids.length > 1 ? raw('PrefetchPlay', [ids]).catch(() => null) : null;
      }
      case 'PrefetchPlayItems': {
        const ids = Array.isArray(args[0]) ? args[0].map(v => v?.videoId).filter(v => typeof v === 'string' && v) : [];
        return ids.length > 1 ? raw('PrefetchPlay', [ids]).catch(() => null) : null;
      }
      case 'NativeDecPreload': return null;
      case 'SaveProgress': {
        const [s, ep, video, seconds, duration] = args;
        const index = typeof ep === 'number' ? ep + 1 : ep?.index || 1;
        return raw(name, [s.id || s, video || ep?.videoId || '', index, Math.round((seconds || 0) * 1000), Math.round((typeof duration === 'number' ? duration : ep?.duration || 0) * 1000)]);
      }
      case 'History': return page((await raw(name, args) || []).map(h => ({ series: series(seriesCache.get(h.seriesId) || { id: h.seriesId, title: h.title || h.seriesId, cover: h.cover }), episodeIndex: h.episode, videoId: h.videoId || '', positionSec: h.positionMs / 1000, updatedAt: h.updatedAt })));
      case 'DeleteHistory': { const refs = Array.isArray(args[0]) ? args[0] : [args[0]]; for (const ref of refs) await raw(name, [ref?.seriesId || ref || '']); return null; }
      case 'Favorites': return page((await raw(name, [args[0] || '', false]) || []).map(series));
      case 'Likes': throw new Error('账号点赞列表尚未恢复');
      case 'Reservations': return page((await raw(name, [args[0] || '', 0]) || []).map(series));
      case 'FollowUpdates': { const v = await raw(name, args); return page(asList(v).map(it => series(it.series || it)), v); }
      case 'LocalPending': return { favorites: 0, likes: 0 };
      case 'SetFavorite': { const s = args[0]; return raw(name, [typeof s === 'object' ? s.id : s, !!args[1]]); }
      case 'RemoveFavorites': { for (const s of asList(args[0])) await raw('SetFavorite', [s.id || s, false]); return null; }
      case 'SetLike': return raw(name, [args[1] || (typeof args[0] === 'object' ? args[0].id : args[0]), !!args[2]]);
      case 'Reviews': { const v = await raw(name, [args[0], '', args[1] || '']); return page(asList(v).map(comment), v); }
      case 'Comments': case 'Replies': case 'ReviewReplies': {
        const v = await raw(name, args); return page(asList(v).map(comment), v);
      }
      case 'SendComment': return comment(await raw(name,[args[0],args[1],args[2],args[3] || '',JSON.stringify(args[4] || [])]));
      case 'SendReply': return comment(await raw(name,[...args.slice(0,6),!!args[6],Number(args[7]) || 0,JSON.stringify(args[8] || [])]));
      case 'SendReview': case 'SendReviewReply': return comment(await raw(name,args));
      case 'SendDanmaku': { const v=await raw(name,args); return {...v,timeMs:v.offsetMs,liked:!!v.isLiked}; }
      case 'Danmaku': { const v = await raw(name, args); return { items: asList(v).map(c => ({ ...c, timeMs: c.offsetMs, liked: c.isLiked })), nextMs: Number(v?.nextMs) || Number(args[2] || 0) + 30000 }; }
      case 'Downloads': return (await raw(name) || []).map(d => ({ ...d, seriesTitle: d.title, episodeIndex: d.episode, bytes: d.doneBytes, total: d.totalBytes }));
      case 'AddDownloads': {
        const [s, eps, quality] = args; const id = s.id || s;
        if (!Array.isArray(eps)) return raw(name, args);
        let count = 0; for (const ep of eps) count += await raw(name, [id, ep.index || ep, ep.index || ep]); return count;
      }
      case 'SetPlaybackPrefs': return raw(name, [JSON.stringify({ ...args[0], defaultQuality: args[0]?.quality || '720p' })]);
      case 'CollectEmoticon': return raw(name, [args[0]?.id || args[0]]);
      case 'UploadPicture': return raw(name, [args[1] || args[0]]);
      case 'UploadLocal': throw new Error('请在评论输入框选择图片');
      case 'ReportEnv': {
        const [first, second] = args;
        return raw(name, typeof first === 'boolean'
          ? [String(second ?? ''), JSON.stringify({ enabled: first })]
          : [String(first ?? ''), String(second ?? '')]);
      }
      case 'LikeDanmaku': return raw(name, [args[2] || args[0], !!args[3]]);
      case 'DeleteReservations': return raw(name, [Array.isArray(args[0]) ? args[0] : [args[0]]]);
      case 'Calendar': return raw(name, [args[0] || '', args[1] || '']);
      case 'CheckUpdate': throw new Error('当前版本暂未配置更新发布服务');
      case 'UpdateState': { const state = await raw(name); return { enabled: false, phase: state?.stage || 'idle', error: state?.error || '', current: state?.current || '' }; }
      case 'DownloadUpdate': case 'ApplyUpdate': throw new Error('当前版本暂未配置更新发布服务');
      case 'QRLoginStart': { const v = await raw(name, ['cn']); return { token: v.token, image: v.qrUrl, expireAt: v.expire }; }
      case 'QRLoginPoll': { const v = await raw(name); return { state: ['waiting', 'scanned', 'confirmed', 'expired'][v.status] || 'waiting', account: v.user ? { ...v.user, loggedIn: v.status === 2 } : null }; }
      case 'Login': { const v = await raw(name, args); return { ...v, loggedIn: !!v?.userId }; }
      case 'LoginRegions': return [{ code: '86', name: '中国大陆', iso: 'CN', min: 11, max: 11 }];
      case 'NativeDecStatus': return { available: false, ready: false, error: '原生解码未恢复，标准视频与内嵌 WASM 由界面处理' };
      case 'NativeDecPrepare': throw new Error('本版本未恢复原生共享内存解码');
      case 'Integrity': { const v = await raw(name); return v.ok ? '' : (v.warnings || []).join('；'); }
      default: return raw(name, args);
    }
  }
  const imageFields = new Set(['cover','coverUrl','cover_url','avatar','avatarUrl','avatarURL','userAvatar','user_avatar','poster','thumbnail','thumb_url','image','imageUrl','image_url','large','webUri','webUrl','expandUrl','dynamicUrl','icon']);
  const remoteImage = (value, field) => typeof value === 'string' && /^https?:\/\//i.test(value) && (imageFields.has(field) || field === 'images');
  function hasRemoteImages(value, field = '') {
    if (remoteImage(value,field)) return true;
    if (Array.isArray(value)) return value.some(item => hasRemoteImages(item,field));
    return !!value && typeof value === 'object' && Object.entries(value).some(([key,item]) => hasRemoteImages(item,key));
  }
  let imageProxy;
  async function imageProxyBase() {
    if (!imageProxy) imageProxy = raw('DiagnoseLocalPlay').then(v => v?.proxyReady && /^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(v.proxyAddr || '') ? v.proxyAddr : '').catch(() => '');
    return imageProxy;
  }
  function localImages(value, base, field = '') {
    if (remoteImage(value,field)) return value.startsWith(base + '/dlimg/') ? value : base + '/dlimg/' + encodeURIComponent(value) + '?v=2';
    if (Array.isArray(value)) return value.map(item => localImages(item,base,field));
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key,item]) => [key,localImages(item,base,key === 'url' && value.webUri ? 'webUri' : key)]));
    return value;
  }
  async function invoke(name, args) {
    const value = await adaptedInvoke(name,args);
    if (name === 'Category' || name === 'Search') queueBrowsePrefetch(name,args,value);
    if (!hasRemoteImages(value)) return value;
    const base = await imageProxyBase();
    return base ? localImages(value,base) : value;
  }
  window.hgCompat = new Proxy({}, { get: (_, name) => (...args) => invoke(name, args) });
  window.__HG_COMPAT__ = { invoke, series, page, clearBrowseCache, hydrateSuggestions, cancelSuggestionCovers, recoverPlayback };
  if (window.location) {
    let previous = '';
    let polling = false;
    setInterval(async () => {
      if (!window.location.hash.startsWith('#/downloads') || polling) return;
      polling = true;
      try {
        const tasks = await invoke('Downloads', []);
        const state = JSON.stringify(tasks);
        if (state !== previous) {
          previous = state;
          window.runtime.EventsEmit?.('download:list');
          for (const task of tasks) window.runtime.EventsEmit?.('download:task', task);
        }
      } catch { /* 页面自身展示调用错误，下一次轮询可恢复。 */ }
      finally { polling = false; }
    }, 1500);
  }
})();

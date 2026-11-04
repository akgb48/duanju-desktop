import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import fs from 'node:fs';

const code = fs.readFileSync(new URL('../frontend/compat/bridge.js', import.meta.url), 'utf8');
function create(impl, extra = {}) {
  const calls = [];
  const native = new Proxy({}, { get: (_, name) => async (...args) => { calls.push([name, args]); return impl(name, args); } });
  const context = { window: { runtime: {}, go: { app: { App: native } } }, console, setTimeout, clearTimeout, ...extra };
  vm.runInNewContext(code, context);
  return { bridge: context.window.hgCompat, compat: context.window.__HG_COMPAT__, calls };
}

test('环境信息兼容布尔开关与字符串参数，传给 Go 的参数保持字符串', async () => {
  const { bridge, calls } = create(() => ({}));
  await bridge.ReportEnv(true, 'play');
  await bridge.ReportEnv(false, 'recommend');
  await bridge.ReportEnv('settings', '');
  assert.deepEqual(calls.map(([name, args]) => [name, Array.from(args)]), [
    ['ReportEnv', ['play', '{"enabled":true}']],
    ['ReportEnv', ['recommend', '{"enabled":false}']],
    ['ReportEnv', ['settings', '']],
  ]);
});

test('输入联想保留剧集跳转、封面和高亮，空输入不请求', async () => {
  const {bridge,calls}=create(name=>name==='DiagnoseLocalPlay'?{proxyReady:true,proxyAddr:'http://127.0.0.1:45678'}:[{word:'总裁回家',seriesId:'9007199254740993123',cover:'https://cover.example/a.jpg',abstract:'都市',parts:[{text:'总裁',hl:true},{text:'回家',hl:false}]},'总裁故事']);
  assert.equal(JSON.stringify(await bridge.SearchSuggest('  ')),'[]');
  assert.equal(calls.length,0);
  const suggestions=await bridge.SearchSuggest(' 总裁 ');
  assert.equal(suggestions[0].seriesId,'9007199254740993123');
  assert.equal(suggestions[0].parts[0].hl,true);
  assert.equal(suggestions[0].cover,'http://127.0.0.1:45678/dlimg/'+encodeURIComponent('https://cover.example/a.jpg')+'?v=2');
  assert.equal(suggestions[1].parts[0].text,'总裁故事');
  assert.equal(JSON.stringify(calls[0]),JSON.stringify(['SearchSuggest',['总裁']]));
});

test('联想缺图时补取同剧真实封面，缓存并合并重复请求',async()=>{
  const {compat,calls}=create(name=>name==='DiagnoseLocalPlay'?{proxyReady:true,proxyAddr:'http://127.0.0.1:45678'}:{cover:'https://poster.example/real.jpg',subInfo:'第2季 · 爱情'});
  const items=[{word:'糯糯下山',seriesId:'s1'},{word:'同剧另一个词',seriesId:'s1'},{word:'纯关键词'},{word:'已有封面',seriesId:'s2',cover:'existing'}];
  await compat.hydrateSuggestions(items);
  assert.equal(calls.filter(c=>c[0]==='SeriesDetail').length,1);
  assert.equal(items[0].cover,'http://127.0.0.1:45678/dlimg/'+encodeURIComponent('https://poster.example/real.jpg')+'?v=2');
  assert.equal(items[1].cover,items[0].cover);
  assert.equal(items[2].cover,undefined);
  assert.equal(items[3].cover,'existing');
  await compat.hydrateSuggestions([{seriesId:'s1'}]);
  assert.equal(calls.filter(c=>c[0]==='SeriesDetail').length,1);
});

test('快速改词时取消旧联想排队任务，旧响应不能更新新菜单',async()=>{
  const pending=new Map();
  const {compat,calls}=create((name,args)=>name==='DiagnoseLocalPlay'?{proxyReady:true,proxyAddr:'http://127.0.0.1:45678'}:new Promise(resolve=>pending.set(args[0],resolve)));
  const old=Array.from({length:7},(_,i)=>({seriesId:'old'+i}));
  const oldWork=compat.hydrateSuggestions(old);
  assert.equal(calls.filter(c=>c[0]==='SeriesDetail').length,3);
  const next=[{seriesId:'latest'}],newWork=compat.hydrateSuggestions(next);
  for (const resolve of [...pending.values()]) resolve({cover:'https://poster.example/old.jpg'});
  for(let i=0;i<12&&!pending.has('latest');i++)await Promise.resolve();
  assert.ok(pending.has('latest'));
  pending.get('latest')({cover:'https://poster.example/latest.jpg'});
  await Promise.all([oldWork,newWork]);
  assert.equal(calls.filter(c=>c[0]==='SeriesDetail').length,4);
  assert.equal(old[0].cover,undefined);
  assert.ok(next[0].cover.includes('latest.jpg'));
});

test('播放预取传递真实视频批次，单个前台视频交由分集窗口加载', async () => {
  const { bridge, calls } = create(() => null);
  await bridge.PrefetchPlay(['v1']);
  assert.equal(calls.length, 0);
  await bridge.PrefetchPlay(['v1', 'v2']);
  await bridge.PrefetchPlayItems([{ seriesId: 's1', videoId: 'v3' }, { seriesId: 's2', videoId: 'v4' }]);
  assert.equal(JSON.stringify(calls), JSON.stringify([
    ['PrefetchPlay', [['v1', 'v2']]], ['PrefetchPlay', [['v3', 'v4']]],
  ]));
});
test('界面字符串页签映射为整数，视频元数据保持嵌套', async () => {
  const { bridge, calls } = create(() => ({ items: [{ seriesId: '9007199254740993123', series: { id: '9007199254740993123', title: '测试剧' }, videoId: 'v1', episode: 2 }] }));
  const value = await bridge.RecommendTab('45', '');
  assert.equal(calls[0][1][0], 45);
  assert.equal(value.items[0].series.id, '9007199254740993123');
  assert.equal(value.items[0].episode.videoId, 'v1');
  assert.equal(value.items[0].series.startEpisode, 0);
});

test('推荐剧名标签保留官方文字、渐变配色与来源说明',async()=>{
  const {bridge}=create(name=>name==='DiagnoseLocalPlay'?{proxyReady:true,proxyAddr:'http://127.0.0.1:45678'}:{items:[{series:{id:'s1',title:'新剧',titleTag:{text:'新剧',colors:['#00AE83'],textColor:'#fff',icon:'https://label.example/new.png'}},videoId:'v1',recommendSource:'new',reasons:[{text:'新剧上新',highlight:true}]}]});
  const page=await bridge.RecommendTab('16','');
  assert.equal(page.items[0].series.titleTag.text,'新剧');
  assert.equal(page.items[0].series.titleTag.colors[0],'#00AE83');
  assert.equal(page.items[0].series.titleTag.icon,'http://127.0.0.1:45678/dlimg/'+encodeURIComponent('https://label.example/new.png')+'?v=2');
  assert.equal(page.items[0].reasons[0].text,'新剧上新');
  assert.equal(page.items[0].recommendSource,'new');
});
test('启动状态与完整性检查使用界面期望的结构和字符串', async () => {
  const { bridge } = create(name => name === 'Integrity' ? { ok: true } : { loggedIn: true, user: { userId: 'u1' } });
  const status = await bridge.Status();
  assert.equal(status.ready, true);
  assert.equal(status.device.ready, false);
  assert.equal(status.account.loggedIn, true);
  assert.equal(status.account.userId, 'u1');
  assert.equal(await bridge.Integrity(), '');
});
test('设备注册失败和重试状态原样传给界面', async () => {
  const { bridge } = create(() => ({ device: { ready: false, registering: true, error: '服务端没有分配设备 ID' } }));
  const status = await bridge.Status();
  assert.equal(status.device.ready, false);
  assert.equal(status.device.registering, true);
  assert.equal(status.error, '服务端没有分配设备 ID');
});
test('迁移成功后返回实际数据目录，正式界面能读取目录大小', async () => {
  let dir = 'before';
  const { bridge, calls } = create((name, args) => {
    if (name === 'MoveDataDir') {
      dir = args[0];
      return null;
    }
    return { dir, sizeBytes: 32, isDefault: false };
  });
  assert.equal(await bridge.MoveDataDir('after'), 'after');
  const info = await bridge.GetDataDirInfo();
  assert.equal(info.dir, 'after');
  assert.equal(info.totalBytes, 32);
  assert.deepEqual(calls.map(([name]) => name), ['MoveDataDir', 'GetDataDirInfo', 'GetDataDirInfo']);
});
test('预约接口保留上线筛选、分页及预约记录 ID', async () => {
  const item = { itemId: '9007199254740993123', seriesId: '9007199254740993124', title: '预约剧' };
  const page = { items: [item], cursor: '40', hasMore: true, onlineTotal: 2, offlineTotal: 21 };
  const { bridge, calls } = create(name => name === 'Reservations' ? page : null);
  assert.deepEqual(await bridge.Reservations(false, '20'), page);
  await bridge.DeleteReservations([item.itemId], false);
  assert.deepEqual(calls, [['Reservations', [false, '20']], ['DeleteReservations', [[item.itemId], false]]]);
});
test('扫码图片保持纯 base64，成功账号带登录标志且未确认时不会登录', async () => {
  const {bridge}=create(name=>name==='QRLoginStart'?{token:'test',qrUrl:'iVBOR-test',expire:2000000000}:name==='QRLoginPoll'?{status:2,user:{userId:'test-user'}}:{userId:'sms-user'});
  assert.equal((await bridge.QRLoginStart({})).image,'iVBOR-test');
  assert.equal((await bridge.QRLoginPoll()).account.loggedIn,true);
  assert.equal((await bridge.Login('test','test')).loggedIn,true);
  const waiting=create(()=>({status:1}));
  assert.equal((await waiting.bridge.QRLoginPoll()).account,null);
});
test('界面零起点分集和秒进度转换为后端集号及毫秒', async () => {
  const { bridge, calls } = create(() => null);
  await bridge.SaveProgress({ id: 's1' }, 3, 'v4', 12, 60);
  assert.equal(JSON.stringify(calls[0]), JSON.stringify(['SaveProgress', ['s1', 'v4', 4, 12000, 60000]]));
});
test('收藏筛选字符串不会被误当作清空全部的开关', async () => {
  const { bridge, calls } = create(() => []);
  await bridge.Favorites('', 'all');
  assert.equal(calls[0][1][1], false);
});
test('播放按 videoId 匹配分集，返回清晰度列表', async () => {
  const { bridge, calls } = create(name => name === 'SeriesDetail' ? { id: 's1', episodes: [{ index: 2, videoId: 'v2' }] } : { sources: [{ quality: '1080p', url: 'https://media.example/a.mp4' }], duration: 60 });
  const value = await bridge.PlayInfo('s1', 'v2');
  assert.equal(calls[1][1][1], 2);
  assert.equal(value.qualities[0].name, '1080p');
});

test('可见视频自动恢复没有重试次数上限，成功后清除等待提示', async () => {
  let now=0,attempts=0;const messages=[];
  const {compat}=create(()=>null,{Date:{now:()=>now},setTimeout(fn,ms){now+=ms;queueMicrotask(fn);return 1;}});
  const result=await compat.recoverPlayback(async()=>++attempts<=45?{ok:false,error:`[HG_PLAYBACK_RECOVERY:${now+2000}] empty`}:{ok:true,data:{videoId:'v1'}},()=>true,message=>messages.push(message));
  assert.equal(attempts,46);
  assert.equal(result.data.videoId,'v1');
  assert.ok(messages.some(message=>message.includes('自动恢复')));
  assert.equal(messages.at(-1),'');
});

test('切剧或离开页面会停止自动恢复，旧请求不能更新新播放器', async () => {
  const timers=manualTimers();let current=true,attempts=0,now=0;const messages=[];
  const {compat}=create(()=>null,{...timers,Date:{now:()=>now}});
  const pending=compat.recoverPlayback(async()=>{attempts++;return {ok:false,error:'[HG_PLAYBACK_RECOVERY:2000] empty'}},()=>current,message=>messages.push(message));
  await Promise.resolve();current=false;now=1000;
  await timers.next();
  assert.equal((await pending).error,'播放已切换');
  assert.equal(attempts,1);
  let finish;current=true;
  const inflight=compat.recoverPlayback(()=>new Promise(resolve=>finish=resolve),()=>current,message=>messages.push(message));
  const count=messages.length;current=false;finish({ok:true,data:{videoId:'old'}});
  assert.equal((await inflight).ok,false);
  assert.equal(messages.length,count);
});

test('权限与数据错误不进入自动恢复，后台预取不使用无限重试', async () => {
  const timers=manualTimers();const {compat,bridge,calls}=create(()=>{throw new Error('[HG_PLAYBACK_RECOVERY:2000] empty')},timers);
  let attempts=0;
  const result=await compat.recoverPlayback(async()=>{attempts++;return {ok:false,error:'HTTP 403 denied'}},()=>true);
  assert.equal(result.error,'HTTP 403 denied');assert.equal(attempts,1);assert.equal(timers.size,0);
  await bridge.PrefetchPlay(['a','b']);
  assert.equal(calls.length,1);assert.equal(timers.size,0);
  const built=fs.readFileSync(new URL('../frontend/dist/assets/index.a2b61b89.js',import.meta.url),'utf8');
  assert.ok(built.includes('recoverPlayback(()=>Lp(i.series.id,i.videoId),()=>A===_l&&!ut&&!yn'));
  assert.ok(built.includes('hg-playback-recovery'));
});

test('播放前的详情暂时失败也会恢复，普通详情页不显示内部重试标记',async()=>{
  let now=0,metadataCalls=0;
  const {bridge,compat}=create(name=>{
    if(name==='SeriesDetail'){
      if(++metadataCalls===1)throw new Error('[HG_PLAYBACK_RECOVERY:2000] api[video_detail]: 服务端未返回内容');
      return {id:'s1',episodes:[{videoId:'v1',index:1}]};
    }
    return {sources:[{quality:'720p',url:'https://cdn.example/video.mp4'}]};
  },{Date:{now:()=>now},setTimeout(fn,ms){now+=ms;queueMicrotask(fn);return 1;}});
  const result=await compat.recoverPlayback(async()=>{try{return {ok:true,data:await bridge.PlayInfo('s1','v1')}}catch(error){return {ok:false,error:error.message}}},()=>true);
  assert.equal(result.ok,true);assert.equal(metadataCalls,2);
  const failing=create(()=>{throw new Error('[HG_PLAYBACK_RECOVERY:2000] network unavailable')});
  await assert.rejects(failing.bridge.SeriesDetail('s2'),error=>error.message==='network unavailable');
});

test('排行榜条件和新剧男女频完整传递，分页游标保持原样', async () => {
  const { bridge, calls } = create(() => ({ items: [] }));
  await bridge.Rank({rank:'comic_series_rank',rank_sub:'comic_series_hot_play',rank_panel:'cate_1'}, 'rank-cursor');
  assert.deepEqual(JSON.parse(calls[0][1][0]), {rank:'comic_series_rank',rank_sub:'comic_series_hot_play',rank_panel:'cate_1'});
  assert.equal(calls[0][1][1], 'rank-cursor');
  await bridge.NewSeries('0', 'new-cursor');
  assert.deepEqual(JSON.parse(calls[1][1][0]), {gender:'0',cursor:'new-cursor'});
});

test('封面和头像统一使用本机图片代理，保留签名并且不改写视频地址', async () => {
  const cover='https://cdn.example/cover.heic?x-signature=a%2Bb%2Fc%3D';
  const {bridge,calls}=create(name => name==='DiagnoseLocalPlay' ? {proxyReady:true,proxyAddr:'http://127.0.0.1:45678'} : name==='Search' ? {series:[{id:'s1',cover,episodes:[{videoId:'v1',index:1,cover}]}]} : name==='Calendar' ? {items:[{seriesId:'s1',cover}]} : {sources:[{quality:'1080p',url:'https://cdn.example/video.mp4'}]});
  const result=await bridge.Search('test','');
  const expected='http://127.0.0.1:45678/dlimg/'+encodeURIComponent(cover)+'?v=2';
  assert.equal(result.items[0].cover,expected);
  assert.equal(result.items[0].episodes[0].cover,expected);
  assert.equal((await bridge.Calendar('','')).items[0].cover,expected);
  const play=await bridge.PlayInfo('s1','v1');
  assert.equal(play.qualities[0].url,'https://cdn.example/video.mp4');
  assert.equal(calls.filter(([name])=>name==='DiagnoseLocalPlay').length,1);
});
test('相关推荐保留相关作品和猜你喜欢两组，弹幕使用服务端时间窗口', async () => {
  const {bridge}=create(name=>name==='Related'?{works:[{id:'1',title:'相关作品'}],guess:[{id:'2',title:'猜你喜欢'}]}:{items:[{id:'d1',offsetMs:1234,text:'弹幕'}],nextMs:30000});
  const related=await bridge.Related('s1');assert.equal(related.works[0].id,'1');assert.equal(related.guess[0].id,'2');
  const danmaku=await bridge.Danmaku('s1','v1',0,90000);assert.equal(danmaku.nextMs,30000);assert.equal(danmaku.items[0].timeMs,1234);
});
test('评论配图走图片代理，发评论序列化图片，剧评和回复保持界面参数', async () => {
  const {bridge,calls}=create(name=>name==='DiagnoseLocalPlay'?{proxyReady:true,proxyAddr:'http://127.0.0.1:45678'}:name==='Comments'?{items:[{id:'c1',nickname:'读者',images:[{url:'https://cdn.example/img.heic',webUri:'https://cdn.example/img.heic'}]}]}:{id:'posted',nickname:'本人',offsetMs:3210});
  const page=await bridge.Comments('s1','v1','');assert.match(page.items[0].images[0].url,/^http:\/\/127\.0\.0\.1:45678\/dlimg\//);
  await bridge.SendComment('s1','v1','内容','',[{webUri:'https://cdn.example/img.heic'}]);
  const call=calls.find(([n])=>n==='SendComment');assert.equal(typeof call[1][4],'string');assert.equal(JSON.parse(call[1][4]).length,1);
  await bridge.SendReply('s1','v1','c1','','','回复',true,1234,[]);const reply=calls.find(([n])=>n==='SendReply');assert.equal(reply[1][6],true);assert.equal(reply[1][7],1234);
});
test('推荐页分类栏不随播放器控件自动隐藏', () => {
  const css = fs.readFileSync(new URL('../frontend/assets/assets/index.28b004cd.css', import.meta.url), 'utf8');
  assert.doesNotMatch(css, /\.feed:has\(\.xgplayer-inactive\)\s+\.cat-bar[^{}]*\{[^{}]*(?:opacity:\s*0\b|pointer-events:\s*none)/);
});

test('推荐页分类入口独立于上游分类列表，保留三个内容分类', async () => {
  const { bridge, calls } = create(() => { throw new Error('分类接口不可用'); });
  const tabs = await bridge.FeedCategories();
  assert.deepEqual(Array.from(tabs, tab => [tab.tabType, tab.name]), [
    [36, '漫剧'], [39, '真人剧'], [16, '推荐'],
  ]);
  assert.equal(calls.length, 0);
});

test('推荐页续播跳过锁定分集，并在本剧最后一集停下', () => {
  const { compat } = create(() => null);
  const episodes = [
    { videoId: 'first' },
    { videoId: 'locked', locked: true },
    { videoId: 'last' },
  ];
  assert.equal(compat.nextRecommendationEpisode(episodes, 'first')?.videoId, 'last');
  assert.equal(compat.nextRecommendationEpisode(episodes, 'last'), undefined);
  assert.equal(compat.nextRecommendationEpisode(episodes, 'unknown'), undefined);
});

test('界面资源的文件大小及摘要与清单一致', () => {
  const crypto = process.getBuiltinModule('node:crypto');
  const root = new URL('../frontend/assets/', import.meta.url);
  const manifest = JSON.parse(fs.readFileSync(new URL('manifest.json', root), 'utf8'));
  for (const entry of manifest.files) {
    const data = fs.readFileSync(new URL(entry.name, root));
    assert.equal(data.length, entry.size);
    assert.equal(crypto.createHash('sha256').update(data).digest('hex'), entry.sha256);
  }
});

function manualTimers() {
  const tasks = new Map(); let id = 0;
  return { setTimeout(fn) { const key = ++id; tasks.set(key,fn); return key; }, clearTimeout(key) { tasks.delete(key); },
    async next() { const entry = tasks.entries().next().value; if (!entry) return false; tasks.delete(entry[0]); await entry[1](); return true; },
    get size() { return tasks.size; } };
}
test('找剧缓存合并相同条件的并发请求，返回页面无需重复请求', async () => {
  const timers = manualTimers(); let finish;
  const {bridge,calls,compat}=create(()=>new Promise(resolve=>finish=resolve),timers);
  const one=bridge.Category({genre:['comic_series'],year:['2026']},'');
  const two=bridge.Category({year:['2026'],genre:['comic_series']},'');
  finish({items:[{id:'s1'}],hasMore:false});
  await Promise.all([one,two]);
  await bridge.Category({genre:['comic_series'],year:['2026']},'');
  assert.equal(calls.length,1);
  compat.clearBrowseCache();
  const refreshed=bridge.Category({genre:['comic_series'],year:['2026']},'');
  finish({items:[{id:'s2'}]});
  assert.equal((await refreshed).items[0].id,'s2');
  assert.equal(calls.length,2);
});
test('滚动复用正在预取的请求，没有更多时立即停止预取', async () => {
  const timers=manualTimers(); let finishNext;
  const {bridge,calls}=create((name,args)=>args[1]===''?{items:[{id:'first'}],cursor:'18',hasMore:true}:new Promise(resolve=>finishNext=resolve),timers);
  await bridge.Category({},'');
  const prefetch=timers.next();
  const foreground=bridge.Category({},'18');
  finishNext({items:[{id:'next'}],cursor:'36',hasMore:false});
  await prefetch;
  assert.equal((await foreground).items[0].id,'next');
  assert.equal(calls.length,2);
  assert.equal(timers.size,0);
});
test('找剧准备两批结果，图片不返回也不会阻塞后续列表请求', async () => {
  const timers=manualTimers();const images=[];
  class SlowImage{constructor(){images.push(this)} set src(v){this.value=v}}
  const {bridge,calls}=create((name,args)=>name==='DiagnoseLocalPlay'?{proxyReady:true,proxyAddr:'http://127.0.0.1:45678'}:{items:[{id:args[1]||'first',cover:'https://cdn.example/'+(args[1]||'first')+'.jpg'}],hasMore:true,cursor:String((Number(args[1])||0)+54)},{...timers,Image:SlowImage});
  await bridge.Category({},'');
  await timers.next();await timers.next();
  assert.equal(calls.filter(([name])=>name==='Category').length,3);
  assert.equal(timers.size,0);
  assert.ok(images.length>0);
  assert.ok(images.length<=4);
});
test('筛选改变后取消旧条件的待执行预取，缓存失败不影响重试', async () => {
  const timers=manualTimers(); let fail=true;
  const {bridge,calls}=create((name,args)=>{
    if(args[0].includes('new')&&fail){fail=false;throw new Error('temporary');}
    return {items:[{id:args[0]}],cursor:'18',hasMore:!args[0].includes('new')};
  },timers);
  await bridge.Category({genre:['old']},'');
  await assert.rejects(bridge.Category({genre:['new']},''),/temporary/);
  await timers.next();
  assert.equal(calls.length,2);
  await bridge.Category({genre:['new']},'');
  assert.equal(calls.length,3);
});
test('翻页缓存到期后重新读取，关键词和筛选缓存互不混用', async () => {
  const timers=manualTimers();let now=0;
  const {bridge,calls}=create(()=>({items:[],series:[],hasMore:false}),{...timers,Date:{now:()=>now}});
  await bridge.Category({},''); await bridge.Category({},'');
  now=61000; await bridge.Category({},'');
  await bridge.Search('总裁',''); await bridge.Search('总裁','');
  assert.equal(calls.length,3);
  assert.equal(calls[2][0],'Search');
});

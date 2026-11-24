# 更新日志

本文件记录短剧电脑版的重要变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.1/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

发布流程见 README「播放与自动更新」一节：更新版本号并推送 `v主.次.修订` 标签即可触发发布。

部分版本在 GitHub Release 中只包含对比链接、没有条目，此处按提交记录与 README 补记，并标注「无变更说明」。

## [1.1.1] - 2026-11-10

### 修复

- 播放请求与设备池对齐参考客户端实现（`FIX: ALIGN PLAYBACK REQUESTS AND DEVICE POOL WITH REFERENCE CLIENT`）。

### 变更

- 发布记录无独立说明，仅含对比链接。

## [1.1.0] - 2026-11-09

### 新增

- 接入 FFmpeg / WebView2 六槽共享帧链路。系统直接播放不可用时，按硬件解码、CPU、WASM 逐级回退。
- 首次需要原生解码时下载约 77 MiB 固定版本 LGPL 组件并校验 SHA256，组件位于 `%LOCALAPPDATA%\duanju-desktop-codecs`，无需安装系统 HEVC 扩展。
- 程序内更新：启动后与每 6 小时检查 Releases，显示下载进度、校验 SHA256，退出后替换 EXE、保留启动参数并重启；替换失败时恢复旧程序。

### 变更

- 媒体代理改为按实际读取的视频数据逐块缓存：内存上限 32 MiB，磁盘上限 256 MiB、保留 1 小时，重启后校验并复用。
- 音视频与相邻集预热复用相同签名地址的 Range，切换视频时取消旧取流与后台预热。
- 原生解码保留一个下一集预热进程，使用管道背压暂停解码，超时或不匹配时回收。

### 修复

- 重构播放与更新的职责划分（`REFACTOR: SEPARATE PLAYBACK AND UPDATE RESPONSIBILITIES`）。

## [1.0.3] - 2026-11-06

### 变更

- 无变更说明，发布记录仅含对比链接。

## [1.0.2] - 2026-11-04

### 变更

- Windows 发布资产改用稳定的文件名（`USE A STABLE WINDOWS RELEASE ASSET FILENAME`）。
- 数据目录迁移改为按文件系统标识比较（`COMPARE MIGRATED DATA DIRECTORIES BY FILESYSTEM IDENTITY`）。

### 说明

- 发布记录无独立说明，仅含对比链接。

## [1.0.1] - 2026-11-04

### 修复

- 修复短信验证码提交与登录状态恢复。
- 同步云端预约，修复「待上线」预约加载失败。

### 变更

- 改进数据目录迁移、断点续传与本地图片代理。

## [1.0.0] - 2026-11-03

### 新增

- Windows x64 首个正式版。双击 `DuanjuDesktop-1.0.0.exe` 运行，使用系统已有 WebView2 组件，运行不需要 Go / Node.js / Python。
- 推荐信息流刷剧、搜索联想、排行榜与新剧浏览。
- 剧集详情、选集播放、评论与弹幕。
- 本地收藏、观看历史、播放进度与下载管理。

### 说明

- 本版本及 1.0.3 之前的版本不含更新器，需手动下载 1.1.0 运行一次后才能使用程序内更新。

## 版本兼容提示

- 1.0.3 及更早版本没有更新器，必须手动下载并运行 1.1.0 一次。
- 已有安装会继续复用 `%LOCALAPPDATA%\duanju-desktop` 数据目录，升级不会清空账号、收藏、历史与下载任务。

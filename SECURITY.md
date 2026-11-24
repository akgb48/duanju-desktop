# 安全策略

## 支持范围

仅最新发布版本接受安全修复。旧版本请先升级到 [Releases](https://github.com/akgb48/duanju-desktop/releases/latest) 中的最新版再验证问题是否仍然存在。

## 报告方式

**请勿为安全问题开公开 Issue**，也不要贴出可直接复现的攻击代码、密钥或账号信息。

请通过 GitHub 的私有漏洞报告提交：

1. 打开仓库的 **Security** 标签页
2. 选择 **Report a vulnerability**（GitHub Security Advisory）
3. 填写影响版本、复现步骤、预期与实际行为

如果无法使用该入口，可先开一个只说明「存在安全问题、希望私下沟通」的 Issue，不包含任何技术细节。

## 重点关注范围

以下方面的问题优先处理：

- **更新链路**：`update.json` 与安装包的签名校验（`pkg/updproto`）、版本比较、降级保护、更新器替换 EXE 失败的恢复路径
- **本地服务**：`--web` 模式与媒体代理的监听范围、端口暴露、目录穿越、请求伪造
- **数据目录**：`%LOCALAPPDATA%\duanju-desktop` 中的账号令牌、Cookie 与设备标识的落盘权限
- **媒体代理**：`Range` 请求处理、缓存文件路径拼接、内容类型嗅探
- **本地文件写入**：下载目录、缓存目录的可控路径与符号链接处理

## 明确不在范围内的内容

- 上游服务本身的安全问题（请直接联系对应平台）
- 需要物理接触或管理员权限的本地攻击
- 依赖库的公开漏洞，如已在官方披露，请通过 Dependabot 或 Issue 提示升级

## 处理预期

- 首次响应：收到报告后 7 天内
- 修复时间：视影响范围与复杂度，修复后会在 Release 说明中致谢（除非报告者要求匿名）

## 发布资产校验

每个 Release 附 `SHA256SUMS.txt` 与 `update.json`，下载后可校验：

```powershell
Get-FileHash .\DuanjuDesktop-<版本>-windows-amd64.exe -Algorithm SHA256
```

程序内更新会自动完成同一校验，校验不通过不会替换现有 EXE。

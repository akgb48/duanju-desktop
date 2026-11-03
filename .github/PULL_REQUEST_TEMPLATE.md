## 变更说明

<!-- 一句话说清改了什么、为什么 -->

## 变更类型

- [ ] feat 新功能
- [ ] fix 缺陷修复
- [ ] refactor 重构
- [ ] perf 性能
- [ ] docs 文档
- [ ] chore 依赖/CI

## 影响范围

- [ ] pkg/api（注册表 / Session / 信封）
- [ ] pkg/crypto（签名）
- [ ] pkg/hgapi（字段映射）
- [ ] pkg/service（编排）
- [ ] pkg/store（数据库）
- [ ] pkg/media（本地代理）
- [ ] pkg/updproto（更新协议）
- [ ] internal/app（前端绑定）
- [ ] frontend（UI）
- [ ] CI / 构建脚本

## 检查清单

- [ ] `gofmt -l .` 为空
- [ ] `go vet ./...` 通过
- [ ] `go test ./...` 通过
- [ ] `go test -race ./pkg/...` 通过
- [ ] 前端 `npm run build` 通过
- [ ] 新增接口已同步 `pkg/api/endpoints.go` + `RegistrySize`
- [ ] 接口或绑定变更通过注册表与绑定契约测试
- [ ] 新增绑定已同步 `frontend/src/api/bridge.js`
- [ ] 导出符号有中文文档注释
- [ ] 无 `_ =` 吞错误（清理调用除外）
- [ ] 无调试残留（`fmt.Println`、`debugger`）

## 验证方式

<!-- 步骤或截图 -->

## 关联 Issue

Closes #

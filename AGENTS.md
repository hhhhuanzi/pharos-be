# Codex / Agent Instructions — dh-nightingale 二开隔离

> 本地 Cursor 另有始终生效规则：`.cursor/rules/dh-second-dev.mdc`（该目录被 `.gitignore` 忽略，克隆后需自行保留/同步）。

## 目标

本仓库是 Nightingale 后端 fork。应随时可 `fetch upstream` + `merge upstream/main` 追平官方，**冲突面尽量小**。

## Remotes

| remote     | 含义                                      | 操作                         |
| ---------- | ----------------------------------------- | ---------------------------- |
| `origin`   | 自己的 fork                               | push / PR 到此               |
| `upstream` | 官方 `https://github.com/ccfos/nightingale.git` | 只 fetch / merge，**不 push** |

## 分支

- `main`：尽量贴近官方 `upstream/main`，避免堆叠二开提交。
- 功能开发用约定分支；**当前产品分支为 `feature_link`**（不要随意改名）。
- 二开功能合入前，先在 `main`（或功能分支）merge 官方，再解决冲突。

## 隔离原则

1. **能加文件不改文件**：二开逻辑优先落在新增文件/目录。
2. **不得不改官方文件时只留薄入口**：注册一行路由、挂载一层 processor；业务实现仍放自有文件。
3. 少在已有大 router 文件中间插入大段逻辑；少改 `alert/eval` 等核心热路径文件。

## BE 落点（优先）

- 新建 `center/router/router_dh_*.go`：自有路由与 handler 入口
- 新建 processor / `pkg/dh/`：业务与公共封装
- DB：跟官方 `models/migrate` 风格增量迁移，**不手改绕过 migrate**
- 工作流同前端：`main` 追 upstream；功能在 `feature_link`

## Tracing 扩展（链路）

- 与前端对齐：tracing 类数据源只需在 `center/cconf/plugin.go` 注册 Plugins 元数据，查询走现有 `/proxy/:id/*`
- **不必**为只读链路 MVP 新增 `RegisterDatasource` / 告警 eval
- 若日后需要服务端 GraphQL 编排，再新增 `center/router/router_dh_*.go`

## Commit 与版本对齐

- 前缀建议：`feat(dh):` / `fix(dh):` / `chore(dh):`
- 前后端尽量成对追同一官方 tag / release，避免接口漂移

## 自检（改代码前）

- 这次改动能放进 `router_dh_*.go` / `pkg/dh/` 吗？
- 若必须改官方文件，是否只加了「薄入口」？
- migrate 是否走官方目录与流程？
- merge `upstream/main` 时，冲突是否集中在那几行入口？

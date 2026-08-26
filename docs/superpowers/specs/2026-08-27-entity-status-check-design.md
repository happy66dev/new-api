# 上游模型 / 虚拟模型状态检测设计规格

## 1. Context

new-api 已有**状态检测页面**（`web/src/features/status-check/`）：按**分组**聚合展示 24h 可用性、平均延迟、缓存命中率喵。

本次为三类**用户自建实体**补上同样的状态检测能力，且**只做被动统计**（真实调用产生的数据），**不做任何主动探测请求**（不做手动探测、不做定时探测、不发探测请求）喵：

1. **用户上游模型**（`user/<name>`，独立计费体系）：状态展示放在**嗅探（Check）按钮左边**喵。
2. **虚拟模型调用链的每一个节点**（internal 候选 / custom 候选）：状态展示放在**候选链的失败规则入口（ShieldCheck）旁边**喵。
3. **虚拟模型本身**：状态展示放在**概述（Overview）基本信息下方**喵。

另修复一个已确认 bug：**使用日志选择"自定上游"类型筛选无效**——筛选后仍返回全部日志喵。

## 2. 现状调研结论

### 2.1 状态检测系统（参考对象）

- **存储**：`pkg/perf_metrics` 按 `(model, group, bucket)` 聚合样本（`Sample{LatencyMs, Success, TtftMs, CacheHit...}`），内存热桶 + 落库 `perf_metrics` 表，支持 24h 序列查询喵。
- **前端**：状态检测页卡片展示 `availability`、`avg_latency_ms`、`avg_ttft_ms`、`availability_24h[]`；`AvailabilityBars`、`StatusHistoryDrawer` 组件可复用喵。

### 2.2 上游模型页面（`web/src/features/upstream-models/index.tsx`）

- 每行操作按钮组：**Check（嗅探）→ Sync to balance → Edit → Delete** 喵。
- 状态展示放在 **Check 左边**喵。

### 2.3 虚拟模型页面（`web/src/features/virtual-models/`）

- 右栏 Tabs：**Overview / Candidate Chain / Failure Rules / API Key Authorization / Runtime Status** 喵。
- **Overview**（index.tsx:118-132）：显示名 + `virtual/<name>` + 候选数 + Edit/Delete；状态卡片放其**下方**喵。
- **候选链编辑器**（virtual-model-candidates-editor.tsx）：每个候选行折叠态操作组为 上移/下移/**ShieldCheck（候选失败规则入口，:437）**/删除；状态展示放 **ShieldCheck 旁边**喵。
- 候选类型：`internal`（分组 + 真实模型，走原生 relay 计费）与 `custom`（base_url + api_key + 真实模型，或引用用户上游模型）喵。

### 2.4 日志筛选 bug 根因（已定位）

- **后端过滤正确**：`model.GetUserLogs` 对 `type=8` 走 `type = 8` 精确过滤，实测 `?type=8` 只返回自定上游日志喵。
- **前端路由 schema 缺陷**：`routes/_authenticated/usage-logs/$section.tsx:28` 的 `logTypeValues = ['0','1','2','3','4','5','6','7']` **漏了 `'8'`**；`z.enum(logTypeValues)` 校验失败后 `.catch([])`，使 `type` 变成空数组 → 请求不带 `type` → 后端返回全部日志喵。
- **修复**：`logTypeValues` 追加 `'8'` 即可（前端 `LOG_TYPE_FILTERS` 已含 `Custom Upstream`，后端 `LogTypeCustomUpstream=8` 已定义）喵。

## 3. 已确认需求（用户指定）

| 项 | 结论 |
| --- | --- |
| 数据来源 | **仅被动统计**（真实调用记录），**不做手动/定时探测**，不发任何探测请求 |
| 上游模型状态展示位置 | 列表行**嗅探（Check）按钮左边** |
| 虚拟模型节点状态展示位置 | 候选链**失败规则（ShieldCheck）入口旁边** |
| 虚拟模型本身状态展示位置 | **概述（Overview）基本信息下方** |
| 展示参考 | 现有**状态检测页面**（可用性/延迟/24h 历史） |
| 日志筛选 bug | 修 `logTypeValues` 漏 `'8'` |

## 4. 语义定义

### 4.1 检测指标（与状态检测页对齐）

对每个被检测实体，展示：

- **可用性**（success rate %）：`成功数 / 计入请求数 × 100`，无数据时显示 `—` 喵。
- **平均延迟**（ms）：统计窗口内的平均端到端延迟喵。
- **最近 24h 可用性**：按小时桶的柱状图（复用 `AvailabilityBars`）喵。
- **请求数**：统计窗口内计入的请求总数喵。
- **最近一次调用**：时间 + 成功/失败 + 失败时的受控错误信息喵。

### 4.2 被动统计的数据来源（唯一的记录方式）

在现有请求生命周期中，于**结算点**把真实调用结果写入状态存储，不新增任何网络请求、不改变计费行为喵。

| 实体 | 记录时机 | 成功/失败判定 |
| --- | --- | --- |
| 上游模型 `user/<name>` | `handleUserUpstreamModelRequest` 执行完成（含共享调用分支） | 透传 2xx 且响应写出=成功；其余=失败 |
| 虚拟模型 internal 候选 | 候选激活后原生 relay 结果（成功 / 全部渠道重试耗尽） | relay 返回 2xx=成功；最终失败=失败 |
| 虚拟模型 custom 候选 | `executeCustomVirtualModelCandidate` 执行完成 | 透传 2xx=成功；失败/重试耗尽/降级=失败 |
| 虚拟模型本身 | 请求最终结果（候选链任一候选成功即整体成功） | 链上有任一候选 2xx=成功；链耗尽=失败 |

**记录实现**：新增 `perfmetrics.RecordEntityProbe(modelName string, latencyMs int64, success bool)` 包装 `Record`，内部强制 `Group = "__entity_probe__"`，避免调用方误传真实分组、与状态检测页分组统计隔离喵。

### 4.3 失败语义（哪些失败计入可用性）

- **计入失败**（反映上游/渠道连通性问题）：
  - 上游 4xx/5xx、超时、连接失败、DNS 失败喵。
  - 凭据解密失败（master key 缺失/不匹配）喵。
  - internal 候选渠道全部不可用、渠道返回 4xx/5xx、请求被渠道拒绝喵。
- **不计入（配置态，不算连通性故障，不写失败 Sample）**：
  - 模型被停用（Enabled=false）喵。
  - 余额不足 / 超过使用上限（`upstream_model_quota_exhausted`）喵。
  - 虚拟模型功能关闭、模型不存在（资源级错误，不计入任何实体的成功率，避免配置错误污染可用性画像）喵。
- 不计入的请求**仍更新 `EntityProbeState.last_at`**（记录"最近有过请求"），但不计成功/失败数喵。

### 4.4 权限与可见性

- **属主视角（完整）**：上游模型、虚拟模型及其节点的状态结果对属主完整可见（自用统计 + 共享调用维度 + 失败受控错误信息），与 CRUD 的 owner 隔离一致喵。
- **共享使用者视角（聚合）**：共享上游模型的状态对**共享使用者**（其他用户）**可见聚合状态**——共享调用维度的成功率、平均延迟、请求数与"最近一次是否成功"，**不含**属主身份、错误明细、调用者身份喵。
- 查询接口必须校验资源归属/共享授权（属主校验用 `loadOwnedUpstreamModel`，共享聚合校验用 `GetEnabledSharedUserUpstreamModelByName`），防止越权枚举喵。
- 虚拟模型的节点/整体状态仅属主可见（虚拟模型无跨用户共享语义）喵。

### 4.5 共享调用统计（详细设计）

**背景**：共享调用（`group=user-shared`，调用者≠属主）走 `handleUserUpstreamModelRequest` 的 `isShared=true` 分支，免费、只累计 `ShareSpent`，日志 `type=8`、`group=user-shared` 喵。

**语义**：
- **属主自用统计**：只统计属主自己发起的调用（`isShared=false`），反映属主视角下该上游配置的健康状况喵。
- **共享调用统计**：单独维度记录共享调用的总量/成功率/延迟，与自用画像分离，避免他人调用或滥用污染属主看到的"我的模型可用性"喵。
- 属主的状态卡片默认展示**自用统计**，卡片内提供**"共享调用"切换**查看共享维度的聚合（请求数、成功率、平均延迟；**不展示调用者身份**）喵。
- **共享使用者视角**：模型广场/游乐场看到该共享模型时，展示**共享调用维度的聚合状态**（成功率、平均延迟、请求数、最近一次成功与否）——与属主看到的"共享调用切换页"数据同源（`group=__entity_probe_shared__`），但不含错误明细喵。

**实现**：共享调用在结算点以 `Group = "__entity_probe_shared__"` 记录，`model` 仍为 `user/<name>`；自用记录 `Group = "__entity_probe__"`。查询按 group 分别聚合，互不混用喵。

### 4.6 成本、安全与开关

- 纯被动记录**不额外消耗任何上游配额**、不产生新请求喵。
- 记录失败时只存受控错误信息（错误码/通用文案），**绝不落密钥、密文、完整 URL 或请求正文**；共享使用者视角**不展示任何错误明细**喵。
- **跟随性能指标开关**：`RecordEntityProbe` 直接复用 `perfmetrics.Record`（内部受 `perf_metrics_setting` 的 `Enabled` 控制）；管理员关闭性能指标后实体状态检测同样无数据（不新增独立开关）喵。

## 5. 数据模型与存储

### 5.1 复用 perf_metrics（24h 聚合）

- 不新增时序表，复用 `pkg/perf_metrics` 的 `(model, group, bucket)` 聚合喵：
  - **上游模型自用**：`model = "user/<name>"`、`group = "__entity_probe__"` 喵。
  - **上游模型共享调用**：`model = "user/<name>"`、`group = "__entity_probe_shared__"` 喵。
  - **虚拟模型整体**：`model = "virtual/<name>"`、`group = "__entity_probe__"` 喵。
  - **虚拟模型节点**：`model = "virtual/<name>/candidate/<id>"`、`group = "__entity_probe__"` 喵。
- 查询：`perfmetrics.Query({Model: 实体model, Group: 对应组, Hours: 24})` 复用 24h 序列能力喵。

### 5.2 新增元数据表 `entity_probe_states`（最近一次调用）

```go
type EntityProbeState struct {
    ID            int64  // 主键
    Scope         string // upstream | virtual | virtual_candidate | virtual_shared
    EntityID      int64  // 上游模型 id 或虚拟模型 id（candidate 时=候选 id）
    VirtualID     int64  // 仅 virtual_candidate 使用：所属虚拟模型 id
    OwnerUserID   int    // 属主，硬隔离
    LastAt        int64  // 最近一次调用时间（Unix）
    LastSuccess   bool   // 最近一次是否成功
    LastLatencyMs int64  // 最近一次延迟
    LastError     string // 最近一次失败受控信息（成功时为空）
    RequestCount  int64  // 计入的请求总数
    SuccessCount  int64  // 计入的成功数
}
```

- 每个 `(scope, entity_id)` 一行（`virtual_shared` 对应上游模型的共享维度，`virtual_candidate` 额外含 `virtual_id`）喵。
- 用于展示"最近一次调用 + 累计计数"，并作为**前端初始渲染的低延迟来源**（perf_metrics 查询走库/缓存即可）喵。
- 被动记录时同步更新：成功/失败均写 `last_at`；计入的请求更新 `request_count`/`success_count`；不计入的配置态请求只更新 `last_at` 不动计数喵。
- 并发：单实体单行更新用 GORM `Clauses(clause.OnConflict)` 条件更新（参考 `RecordStatusCheckProbeResult`）喵。

### 5.3 迁移

- `model/main.go` AutoMigrate 增加 `EntityProbeState`；三库兼容（无方言特性）喵。

## 6. 后端 API 设计

```
GET    /api/upstream-models/:id/status          // 属主视角：自用统计 + 最近一次；?include_shared=true 附共享维度
GET    /api/upstream-models/shared/:name/status // 共享使用者视角：共享调用维度聚合（按 user/<name>，模型须共享中）
GET    /api/virtual-models/:id/status           // 整体统计 + 各节点摘要
GET    /api/virtual-models/:id/candidates/:cid/status   // 节点统计 + 最近一次
```

**无任何 `POST status-check` 探测接口**（不做主动探测）喵。

响应统一结构（属主视角）：

```json
{
  "success": true,
  "data": {
    "availability": 92.31,
    "avg_latency_ms": 812,
    "request_count": 13,
    "availability_24h": [100, 0, 100, null, ...],
    "last_at": 1787774400,
    "last_success": true,
    "last_latency_ms": 745,
    "last_error": ""
  }
}
```

- 上游模型 `status?include_shared=true` 额外返回 `shared: { availability, avg_latency_ms, request_count, last_at, last_success }`（供属主切换查看）喵。
- 共享聚合接口 `GET /api/upstream-models/shared/:name/status`（共享使用者视角）只返回 `{ availability, avg_latency_ms, request_count, last_at, last_success }`，**不含 `last_error`** 与任何属主/调用者信息；模型不在共享中（`share_enabled=false` 或额度耗尽）按 404 处理喵。
- 虚拟模型 `status` 额外返回 `candidates: [{ candidate_id, label, availability, avg_latency_ms, last_success }]`，供 Overview 节点摘要展示喵。

## 7. 前端设计（状态展示交互，详细）

> 由于无主动探测，**所有状态展示都是只读**的，来自 `GET status` 聚合喵。

### 7.1 上游模型行（嗅探按钮左边）

- 位置：操作组最左（Check 左边）插入**只读状态指示器**喵。
- **折叠展示**：彩色状态点 + 可选可用性文本。
  - 绿点 = `request_count > 0` 且 `last_success = true`（最近一次成功）喵。
  - 红点 = `request_count > 0` 且 `last_success = false`（最近一次失败）喵。
  - 灰点 = `request_count = 0`（从未调用/未计入）喵。
  - 可用性文本：`xx%`（有请求时），置灰 `—`（无请求）喵。
- **悬停 Tooltip**：显示摘要行——可用性、平均延迟、请求数、最近一次时间（`formatLatency`/相对时间）、失败时的受控错误信息喵。
- **点击 Popover（详情）**：
  - 24h 可用性柱状图（复用 `AvailabilityBars`）喵。
  - 指标网格：可用性 / 平均延迟 / 请求数 / 最近一次结果喵。
  - 若 `include_shared` 且共享维度有数据，提供"自用 / 共享调用"切换，切换后柱状图与指标随维度更新喵。
  - 空态：显示"暂无调用数据"喵。
- 列表加载时并行拉取各模型 `GET status`，状态点随轮询/刷新更新喵。

### 7.2 虚拟模型候选行（失败规则入口旁边）

- 位置：候选行操作组 **ShieldCheck 旁边**插入只读状态点（灰/绿/红，语义同上）喵。
- **悬停 Tooltip**：该节点的可用性、平均延迟、最近一次结果喵。
- **点击 Popover**：节点 24h 柱状图 + 指标 + 错误信息（避免与候选行展开编辑冲突，Popover 点击不触发行展开）喵。
- 候选行未保存（`candidate.id === undefined`）时**不显示状态点**（无实体可统计）喵。
- 数据：候选链加载后按 `GET /candidates/:cid/status` 拉取，或由虚拟模型整体 `status.candidates[]` 一次带回，按 candidate_id 匹配渲染喵（推荐后者，减少请求数）喵。

### 7.3 虚拟模型 Overview（基本信息下方）

- 在 Overview 基本信息卡片**下方**增加"状态检测"卡片喵：
  - **整体指标**：可用性 %、平均延迟、请求数、24h 柱状图（复用 `AvailabilityBars`）喵。
  - **最近一次调用**：时间 + 成功/失败 + 失败错误信息喵。
  - **刷新按钮**：`GET status` 重新拉取（非探测）喵。
  - **节点摘要列表**：每个候选一行（序号 + 模型名 + 可用性/延迟 + 状态点），点击该行跳转到"候选链" tab 喵。
  - 空态：无整体请求时显示"暂无调用数据"；有整体数据但个别节点无数据时该节点显示 `—` 喵。

### 7.4 共享组件

- 提取 `EntityStatusIndicator`（状态点 + Tooltip 摘要）与 `EntityStatusDetail`（Popover：24h 柱状图 + 指标网格 + 最近一次），供三处复用喵。
- 复用现有 `AvailabilityBars`、`formatLatency`、`getSuccessRateTextClass` 喵。
- 指标颜色/文案统一走现有 `getSuccessRateTextClass` 与 i18n keys 喵。

### 7.5 模型广场共享卡片（共享使用者视角）

- 模型广场中**共享模型卡片**（`owner_by === 'user-shared'`）在现有"共享剩余额度"基础上，增加**共享调用聚合状态点**（复用 `EntityStatusIndicator`）喵：
  - 数据源：`GET /api/upstream-models/shared/<name>/status`（登录用户即可查共享中的模型）喵。
  - 展示：绿/红/灰状态点 + `xx%` 可用性 + 平均延迟（无错误明细）喵。
  - 悬停 Tooltip：可用性、平均延迟、请求数、最近一次时间与是否成功喵。
  - 无共享调用数据时显示灰点 + `—` 喵。
- 游乐场 `user-shared` 分组模型列表**可选**展示同款状态点（P2 内一并评估）喵。

## 8. 日志筛选修复

- `routes/_authenticated/usage-logs/$section.tsx:28` 的 `logTypeValues` 追加 `'8'`：
  ```ts
  const logTypeValues = ['0', '1', '2', '3', '4', '5', '6', '7', '8'] as const
  ```
- 回归：选择"自定上游" → URL `type=8` → 路由校验通过 → 请求带 `type=8` → 后端只返回 type=8 日志喵。

## 9. 分期实施计划（一期一交付）

### P1：日志筛选修复 + 上游模型状态展示（含共享维度与共享使用者视角）
- 修 `logTypeValues` 加 `'8'` 喵。
- 后端：`EntityProbeState` 表 + `RecordEntityProbe` + 上游模型 `handleUserUpstreamModelRequest` 结算点记录（自用 `__entity_probe__` / 共享 `__entity_probe_shared__`）+ `GET /api/upstream-models/:id/status`（含共享维度）+ `GET /api/upstream-models/shared/:name/status`（共享聚合）喵。
- 前端：上游模型行状态指示器（Check 左边）+ Tooltip + 详情 Popover + 共享切换；**模型广场共享卡片聚合状态点**（`user-shared` 条目）喵。
- 测试：记录时机（成功/失败/配置态不计入）、共享维度隔离、共享聚合无错误明细、owner/共享授权隔离、24h 聚合、最近一次、失败信息脱敏喵。

### P2：虚拟模型整体 + 候选节点状态展示
- 后端：internal/custom 候选结算点记录、虚拟模型整体聚合、`GET /api/virtual-models/:id/status` 与 `/candidates/:cid/status` 喵。
- 前端：Overview 状态卡片（整体 + 节点摘要 + 跳转）+ 候选行状态点（ShieldCheck 旁）；游乐场 `user-shared` 分组状态点（可选评估）喵。
- 测试：internal 候选成功/失败、custom 候选成功/失败、引用上游模型候选（同时计入上游模型与候选两个维度）、链耗尽整体失败、任一候选成功整体成功喵。

## 10. 验证

- 每期：后端相关包 `go test`、前端 `bunx vitest run`、`bun run typecheck`、`bun run build` 喵。
- 冒烟：创建用户上游模型 → 调用成功/制造上游故障 → 行内状态点/可用性/延迟更新且共享维度独立；设置虚拟模型（internal + custom + 引用上游模型候选）→ Overview 与候选行状态展示正确；日志选"自定上游"只返回 type=8 日志喵。
- 回归：普通计费、上游模型余额/上限、虚拟模型候选链失败规则、共享调用均不受影响喵。

## 11. 已确认决策（用户拍板记录）

1. **数据来源**：仅被动统计（真实调用记录），**不做手动/定时探测、不发任何探测请求**喵。
2. **共享模型状态可见性**：共享使用者在模型广场/游乐场可见**聚合状态**（成功率/平均延迟/请求数/最近是否成功），**无错误明细、无属主/调用者身份**；属主看完整（自用 + 共享维度 + 错误明细）喵。
3. **开关**：实体状态检测**跟随性能指标（perf_metrics）开关**，不新增独立开关；关闭性能指标则实体状态无数据喵。
4. 配置态请求（余额不足、模型停用、模型不存在）**不计入成功率**，但更新"最近一次时间"喵。
5. 共享调用**单独维度**（`__entity_probe_shared__`），属主默认看自用、可切换共享聚合喵。
6. "最近一次"由 `entity_probe_states` 表提供（perf_metrics 小时桶太粗）喵。
7. 上游模型行状态点**只读**（Check 保留嗅探功能，状态展示在其左边）喵。
8. 虚拟模型节点摘要由整体 `status.candidates[]` 一次带回（不逐个请求）喵。
9. **自主决策补充**（实现时遵循）：
   - internal 候选"分组无渠道/模型不可达"等配置不可用**计入失败**（提示候选配置问题，与上游模型资源级错误不计入区分）喵。
   - 删除上游模型/虚拟模型时联动清理 `entity_probe_states` 孤儿行；perf_metrics 时序数据自动过期不清理喵。
   - 虚拟模型整体延迟用**整次请求耗时**（含失败候选尝试，反映用户体验）；各候选记录各自耗时喵。

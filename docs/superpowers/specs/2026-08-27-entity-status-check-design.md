# 上游模型 / 虚拟模型状态检测设计规格

## 1. Context

new-api 已有**状态检测页面**（`web/src/features/status-check/`）：按**分组**聚合展示 24h 可用性、平均延迟、缓存命中率，并支持空闲期主动探测（`pkg/perf_metrics/status_check_flexible.go` + `model/status_check_probe_state.go`）喵。

本次为三类**用户自建实体**补上同样能力的状态检测喵：

1. **用户上游模型**（`user/<name>`，独立计费体系）：状态检测放在**嗅探（Check）按钮左边**喵。
2. **虚拟模型调用链的每一个节点**（internal 候选 / custom 候选）：状态检测放在**候选链的失败规则入口（ShieldCheck）旁边**喵。
3. **虚拟模型本身**：状态检测放在**概述（Overview）基本信息下方**喵。

另修复一个已确认 bug：**使用日志选择"自定上游"类型筛选无效**——筛选后仍返回全部日志喵。

## 2. 现状调研结论

### 2.1 状态检测系统（参考对象）

- **存储**：`pkg/perf_metrics` 按 `(model, group, bucket)` 聚合样本（`Sample{LatencyMs, Success, TtftMs, CacheHit...}`），内存热桶 + 落库 `perf_metrics` 表，支持 24h 序列查询喵。
- **主动探测**：`RecordStatusCheckProbe(group, latencyMs, success)` 用特殊 model 名 `__status_check_probe__` 记录探测结果；`StatusCheckProbeState` 表记录 idle/backoff 防浪费喵。
- **前端**：状态检测页卡片展示 `availability`、`avg_latency_ms`、`avg_ttft_ms`、`cache_hit_rate`、`availability_24h[]`、`history_24h[]`；`AvailabilityBars`、`StatusHistoryDrawer` 组件可复用喵。

### 2.2 上游模型页面（`web/src/features/upstream-models/index.tsx`）

- 每行操作按钮组：**Check（嗅探）→ Sync to balance → Edit → Delete** 喵。
- 状态检测放在 **Check 左边**，即行内操作组最左、或紧随模型名区域后喵。

### 2.3 虚拟模型页面（`web/src/features/virtual-models/`）

- 右栏 Tabs：**Overview / Candidate Chain / Failure Rules / API Key Authorization / Runtime Status** 喵。
- **Overview**（index.tsx:118-132）：显示名 + `virtual/<name>` + 候选数 + Edit/Delete，状态检测卡片放其**下方**喵。
- **候选链编辑器**（virtual-model-candidates-editor.tsx）：每个候选行折叠态的操作组为 上移/下移/**ShieldCheck（候选失败规则入口，:437）**/删除；状态检测按钮放 **ShieldCheck 旁边**喵。
- 候选类型：`internal`（分组 + 真实模型，走原生 relay）与 `custom`（base_url + api_key + 真实模型，或引用用户上游模型）喵。

### 2.4 日志筛选 bug 根因（已定位）

- **后端过滤正确**：`model.GetUserLogs` 对 `type=8` 走 `type = 8` 精确过滤，实测 `?type=8` 只返回自定上游日志喵。
- **前端路由 schema 缺陷**：`routes/_authenticated/usage-logs/$section.tsx:28` 的 `logTypeValues = ['0','1','2','3','4','5','6','7']` **漏了 `'8'`**；`z.enum(logTypeValues)` 校验失败后 `.catch([])`，使 `type` 变成空数组 → 请求不带 `type` → 后端返回全部日志喵。
- **修复**：`logTypeValues` 追加 `'8'` 即可（前端 `LOG_TYPE_FILTERS` 已含 `Custom Upstream` 选项，后端 `LogTypeCustomUpstream=8` 已定义）喵。

## 3. 已确认需求（用户指定）

| 项 | 结论 |
| --- | --- |
| 上游模型状态检测位置 | 列表行**嗅探（Check）按钮左边** |
| 虚拟模型节点状态检测位置 | 候选链**失败规则（ShieldCheck）入口旁边** |
| 虚拟模型本身状态检测位置 | **概述（Overview）基本信息下方** |
| 参考对象 | 现有**状态检测页面**（可用性/延迟/24h 历史） |
| 日志筛选 bug | 修 `logTypeValues` 漏 `'8'` |

## 4. 语义定义（推荐值，待主人确认）

### 4.1 检测指标（与状态检测页对齐）

对每个被检测实体，展示：

- **可用性**（success rate %）：`成功数 / 请求数 × 100`，无数据时显示 `—` 喵。
- **平均延迟**（ms）：最近统计窗口内的平均端到端延迟喵。
- **最近 24h 可用性**：按小时桶的柱状图（复用 `AvailabilityBars`）喵。
- **请求次数**：统计窗口内探测 + 被动累计的请求总数喵。
- **最近一次探测**：时间 + 成功/失败 + 失败时的受控错误信息喵。

### 4.2 数据来源（三路合并）

1. **被动统计（默认开启）**：真实调用发生时把结果写入 perf_metrics。
   - 上游模型真实调用：`handleUserUpstreamModelRequest` 成功/失败后，以 `model = "user/<name>"` 记录一条 `Sample`（成功态、延迟；失败时不记录延迟但记录失败计数）喵。
   - 虚拟模型节点真实调用：候选激活失败/成功时，以 `model = "virtual/<name>/candidate/<id>"` 记录喵。
   - **不额外消耗配额**，反映真实可用性喵。
2. **手动探测（点击触发）**：用户点击状态检测按钮 → 后端立即发一次真实探测请求并记录结果喵。
3. **自动定时探测（可选，P3）**：参考 `status_check_flexible.go` 的 idle + 连续探测上限策略，对启用自动探测的实体按空闲窗口补充探测喵。

### 4.3 探测请求构造（手动探测）

- **上游模型 / custom 候选**：解密凭据（复用 `credential_vault`），构造极简请求 `chat.completions`：`model=<real_model_name>`、`messages=[{role:"user",content:"ping"}]`、`max_tokens=1`，走 `ExecuteUserUpstreamModel` 同款 URL 归一化路径（含 `/pg→/v1` 修复）喵。**不计费、不写消费日志**（探测与余额/上限检查独立）喵。
- **internal 候选**：走原生 relay 的**渠道测试路径**（参考现有 `IsChannelTest` 语义，不扣用户配额、不写消费日志），选中候选分组下任一可用渠道发同款极简请求喵。
- **失败防御**：探测请求任何异常（凭据解密失败、上游 4xx/5xx、超时）都作为失败结果记录，返回受控错误信息，**不泄露密钥与密文**喵。

### 4.4 权限与可见性

- 上游模型、虚拟模型及其节点的状态检测结果**仅属主可见**（与 CRUD 的 owner 隔离一致）喵。
- 共享上游模型的**共享调用**不参与属主的状态统计（避免他人调用污染属主的可用性画像）；共享调用是否单独统计属于 P3 可选喵。
- 探测接口必须校验资源归属（复用 `loadOwnedUpstreamModel` / 虚拟模型 owner 校验），防止跨用户探测/枚举喵。

### 4.5 成本与安全控制

- 手动探测每次只发 1 个请求，`max_tokens=1`，且探测失败不计费、成功也不扣余额喵。
- 探测频率限制：单实体手动探测加最小间隔（如 10s）防连点；自动探测复用 idle + `max_consecutive_probes` 上限喵。
- 探测凭据只在服务端解密，绝不下发前端喵。

## 5. 数据模型与存储

### 5.1 复用 perf_metrics（推荐）

- 不新增时序表，复用 `pkg/perf_metrics` 的 `(model, group, bucket)` 聚合：
  - **上游模型**：`model = "user/<name>"`、`group = "__entity_probe__"`（固定探测组，与真实 relay 分组隔离）喵。
  - **虚拟模型整体**：`model = "virtual/<name>"`、`group = "__entity_probe__"` 喵。
  - **虚拟模型节点**：`model = "virtual/<name>/candidate/<id>"`、`group = "__entity_probe__"` 喵。
- 记录方式：新增 `perfmetrics.RecordEntityProbe(modelName string, latencyMs int64, success bool)` 包装 `Record`，内部强制 `Group = "__entity_probe__"`，避免调用方误传真实分组喵。
- 查询：`perfmetrics.Query({Model: 实体model, Group: "__entity_probe__", Hours: 24})` 复用现有 24h 序列能力喵。

### 5.2 新增探测元数据表（记录最近一次结果）

`entity_probe_states`（新表，参考 `status_check_probe_states`）：

```go
type EntityProbeState struct {
    ID            int64  // 主键
    Scope         string // upstream | virtual | virtual_candidate
    EntityID      int64  // 上游模型 id 或虚拟模型 id（candidate 时=候选 id）
    VirtualID     int64  // 仅 virtual_candidate 使用：所属虚拟模型 id
    OwnerUserID   int    // 属主，硬隔离
    LastProbeAt   int64  // 上次探测时间（Unix）
    LastSuccess   bool   // 最近一次是否成功
    LastLatencyMs int64  // 最近一次延迟
    LastError     string // 最近一次失败受控信息（成功时为空）
    ProbeCount    int64  // 累计探测次数
    SuccessCount  int64  // 累计成功次数
}
```

- 用于前端显示"最近一次探测"和"上次手动探测"去重/限流（`LastProbeAt` 最小间隔判断）喵。
- 被动统计的聚合仍走 perf_metrics，`EntityProbeState` 只存最近一次 + 累计计数（供"无 perf 桶时的兜底展示"）喵。
- `Scope+EntityID` 联合唯一（`(scope, entity_id)`，`virtual_candidate` 额外含 `virtual_id`）喵。

### 5.3 迁移

- `model/main.go` AutoMigrate 增加 `EntityProbeState`；三库兼容（无方言特性）喵。

## 6. 后端 API 设计

```
POST   /api/upstream-models/:id/status-check          // 手动探测上游模型
GET    /api/upstream-models/:id/status                // 返回统计 + 最近一次
POST   /api/virtual-models/:id/status-check           // 手动探测虚拟模型整体（聚合链）
GET    /api/virtual-models/:id/status                 // 返回整体统计 + 各节点摘要（供 Overview）
POST   /api/virtual-models/:id/candidates/:cid/status-check   // 手动探测单个节点
GET    /api/virtual-models/:id/candidates/:cid/status         // 返回节点统计 + 最近一次
```

响应统一结构：

```json
{
  "success": true,
  "data": {
    "availability": 92.31,       // 可用性 %
    "avg_latency_ms": 812,
    "request_count": 13,
    "availability_24h": [100, 0, 100, ...],  // 24 小时桶（无数据为 null/0）
    "last_probe_at": 1787774400,
    "last_success": true,
    "last_latency_ms": 745,
    "last_error": ""
  }
}
```

- 探测接口：`POST status-check` 执行探测 → 写 `EntityProbeState` + `perfmetrics.RecordEntityProbe` → 返回最新状态喵。
- 查询接口：`GET status` 聚合 perf_metrics 24h + `EntityProbeState` 最近一次，按 owner 隔离喵。
- 虚拟模型整体 `status-check`：对链上**第一个启用的候选**执行一次探测，并尝试后续候选回退（复用候选链逻辑：任一候选成功即整体成功），记录整体级 Sample（model=`virtual/<name>`）喵。

## 7. 前端设计

### 7.1 上游模型行（嗅探按钮左边）

- 在操作组 `<Check> <Sync> <Edit> <Delete>` 的 **Check 左边**插入状态检测入口喵。
- 交互：图标按钮（`HeartPulse`，复用状态检测页图标）+ 彩色状态点（绿=最近成功，红=最近失败，灰=无数据），title 显示 `可用性 xx% · 平均延迟 xxms · 上次探测 HH:mm` 喵。
- 点击 → `POST status-check` 手动探测 → 成功/失败 toast + 状态点更新 → 可选展开 Tooltip/Popover 展示 24h 柱状图与最近结果喵。
- 通过 `GET status` 在列表加载时并行拉取各模型状态摘要喵。

### 7.2 虚拟模型候选行（失败规则入口旁边）

- 候选行操作组（上移/下移/**ShieldCheck**/删除）的 **ShieldCheck 旁边**插入状态检测图标按钮 + 状态点（数据源 `GET /candidates/:cid/status`）喵。
- 点击 → `POST /candidates/:cid/status-check` 手动探测该节点 → 刷新状态点喵。
- 候选行未保存（`candidate.id === undefined`）时禁用状态检测（无实体可探测）喵。

### 7.3 虚拟模型 Overview（基本信息下方）

- 在 Overview 的显示名/候选数卡片**下方**增加"状态检测"卡片喵：
  - 整体可用性 %、平均延迟、24h 可用性柱状图（复用 `AvailabilityBars`）喵。
  - 最近一次探测结果（时间 + 成功/失败 + 错误信息）喵。
  - **探测按钮**：触发 `POST /api/virtual-models/:id/status-check` 喵。
  - **节点摘要**：列出链上每个候选的可用性/延迟/最近状态（紧凑行，点击跳到候选链 tab）喵。

### 7.4 共享组件

- 提取 `EntityStatusIndicator`（状态点 + 摘要 tooltip）与 `EntityStatusCard`（可用性/延迟/24h 柱状图），供三处复用喵。
- 复用现有 `AvailabilityBars`、`formatLatency`、`getSuccessRateTextClass` 喵。

## 8. 日志筛选修复

- `routes/_authenticated/usage-logs/$section.tsx:28` 的 `logTypeValues` 追加 `'8'`：
  ```ts
  const logTypeValues = ['0', '1', '2', '3', '4', '5', '6', '7', '8'] as const
  ```
- 同步检查 `$section.tsx` 是否还有其他硬编码类型白名单（目前仅此一处）喵。
- 回归：选择"自定上游" → URL `type=8` → 路由校验通过 → 请求带 `type=8` → 后端只返回 type=8 日志喵。

## 9. 分期实施计划（一期一交付）

### P1：日志筛选修复 + 上游模型状态检测 ✅（范围最小，先验证链路）
- 修 `logTypeValues` 加 `'8'` 喵。
- 后端：`EntityProbeState` 表 + `RecordEntityProbe` + 上游模型 `POST/GET status`（探测走 `ExecuteUserUpstreamModel` 极简请求，不计费）喵。
- 前端：上游模型行状态检测按钮（Check 左边）+ 状态点 + 探测喵。
- 测试：schema 白名单单测、探测成功/失败/凭据失败/超时、owner 隔离、24h 聚合、不计费断言喵。

### P2：虚拟模型整体 + 候选节点状态检测
- 后端：`POST/GET /api/virtual-models/:id/status`、`/candidates/:cid/status`；整体探测走候选链回退；custom 候选探测走透传极简请求；internal 候选走渠道测试路径（不计费）喵。
- 前端：Overview 状态检测卡片 + 候选行状态点（ShieldCheck 旁）喵。
- 测试：整体聚合、节点探测（internal/custom/引用上游模型三形态）、失败回退、不计费喵。

### P3：自动定时探测 + 历史抽屉（可选增强）
- 复用 `status_check_flexible.go` 的 idle + `max_consecutive_probes` 策略，为开启自动探测的实体定时补充探测喵。
- 前端 `StatusHistoryDrawer` 复用展示 24h 明细喵。
- 共享上游模型的共享调用统计（是否纳入属主画像）在此期确认喵。

## 10. 验证

- 每期：后端相关包 `go test`、前端 `bunx vitest run`、`bun run typecheck`、`bun run build` 喵。
- 冒烟：创建用户上游模型 → 行内状态检测按钮探测 → 状态点/可用性/延迟更新；设置虚拟模型（internal + custom + 引用上游模型候选）→ Overview 与候选行状态检测可用、整体探测任一候选成功即成功；日志选"自定上游"只返回 type=8 日志喵。
- 回归：普通计费、上游模型余额/上限、虚拟模型候选链失败规则、共享调用均不受影响喵。

## 11. 待确认细节（已给推荐值，主人有异议再改）

1. **数据来源**默认"被动统计 + 手动探测"，自动定时探测放 P3 可选；是否一上来就要自动探测？喵
2. 探测请求用 **极简 chat.completions（max_tokens=1）**；若上游对极小 max_tokens 有副作用可改用 `GET /models`（但无法验证生成可用性）喵。
3. internal 候选探测走**渠道测试（不计费）**路径；若实现成本过高，可退化为"只做渠道可用性查询不真正请求"（成功率仅来自真实调用）喵。
4. 共享上游模型的**共享调用是否纳入属主状态统计**：推荐不纳入（P3 单独确认）喵。
5. `EntityProbeState` 表 vs 完全复用 perf_metrics（不存最近一次）：推荐加表以便展示"最近一次探测/错误信息"，是否可接受多一张表？喵
6. 手动探测按钮点击后是否自动展开 24h 详情：推荐点击只探测+更新状态点，详情走 Tooltip/Popover 喵。

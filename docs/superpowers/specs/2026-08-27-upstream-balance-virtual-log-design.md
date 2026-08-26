# 上游模型余额重构 + 虚拟模型日志体系 设计规格

## 1. Context

上一轮（`2026-08-26-user-upstream-model-billing-design.md`）已完成用户上游模型（`user/<name>`）独立计费、共享、候选链引用的落地，并写入了状态检测 spec（`2026-08-27-entity-status-check-design.md`）喵。

本轮在现有体系之上做**六块重构与增强**喵：

1. **上游模型余额机制重构**：把"余额 / 使用上限 / 剩余API额度 / 共享额度"四个易混淆的数字精简为"**余额 / 可用 / 共享**"三个递减账户，并修正扣费语义喵。
2. **新增"虚拟模型"日志类型**：所有虚拟模型请求（internal 候选 + custom 候选）归入新日志类型，不再混入普通消费日志喵。
3. **渠道字段 `#xx` → `候选xx`**：虚拟模型日志的渠道列改展示候选序号，不再显示渠道 id 喵。
4. **日志详情记录所有候选与各自错误**：参考 autoapi 的 `attempts` 结构，逐候选记录成功/失败与受控错误信息喵。
5. **数据看板 / 排行榜数据源收窄**：只统计 new-api 内部模型，用户自带上游、共享上游、虚拟模型自定义上游全部不计入喵。
6. **虚拟模型（分组，模型）选择入口**：在游乐场的分组下拉中增加"虚拟模型"分类，虚拟模型仅游乐场可选、其余位置一律不显示喵。

## 2. 现状调研结论

### 2.1 上游模型余额字段与扣费（`model/user_upstream_model.go`）

现有字段（金额一律"分" int64）：

| 字段 | 现状语义 | 扣费行为 |
| --- | --- | --- |
| `BalanceCents` 余额 | 手动预存 / 一键同步嗅探 | **自用扣减**（下限钳 0） |
| `SpendLimitCents` 使用上限 | 自用累计阈值 | 请求前硬检查 `TotalSpent < SpendLimit` |
| `TotalSpentCents` 累计消耗 | 自用累计值 | 自用后递增 |
| `UpstreamRemainingCents` 剩余API额度 | 嗅探到的上游真实剩余，仅展示 | 不参与扣费；"一键设为余额"写 Balance |
| `ShareLimitCents` 共享额度 | 共享累计阈值 | 请求前硬检查 `ShareSpent < ShareLimit` |
| `ShareSpentCents` 共享累计 | 共享累计值 | **共享调用免费**，只递增 |

现状问题（主人指出的"目前扣费似乎有问题"）：

- 共享调用**完全不扣费**（只累计 `ShareSpent`），属主余额/额度不被共享消耗影响喵。
- 自用只扣 `Balance`，`limit` 走"累计 vs 上限"的比较式，用户体感是"余额扣了、limit 没动"，两个数字对不上喵。
- "余额 / 使用上限 / 剩余API额度"三个概念并存，UI 上用户难以判断"到底还能用多少"喵。

### 2.2 虚拟模型请求日志现状

- **internal 候选**：分发层 `middleware/distributor.go` 激活候选后走原生 relay 计费，写 `type=2` 消费日志（`service/text_quota.go` → `RecordConsumeLog`），渠道字段是**真实渠道 id**喵。
- **custom 候选**：`ExecuteCustomCandidate` 纯透传；引用用户上游模型（`middleware/upstream_model.go`）时写 `type=8` 自定上游日志，费用归属该上游模型余额；**纯直填 custom 目前不写日志**喵。
- 虚拟模型自身没有独立日志类型，internal 候选混入普通消费统计，custom 混入自定上游统计喵。

### 2.3 渠道字段渲染（`web/src/features/usage-logs/components/columns/common-logs-columns.tsx`）

- 普通日志渠道列：`channel_name #channel_id`（无名字时 `#channel_id`）；重试链用 `admin_info.use_channel` 渲染 `#c1 → #c2` 喵。
- 路由 `logTypeValues` 上轮已补 `'8'`，本轮再补 `'9'` 喵。

### 2.4 数据看板 / 排行榜数据源

- 看板数据源 = `quota_data` 表；`model/log.go` 的 `LogQuotaData`（受 `DataExportEnabled` 控制）在写**消费日志**与**task 结算日志**时按小时桶记录 `(user, model_name, use_group, token, channel)` 喵。
- 目前虚拟模型 internal 候选（走消费日志）会写进 `quota_data`，用户上游模型（type=8）**不经过** `LogQuotaData`（它在 createLog 后不调用 LogQuotaData），但 virtual custom 候选如进入消费路径则可能计入——需实现时统一收口喵。

### 2.5 游乐场模型 / 分组选择器（`web/src/components/model-group-selector.tsx`）

- `GroupSelector`：分组下拉，数据来自 `getUserGroups()` 喵。
- `ModelSelector`：模型下拉，按 `model.category` 分组展示（`{{category}} Models`）；游乐场把启用状态的虚拟模型**追加到普通模型列表末尾**（无 `category` 时归入 "Other"），见 `playground-option-utils.ts` 的 `mergeVirtualModelOptions` 喵。
- 主人建议：把虚拟模型提升为**分组下拉里的一个分类**，选中后模型下拉只显示虚拟模型；虚拟模型仅游乐场生效喵。

## 3. 已确认需求（主人拍板记录）

| 项 | 结论 |
| --- | --- |
| 账户映射 | **余额** ← 原"余额(Balance)"（手动预存）；**可用** ← 原"使用上限(limit)"与"剩余API额度"在界面上**合并**为一个"可用额度"；**共享** ← 原"共享额度" |
| 扣费规则 | 三账户都是**递减账户**（每次调用扣本次费用）；**自用扣「余额+可用」**、**共享扣「余额+可用+共享」**；**余额=0 停全部（自用+共享）** |
| 停止共享 | **可用=0 或 共享=0** 时自动停止共享 |
| 虚拟模型日志类型 | **所有虚拟模型请求**（internal + custom 候选）归入新「虚拟模型」日志类型；internal 候选不再写 type=2 消费日志 |
| 看板/排行榜范围 | 排除 `user/` 前缀（自带+共享上游）与虚拟模型 **custom 候选**；virtual **internal 候选仍计入**（走 new-api 内部渠道） |

## 4. 需求A：上游模型余额机制重构

### 4.1 新账户语义与字段映射

新账户（全部**递减式**，金额存"分"）：

| 新账户 | UI 文案 | 语义 | 来源字段 | 备注 |
| --- | --- | --- | --- | --- |
| **余额 balance** | 余额 | 这个模型理论上还能用那么多（上游真实可用量） | 原 `BalanceCents` | 手动预存，或嗅探"一键设为余额" |
| **可用 available** | 可用额度 | 这个模型用户能接受用那么多（可承受的消耗上限） | 新字段 `AvailableCents` | 初始值 = 原 `SpendLimitCents`；嗅探"一键设为可用" |
| **共享 share** | 共享额度 | 这个模型共享给别人的额度 | 原 `ShareLimitCents` | 递减账户 |

弃用字段（保留列以兼容旧数据，**不再读取、不再参与扣费、前端不展示**）：

- `TotalSpentCents`（自用累计，由可用递减取代）
- `ShareSpentCents`（共享累计，由共享递减取代）
- `UpstreamRemainingCents` / `UpstreamRemainingAt`（剩余API额度并入"可用"，嗅探结果转为"一键设为可用"的参考来源）

### 4.2 字段迁移（三库兼容）

- 新增 `available_cents` 列（int64，默认 0）喵。
- 数据回填：`available_cents = spend_limit_cents`（旧"使用上限"作为初始可用额度；为 0 表示未设置，需用户填）喵。
- `model/main.go` AutoMigrate 增列；回填用一次幂等 SQL（三库通用 `UPDATE user_upstream_models SET available_cents = spend_limit_cents WHERE available_cents = 0 AND spend_limit_cents > 0`，走 GORM 条件更新）喵。

### 4.3 扣费规则（`DeductUserUpstreamModelCharge` 重构）

在 `model/user_upstream_model.go` 重构结算函数，事务内 `lockForUpdate` 行锁，减法一律钳制到 ≥0 喵：

```go
// 自用调用：扣「余额 + 可用」两个递减账户，各自钳 0，绝不出现负数喵。
if !isShared {
    upstreamModel.BalanceCents = clampSub(upstreamModel.BalanceCents, costCents)
    upstreamModel.AvailableCents = clampSub(upstreamModel.AvailableCents, costCents)
} else {
    // 共享调用：扣「余额 + 可用 + 共享」三个递减账户喵。
    upstreamModel.BalanceCents = clampSub(upstreamModel.BalanceCents, costCents)
    upstreamModel.AvailableCents = clampSub(upstreamModel.AvailableCents, costCents)
    upstreamModel.ShareLimitCents = clampSub(upstreamModel.ShareLimitCents, costCents)
}
```

- 费用为 0 时不产生写入喵。
- 扣费后若 `AvailableCents == 0 || ShareLimitCents == 0`，该模型**不再作为共享模型**（运行时判定 + 可选惰性置 `share_enabled=false`，便于广场/共享列表即时消失）喵。

### 4.4 请求前硬检查（`handleUserUpstreamModelRequest` 与候选链激活引用时）

| 调用方 | 硬检查条件 | 不满足返回 |
| --- | --- | --- |
| 自用直接调用 | `Enabled && Balance > 0 && Available > 0` | `upstream_model_quota_exhausted` |
| 共享调用 | `Enabled && Balance > 0 && Available > 0 && Share > 0` | `upstream_model_quota_exhausted`（不再出现在共享列表） |
| 虚拟模型 custom 候选引用 | 按属主自用语义检查 | `upstream_model_quota_exhausted` |

- 语义不变：**请求时拦截、改大额度即恢复**；本次照常返回、余额耗尽后下一次被拦截喵。
- `GET /api/upstream-models/shared/:name/status` 与模型广场"共享中"判定条件同步改为 `ShareEnabled && Balance > 0 && Available > 0 && Share > 0` 喵。

### 4.5 嗅探与"一键设为"

- 嗅探接口保留（`POST /api/upstream-models/:id/balance-check`），结果写入 `UpstreamRemainingCents` 作为**只读参考提示**（"上游真实剩余约 xx 元"）喵。
- **"一键设为可用"**按钮：把嗅探值写入 `AvailableCents`（替代原"同步为余额"的主要用途）喵。
- **"一键设为余额"**按钮：保留，把嗅探值写入 `BalanceCents` 作为预存参考喵。

### 4.6 前端表单变化（`web/src/features/upstream-models/` 创建/编辑抽屉）

- 计费区三栏：**余额**（手动预存 + 嗅探参考提示）、**可用额度**（用户能接受用那么多 + "一键设为可用"）、**共享额度**喵。
- 删除"使用上限 / 剩余API额度 / 累计消耗 / 共享累计"展示喵。
- 共享区：共享开关 + 共享额度 + 状态提示（可用=0 或 共享=0 时显示"已自动停止共享"）喵。

## 5. 需求B：新增"虚拟模型"日志类型

### 5.1 类型定义

- `model/log.go`：`LogTypeVirtualModel = 9 // 虚拟模型：所有虚拟模型请求的使用日志喵`喵。
- 前端 `LOG_TYPE_ENUM` / `LOG_TYPES` 增加 `{ value: 9, label: 'Virtual Model', color: ... }`；`LOG_TYPE_FILTERS` 增加"虚拟模型"；`DISPLAYABLE_LOG_TYPES` 增加 9；`routes/_authenticated/usage-logs/$section.tsx` 的 `logTypeValues` 追加 `'9'` 喵。

### 5.2 归类规则

| 请求路径 | 旧日志类型 | 新日志类型 |
| --- | --- | --- |
| 虚拟模型 internal 候选 | `type=2` 消费日志 | **`type=9` 虚拟模型** |
| 虚拟模型 custom 候选（引用用户上游模型） | `type=8` 自定上游 | **`type=9` 虚拟模型** |
| 虚拟模型 custom 候选（纯直填 base_url/api_key） | 无日志 | **`type=9` 虚拟模型** |
| 用户上游模型独立调用（`user/<name>`，非虚拟模型） | `type=8` 自定上游 | `type=8` 不变 |

- **计费行为不变**：internal 候选仍按 new-api 原生渠道计费扣用户 quota（仅日志类型变化）；custom 候选引用上游模型仍扣该上游模型余额；纯 custom 不扣任何 new-api 配额喵。
- 实现：虚拟模型分发层（`middleware/distributor.go`）在建立 `virtualModelExecutionState` 时写入 `ContextKeyLogType=9`；relay 消费结算写日志时读取该 key 覆盖 `type`（internal 候选路径），custom 候选路径直接按 type=9 写喵。

### 5.3 日志 quota 与 usage

- internal 候选：正常记录 quota（扣用户配额）+ usage 喵。
- custom 候选：`quota=0`，记录 usage 与折算信息（引用上游模型时额外记上游模型计费明细）喵。

## 6. 需求C：渠道字段 `#xx` → `候选xx`

### 6.1 虚拟模型日志渠道字段

- `type=9` 日志的 `channel_id` 存**最终成功候选的链上序号**（1 起）；`channel_name` 存候选标识（internal: 真实模型名；custom: 模型名 / 显示名）喵。
- 重试链：`Other.admin_info.use_channel` 存候选序号数组（如 `[1,3]` 表示候选1失败、候选3成功），供前端渲染 `候选1 → 候选3` 喵。

### 6.2 前端渲染

- `common-logs-columns.tsx` 渠道列：`type === 9` 时显示 `候选{n}`（有重试链显示 `候选1 → 候选3`），**不再显示 `#channel_id`**；其余类型保持现状喵。
- 脱敏规则不变（管理员可见完整候选标识，普通用户不展示敏感候选信息）喵。

## 7. 需求D：日志详情记录所有候选（类 autoapi）

### 7.1 参考 autoapi 的 attempts 结构

autoapi 在候选链耗尽时返回 502，错误体 `attempts` 字段列出**每个候选各自的失败原因**；成功响应时日志同样记录每个候选的尝试过程喵。本轮把同样的信息落入 new-api 虚拟模型日志的 `Other` 喵。

### 7.2 Other 数据结构（`type=9` 日志）

```json
{
  "virtual_model": "virtual/demo",
  "candidates": [
    {
      "seq": 1,
      "source": "internal",
      "label": "gpt-4o",
      "success": true,
      "status_code": 200,
      "error_class": "",
      "error_message": "",
      "elapsed_ms": 812,
      "retry_count": 0
    },
    {
      "seq": 2,
      "source": "custom",
      "label": "my-upstream",
      "success": false,
      "status_code": 429,
      "error_class": "rate_limited",
      "error_message": "上游限流，请稍后重试",
      "elapsed_ms": 310,
      "retry_count": 2
    }
  ],
  "final_success": true
}
```

- `candidates` 按执行顺序排列，**每个候选一次尝试记一条**（含失败规则 retry 的多次尝试以 `retry_count` 体现）喵。
- `error_class` / `error_message` 只写受控信息（`NormalizeCandidateFailure` 的错误分类 + 通用文案），**绝不落密钥、密文、完整 URL、请求正文**喵。
- 记录时机：internal 候选在原生 relay 结果返回时（`AdvanceVirtualModelAfterNativeFailure` 与成功分支）；custom 候选在 `ExecuteCustomCandidate` 返回时；候选链整体结束（成功或耗尽）时随 `RecordVirtualModelLog` 一起落库喵。
- 收集载体：`virtualModelExecutionState` 增加 `candidateAttempts []CandidateAttempt` 切片，随执行过程追加喵。

### 7.3 前端日志详情

- 日志详情组件对 `type=9` 增加"候选尝试"区块：按序列出每个候选（序号 + 来源标签 + 候选标识 + 状态码 + 成功/失败 + 耗时），失败项展示受控错误信息喵。
- 成功候选绿色勾、失败红色叉；链最终成功时整体标注成功喵。

## 8. 需求E：数据看板 / 排行榜排除用户上游

### 8.1 排除规则

`LogQuotaData` 写入 `quota_data` 前按 `model_name` 前缀过滤：

```go
// 只统计 new-api 内部模型；用户自带/共享上游与虚拟模型自定义上游一律不计入看板与排行榜喵。
if strings.HasPrefix(params.ModelName, "user/") || strings.HasPrefix(params.ModelName, "virtual/") {
    return
}
```

- `user/` 前缀：自带 + 共享上游全部排除喵。
- `virtual/` 前缀：virtual custom 候选日志 `ModelName = virtual/<name>` 排除喵。
- **virtual internal 候选仍计入**：其原生 relay 日志 `ModelName` 为候选真实模型名（无 `virtual/` 前缀），自然计入看板/排行榜，符合主人"internal 仍计入"的决策喵。
- **实现时确认**：若 internal 候选日志 `ModelName` 意外带 `virtual/` 前缀，则改为写候选真实模型名，保证 internal 计入喵。

### 8.2 实现点

- `model/log.go` `LogQuotaData` 两个调用点前统一过滤（消费日志 + task 结算日志）喵。
- 回归：普通模型看板、排行榜计数不变；虚拟模型 internal 候选计入；user/ 与 virtual custom 不计入喵。

## 9. 需求F：虚拟模型（分组，模型）分类入口

### 9.1 ModelGroupSelector 扩展

- `ModelGroupSelector` 增加 prop `showVirtualModels = false`（仅游乐场传 `true`）喵。
- `showVirtualModels=true` 时 `GroupSelector` 分组列表**末尾追加**一个分类项：`{ value: 'virtual', label: t('Virtual Models') }`（"虚拟模型"）喵。
- 选中 `virtual` 分组 → 模型下拉显示该用户**启用状态**的虚拟模型（`value = virtual/<name>`），其余分组行为不变喵。
- 分组 value 为 `virtual` 时：游乐场不再请求真实 `getUserModels(group)`（虚拟模型来自本端已加载的虚拟模型列表），避免 404 喵。

### 9.2 游乐场行为调整（`web/src/features/playground/`）

- `use-playground-options.ts` 给 `ModelGroupSelector` 传 `showVirtualModels`，并把虚拟模型选项从 `mergeVirtualModelOptions`（追加到普通模型末尾）中**移除**——虚拟模型只通过 `virtual` 分组分类出现，不再混在 "Other" 里喵。
- 用户默认分组为 `virtual` 时模型下拉默认选中第一个启用虚拟模型喵。

### 9.3 其余位置不显示

- `ModelGroupSelector` 的默认 `showVirtualModels=false`，渠道测试、模型测试、其他使用该组件的页面**一律不显示虚拟模型分类**喵。
- 后端 `/api/user/models` 不因本需求变化（虚拟模型不进真实分组），仅前端选择器入口变化喵。

## 10. 分期实施计划（一期一交付）

### P1：余额机制重构

- 后端：`available_cents` 字段 + 迁移回填；`DeductUserUpstreamModelCharge` 三账户递减扣费；请求前硬检查（自用/共享/候选引用）；停止共享判定（可用=0 或 共享=0 或 余额=0）；嗅探"一键设为可用"接口调整喵。
- 前端：上游模型表单改为 余额/可用/共享 三栏 + 一键设为可用按钮；共享停止提示喵。
- 测试：自用/共享扣费账户、钳 0、并发扣费、余额=0 停全部、可用/共享=0 停共享、候选引用硬检查、迁移回填幂等喵。

### P2：虚拟模型日志类型 + 渠道候选xx + 详情候选序列

- 后端：`LogTypeVirtualModel=9`；internal 候选写 type=9（ContextKey 覆盖）；custom 候选（引用上游 + 纯直填）写 type=9；渠道字段改候选序号 + `use_channel` 候选数组；`candidateAttempts` 收集 + Other 落库喵。
- 前端：日志类型筛选增加"虚拟模型"；渠道列 type=9 渲染 `候选n` / `候选1 → 候选3`；日志详情"候选尝试"区块喵。
- 测试：internal 成功/失败日志类型、custom 引用上游日志类型（费用仍归属上游模型）、纯 custom 日志、候选序列正确性、错误脱敏、筛选/渲染喵。

### P3：看板排除 + 虚拟模型分组分类

- 后端：`LogQuotaData` 前缀过滤（user/ 与 virtual/ 排除）喵。
- 前端：`ModelGroupSelector.showVirtualModels` + 游乐场 `virtual` 分组分类 + 移除模型列表末尾追加逻辑喵。
- 测试：看板 internal 计入 / user 与 custom 不计入；分组选择器虚拟模型分类仅游乐场出现、其余页面不出现喵。

## 11. 验证

- 每期：后端相关包 `go test`、前端 `bunx vitest run`、`bun run typecheck`、`bun run build` 喵。
- 冒烟：创建用户上游模型 → 设置 余额/可用/共享 → 自用扣「余额+可用」、共享扣「余额+可用+共享」→ 余额=0 停全部、可用或共享=0 停共享 → 广场/共享列表即时消失 → 改大恢复喵；配置虚拟模型（internal + custom + 引用上游模型）→ 全部请求出现在 type=9 日志、渠道列显示 `候选n`、详情列出每个候选与错误 → 看板 internal 候选计入、user 与 custom 不计入 → 游乐场分组下拉出现"虚拟模型"分类、仅游乐场可见喵。
- 回归：普通计费、type=8 自定上游、虚拟模型候选链失败规则、共享调用计费均不受影响喵。

## 12. 已确认决策（主人拍板记录）

1. **账户映射**：余额 ← 原余额（手动预存，语义=理论上还能用那么多）；可用 ← 原"使用上限 + 剩余API额度"合并（用户能接受用那么多）；共享 ← 原共享额度喵。
2. **扣费规则**：三账户递减（每次扣本次费用）；自用扣「余额+可用」，共享扣「余额+可用+共享」；**余额=0 停全部（自用+共享）**喵。
3. **停止共享**：可用=0 或 共享=0 自动停止共享（共享判定 = `ShareEnabled && Balance>0 && Available>0 && Share>0`）喵。
4. **虚拟模型日志类型**：所有虚拟模型请求（internal + custom）归入新「虚拟模型」类型；internal 候选不再写 type=2 消费日志；type=8 仅保留给用户上游模型独立调用喵。
5. **看板/排行榜范围**：排除 `user/` 前缀与 virtual custom 候选；virtual internal 候选仍计入喵。
6. **虚拟模型入口**：分组下拉新增"虚拟模型"分类，仅游乐场生效，其余位置不显示喵。
7. **自主决策补充**（实现时遵循）：
   - 弃用字段保留列不删，避免三库迁移风险；迁移回填幂等喵。
   - 嗅探结果仍写 `UpstreamRemainingCents` 只读展示，"一键设为可用"写 `AvailableCents`、"一键设为余额"写 `BalanceCents` 喵。
   - 候选尝试的 `error_message` 只写受控通用文案，绝不落密钥/URL/正文；普通用户日志视图不展示敏感候选标识喵。

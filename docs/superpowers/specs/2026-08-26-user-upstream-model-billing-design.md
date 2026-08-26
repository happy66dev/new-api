# 用户上游模型与独立计费系统 设计规格

## 1. Context

new-api 目前只有"虚拟模型"（`virtual/<name>`）供用户管理私有模型链，其中 **custom 候选是纯透传**：用户填 base_url + api_key，请求直接转发，**不计费、不写日志、无额度控制**喵。

本次新增完整能力：**用户上游模型**（每个模型独立 base_url + api_key + 真实模型名），配套**独立 RMB 计费系统**、**余额/使用上限控制**、**上游额度嗅探**、**跨用户共享**（硬编码"用户共享分组"）以及**共享日志**喵。功能按 P1~P5 分期实现喵。

普通模型计费、虚拟模型 internal 候选计费保持不变；本系统是独立于 new-api 默认 quota 的平行计费体系喵。

## 2. 已确认语义（问答结论）

| 项 | 结论 |
| --- | --- |
| 层级结构 | **一模型一上游**：每个上游模型独立配置 base_url + api_key + 真实模型名 |
| 页面参考 | **混搭**：整体布局参考管理员模型页（`web/src/features/models/`），表单字段参考渠道页（base_url/api_key/模型名/认证方式） |
| 候选链联动 | custom 候选**改为从用户上游模型选取**（引用整个上游模型条目） |
| 计费模型 | **三者并存**：余额扣费 + 使用额度上限 + 剩余API额度展示 |
| 余额来源 | **两者都要**：手动预存 + 一键把嗅探结果设为余额 |
| 计费单位 | **RMB（人民币）**，独立于 newapi 积分 |
| 共享耗尽 | **仅停止共享**：共享额度耗尽自动关共享，所有者自己继续用 |
| 广场展示 | **所有者看全量额度**，其他用户看**共享剩余额度** |
| 调用方式 | **可直接调用 + 可入候选链** |
| 停止形式 | **请求时拦截**：返回额度不足错误，模型保持启用，改大额度/上限立即恢复 |
| 共享可见 | **模型广场 + playground/API 模型列表**都出现 |
| 共享计量 | **参考 newapi 机制**：按"每百万 token 多少 RMB"折算累计消耗 |

## 3. 术语

- **用户上游模型（User Upstream Model）**：用户私有的一条上游配置，`upstream/<normalizedName>` 唯一标识喵。
- **余额（Balance）**：该模型可扣减的 RMB 余额，手动预存或一键同步嗅探结果喵。
- **使用额度上限（SpendLimit）**：自用累计消耗（RMB）阈值，达到后请求拦截喵。
- **剩余API额度（UpstreamRemaining）**：上游 key 的真实剩余额度（嗅探或手动设置），仅展示，不直接参与扣费喵。
- **共享额度（ShareLimit）**：共享调用累计折算消耗（RMB）阈值，达到后自动停止共享（所有者自用不受影响）喵。

## 4. 数据模型

### 4.1 `user_upstream_models` 表（新）

```go
type UserUpstreamModel struct {
    ID                     int64
    OwnerUserID            int    // 属主，硬约束不可被客户端修改
    NormalizedName         string // 规范名，owner 内唯一（复用 NormalizeVirtualModelName 思路）
    DisplayName            string
    Enabled                bool
    // 上游连接（复用 service/virtualmodel/credential_vault.go 的 AES 加密）
    EncryptedBaseURL       string
    EncryptedAPIKey        string
    BaseURLFingerprint     string
    APIKeyFingerprint      string
    RealModelName          string // 上游真实模型名
    AuthStyle              string // bearer/api_key/anthropic（复用 NormalizeVirtualModelAuthStyle）
    // 计费（RMB，金额存"分" int64 避免浮点；每类型独立价格，单位元/每百万 token）
    ModelRatio             string // 输入价格：每百万输入 token 的 RMB，decimal 字符串，默认 "1"
    CompletionRatio        string // 输出价格，默认 "1"
    CacheRatio             string // 缓存命中价格，默认 "1"
    CacheCreationRatio     string // 缓存写入价格，默认 "1"
    CacheCreation5mRatio   string // 5m 缓存写入价格，默认 "1"
    CacheCreation1hRatio   string // 1h 缓存写入价格，默认 "1"
    ImageRatio             string // 图片输入价格，默认 "1"
    AudioRatio             string // 音频输入价格，默认 "1"
    AudioCompletionRatio   string // 音频输出价格，默认 "1"
    BalanceCents           int64  // 余额（分）
    SpendLimitCents        int64  // 自用使用上限（分），0=不限
    TotalSpentCents        int64  // 自用累计消耗（分）
    // 剩余API额度（展示）
    UpstreamRemainingCents int64  // 剩余API可用额度（分），嗅探或手动设置
    UpstreamRemainingAt    int64  // 上次嗅探/更新时间
    // 嗅探
    BalanceCheckEnabled    bool
    BalanceCheckPath       string // 自定义嗅探路径，空则用默认 OpenAI billing 接口
    // 共享
    ShareEnabled           bool
    ShareLimitCents        int64  // 共享额度（分），0=不限
    ShareSpentCents        int64  // 共享累计消耗（分）
    // 展示
    ShowBalanceEnabled     bool   // 是否在模型广场展示额度
    // 版本与时间
    Version                int
    CreatedAt              time.Time
    UpdatedAt              time.Time
}
```

- 金额一律 `int64` 分存储，前端展示转为元（2 位小数）；价格用 decimal 字符串，运算走 `github.com/shopspring/decimal`（项目已有），结果再转分（复用 `common.QuotaFromDecimalChecked` 的饱和防御思路）喵。
- `VirtualModelCustomCandidate` 增加 `UpstreamModelID *int64`：非空时激活改从 `user_upstream_models` 取 base_url/api_key/real_model_name；为空时保留旧直填配置（兼容已有数据）喵。

### 4.2 硬编码共享分组

- 常量 `constant.GroupUserShared = "user-shared"`，所有用户可用分组默认包含它喵。
- 共享模型不写 abilities 表（不走 channel/ability 索引），`/api/user/models` 在 `user-shared` 分组下直接查共享中的 `upstream/<name>` 返回喵。

### 4.3 日志类型

- `model/log.go` `LogType` 增加 `CustomUpstream=8`（"自定上游"）：用户上游模型的所有使用日志（**自用 + 共享**）都归入此类型喵。
- 自用与共享通过 `group` 字段区分：自用=所属分组（如 default），共享=`user-shared` 共享分组喵。
- 共享调用 quota=0（免费），但记录 usage 与折算 RMB 供展示喵。

## 5. 计费规则（独立 RMB 系统）

### 5.1 usage 解析
- 复用 `relaykit/dto/openai_response.go` 的 `Usage`（`PromptTokens`/`CompletionTokens`/`PromptCacheHitTokens`/`CacheWriteTokens`/`InputTokenDetails.ImageTokens`/`AudioTokens` 等），分类口径与 new-api 的 `calculateTextQuotaSummary`（`service/text_quota.go`）一致喵。
- 上游返回非 OpenAI 兼容格式或缺失 usage 时：不计费（费用=0），写日志标注"无 usage"；失败请求不扣费喵。

### 5.2 费用计算（每类型直接价格，单位 RMB 转分）
每个价格字段代表"每百万该类型 token 的 RMB 元"，各分类 token 数乘各自价格后求和喵：

```
promptBaseTokens = PromptTokens
  - CachedTokens          // 缓存命中从基础输入扣除
  - CacheCreationTokens   // 缓存写入从基础输入扣除
  - ImageTokens           // 图片输入从基础输入扣除
  - AudioTokens           // 音频输入从基础输入扣除
  （钳制 ≥0，参考 new-api 的 overlap 钳制）
textCompletionTokens = CompletionTokens - AudioCompletionTokens   // 音频输出从普通输出扣除，钳制 ≥0
remainingCacheCreationTokens = CacheCreationTokens - 5mTokens - 1hTokens   // 钳制 ≥0

costCents = (
    promptBaseTokens × ModelRatio
  + CachedTokens × CacheRatio
  + remainingCacheCreationTokens × CacheCreationRatio
  + 5mTokens × CacheCreation5mRatio
  + 1hTokens × CacheCreation1hRatio
  + ImageTokens × ImageRatio
  + AudioTokens × AudioRatio
  + textCompletionTokens × CompletionRatio
  + AudioCompletionTokens × AudioCompletionRatio
) / 1e6 × 100   // 元 → 分
```
- 全程 decimal，避免浮点误差；转分用 `common.QuotaFromDecimalChecked`，钳制时 `common.SysError` 审计喵。
- 所有价格字段默认 1 元/百万 token（前端默认值即 1，用户按需调整）喵。
- 按请求固定价（new-api 的 `UsePrice`/ModelPrice 分支）本系统暂不支持；上游模型一律按 token 分类型计费喵。

### 5.3 扣费与拦截
- **请求前硬检查**（`handleUserUpstreamModelRequest` 与候选链激活时）：
  - 模型 Enabled、余额 `> 0`（自用）、`TotalSpent < SpendLimit`（自用）、共享模型需 `ShareSpent < ShareLimit`；任一不满足返回 `upstream_model_quota_exhausted`（409/402）喵。
- **请求后**：按实际费用扣余额（不足则置 0）、累加 `TotalSpent`、共享调用累加 `ShareSpent`；本次照常返回，耗尽后下次请求被拦截（符合"请求时拦截、改大即恢复"语义）喵。
- 扣减使用悲观锁（`lockForUpdate`），`Balance`、`TotalSpent`、`ShareSpent` 的减法不得出现负数（用 checked 减法/钳制）喵。

### 5.4 嗅探
- 默认路径：`GET {base_url}/v1/dashboard/billing/subscription` + `GET {base_url}/v1/dashboard/billing/usage`，剩余 USD → 按汇率转 RMB 分；`BalanceCheckPath` 非空则用自定义路径喵。
- 参考 `controller/channel-billing.go` `updateStandardChannelBalance` 的实现方式；失败记录错误不自动禁用喵。
- "一键设为余额"按钮：把 `UpstreamRemainingCents` 写入 `BalanceCents` 喵。

## 6. 调用流程

### 6.1 直接调用 `upstream/<name>`
1. 分发层新增 `isUserUpstreamModelRequest`（前缀 `upstream/`），在 `Distribute()` 中识别并转 `handleUserUpstreamModelRequest`（独立于虚拟模型链）喵。
2. 按 owner（会话态）或 token 授权查询启用模型；校验硬检查 → 解密凭据 → 构造上游请求转发喵。
3. **读响应并解析 usage**（非流式读完整 body；流式逐 SSE 事件累积，最终 chunk 取 usage）→ 计费 → 原样转发响应喵。
4. 写日志（type=自定上游/CustomUpstream），`Other` 记录 `custom_cost_rmb`/`model_ratio`/`completion_ratio`/`cache_ratio`/`cache_creation_ratio`/`image_ratio`/`audio_ratio`/四类 token 明细喵。

### 6.2 虚拟模型候选链引用
- custom 候选激活时若 `UpstreamModelID` 非空：从用户上游模型取凭据与真实模型名，执行同上的 usage 解析 + 计费 + 日志（计费归属该用户上游模型）喵。
- 候选链自身的 timeout/retry/failure rules 保持生效；用户上游模型被引用后其自身 Enabled/余额/上限同样生效喵。

## 7. 共享机制

- 共享模型 = `ShareEnabled && ShareSpent < ShareLimit` 的用户上游模型，归入 `user-shared` 分组喵。
- `GetUserUsableGroups` 对所有用户追加 `user-shared`；`/api/user/models`、playground、模型广场均可见喵。
- **共享调用不计费**（不扣所有者余额、不累加 TotalSpent），只累加 `ShareSpent`，并写 **type=自定上游 日志**（group=user-shared，quota=0，记录使用者、token、折算 RMB）喵。
- 共享额度耗尽 → 仅停止共享（`user-shared` 下不再出现、共享请求拦截），所有者自用不受影响喵。

## 8. API 与路由

- `GET/POST /api/upstream-models`、`GET/PUT/DELETE /api/upstream-models/:id`（owner 隔离，参考虚拟模型 controller 模式）喵。
- `POST /api/upstream-models/:id/balance-check`（嗅探）、`POST /api/upstream-models/:id/balance/sync`（嗅探结果设为余额）喵。
- `GET /api/user/models` 在 `user-shared` 分组返回共享 `upstream/<name>` 喵。
- `GET /api/pricing` 追加共享模型条目（含共享剩余额度，供模型广场）喵。
- 直接调用入口：`/v1/chat/completions`、`/pg/chat/completions` 等现有 relay 路由，模型名 `upstream/<name>` 喵。

## 9. 前端

- **常规栏新增菜单**"上游模型"（`web/src/hooks/use-sidebar-data.ts` general 分组加 `Custom Upstream Models` → `/upstream-models`）喵。
- **页面** `web/src/features/upstream-models/`：列表 + 创建/编辑抽屉。布局参考 models 页，字段参考渠道页：模型名、显示名、base_url、api_key（不回显）、真实模型名、认证方式、**定价配置（每种请求类型独立价格，单位 RMB 元/每百万 token：输入/输出/缓存命中/缓存写入（含 5m、1h）/图片输入/音频输入/音频输出，默认全部 1 元）**、余额（含"一键同步嗅探"按钮）、使用上限、剩余API额度（嗅探/手动）、嗅探开关与路径、共享开关、共享额度、广场展示开关、启用开关喵。
- **候选链编辑器**：custom 候选改为从用户上游模型下拉选择，选中后隐藏 base_url/api_key 输入（显示条目摘要），保留 timeout/retry/启用喵。
- **模型广场**（`web/src/features/pricing/`）：共享模型卡片展示"共享剩余额度"；所有者额外可见自己模型的余额/上限/剩余API额度（受 `ShowBalanceEnabled` 控制）喵。
- **使用日志**（`web/src/features/usage-logs/`）：类型筛选增加"自定上游"；该类型下可按分组筛选共享调用（group=user-shared）；日志详情展示自定义计费明细（`custom_cost_rmb`、token 明细）喵。

## 10. 分期实施计划（一期一交付）

> 状态：P1~P5 全部实现并完成冒烟验证（2026-08-27）喵~

### P1：用户上游模型基础 CRUD + 直接调用透传 ✅ 已实现
- 表 `user_upstream_models`、AutoMigrate、controller/router、owner 隔离、凭据加密存储（复用 credential_vault）喵。
- 前端菜单 + 列表 + 创建/编辑抽屉喵。
- 分发层 `upstream/` 识别 + 透传（**暂不计费**）喵。
- 测试：CRUD 边界、加密、透传、owner 隔离、前缀识别喵。

### P2：独立 RMB 计费系统 ✅ 已实现
- usage 解析（非流式 + 流式）、费用计算（decimal→分）、余额扣减/上限累计（悲观锁）、请求前硬检查拦截喵。
- 消费日志 `Other` 计费明细喵。
- 测试：四类 token 计费、余额不足钳制、上限拦截、无 usage 防御、并发扣费喵。

### P3：额度嗅探 + 一键同步 ✅ 已实现
- 默认 OpenAI billing 路径 + 自定义路径、USD→RMB 汇率、嗅探结果写 `UpstreamRemainingCents`、前端"同步为余额"按钮喵。
- 测试：嗅探成功/失败/自定义路径、同步幂等喵。

### P4：共享功能 ✅ 已实现
- `user-shared` 分组、共享开关与额度、`/api/user/models` 与 `/api/pricing` 共享条目、共享调用免费 + `ShareSpent` 累计 + 共享耗尽停止、自定上游类型日志（group=user-shared 区分共享）、模型广场额度展示（所有者/其他用户）喵。
- 测试：共享可见性、免费不扣费、共享日志、耗尽停止后自用不受影响喵。

### P5：候选链集成 ✅ 已实现
- `VirtualModelCustomCandidate.UpstreamModelID`、激活改用条目凭据、计费归属、候选链编辑器下拉选择喵。
- 测试：引用激活、凭据联动、计费归属、旧直填候选兼容喵。

## 11. 验证

- 每期：后端相关包 `go test`、前端 `bunx vitest run`、`bun run typecheck` 与 `bun run build` 喵。
- 冒烟：启动 exe，创建用户上游模型 → 直接调用 `upstream/<name>` → 日志出现 type=自定上游 的计费明细；设共享 → 其他用户 `user-shared` 分组可见并免费调用 → 共享调用日志归入"自定上游"类型且 group=user-shared；余额耗尽 → 请求返回额度不足喵。
- 回归：普通模型与虚拟模型 internal 候选计费不受影响（`go test ./...` 除已知 Windows flaky 外通过）喵。

### 冒烟验证实录（2026-08-27，mock 上游 127.0.0.1:19090 + SQLite smoke.db）

- **P3 嗅探**：`POST /api/upstream-models/1/balance-check` → `upstream_remaining_cents=65700`（(120-30)USD × 7.3 汇率 × 100）喵；`balance/sync` → `balance_cents=65700` 喵。
- **P4 共享**：user2 在 `GET /api/user/models?group=user-shared` 看到 `upstream/smoke` 喵；共享调用成功且免费（owner balance 不变、total_spent 不变、share_spent +150）喵；日志 type=8、group=`user-shared`、quota=0、`is_shared_call=true` 喵；`share_spent` 达 `share_limit` 后共享调用返回 404（模型停止共享），owner 自用仍 200 且照常扣费喵。
- **P5 候选链**：虚拟模型 custom 候选设 `upstream_model_id=1` 后持久化成功喵；绑定 token 后调用 `virtual/smoke-vm` 返回 200，费用归属用户上游模型（balance 扣款、total_spent 累计）喵；日志 type=8、group=`default` 喵。
- 清理：冒烟结束后停止 mock/exe 后台进程并删除 `smoke.db` 喵。

## 12. 待确认的次要细节（已给推荐值，实现前如主人有异议再改）

1. 直接调用前缀用 **`upstream/<name>`**（与 `virtual/` 平行）喵。
2. 定价采用**每类型直接价格**（输入/输出/缓存/图片/音频等各自独立，单位 RMB 元/每百万 token，默认全 1），按请求固定价（UsePrice）暂不支持喵。
3. RMB 金额内部以**分**（int64）存储，前端展示元喵。
4. 嗅探 USD→RMB 汇率**默认固定值**（可在设置里改），嗅探失败不自动禁用喵。

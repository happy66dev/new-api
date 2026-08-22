# 虚拟模型候选级 RelayInfo 与计费隔离实现规格

## 1. 目标

本规格只解决虚拟模型候选切换时的 `RelayInfo`、计费会话、预扣、退款、usage 结算和最终错误收尾隔离问题喵。

目标是在不改变普通模型和 Token.AutoRoutes 行为的前提下，让一次虚拟模型请求中的每个 internal 候选都拥有独立的候选尝试上下文，并让候选失败、候选切换和最终响应的计费语义可验证、可追踪、可幂等喵。

本规格不负责重新设计 Channel 选择、custom 上游协议透传、失败规则 UI、共享虚拟模型或虚拟模型固定价格喵。

## 2. 当前问题

当前 `controller.Relay` 创建一个外层 `relayInfo`，候选切换时通过清空部分字段并重新执行定价和预扣来复用它喵。

这种方式仍可能残留或混用以下候选相关状态喵：

- `OriginModelName`、`TokenGroup`、`UsingGroup` 和 `UpstreamModelName` 喵。
- `ChannelMeta`、请求转换链、协议适配缓存和 Channel 相关字段喵。
- `PriceData`、`TieredBillingSnapshot`、`BillingRequestInput` 和预估 token 喵。
- `Billing`、`FinalPreConsumedQuota`、订阅预扣和 `SubscriptionPostDelta` 喵。
- `RetryIndex`、`LastError`、发送/接收计数和流式响应状态喵。
- `RequestId`、日志关联字段和最终 `RelayInfo` 所代表的候选身份喵。

当前外层 defer 还同时承担最终错误响应、退款和 violation fee，候选中间失败与最终请求失败的语义容易互相污染喵。

## 3. 不变约束

- 普通模型请求继续使用现有单一 `RelayInfo` 生命周期，行为不改变喵。
- Token.AutoRoutes 继续由 new-api 原生路径处理，不进入虚拟模型候选级隔离逻辑喵。
- internal 候选的 Channel 选择、原生 Channel retry、自动禁用、协议适配、usage 解析和原生日志继续复用现有 relay 喵。
- custom 候选不创建 new-api 额度预扣或消费结算喵。
- 候选已经发送有效业务字节后不得切换、重放、循环或追加错误响应喵。
- 每个候选的资金操作必须幂等，重复退款或结算不能重复改变余额喵。
- 任何候选快照只使用请求开始时的配置，不在候选切换期间重新读取控制面规则或候选配置喵。

## 4. 核心概念

### 4.1 VirtualRequestExecution

一次 `virtual/<name>` 请求对应一个虚拟请求执行上下文，只保存跨候选共享且不属于某一候选的状态喵。

```go
type VirtualRequestExecution struct {
    VirtualRequestID       string
    BaseRequestID          string
    OwnerUserID            int
    RelayFormat            types.RelayFormat
    RequestSnapshot        *VirtualModelRequestSnapshot
    CandidateSnapshot      *model.VirtualModelExecutionSnapshot
    RequestDeadline        time.Time
    LoopEnabled            bool
    MaximumLoopRounds      int
    LoopRoundsCompleted    int
    CurrentCandidateIndex  int
    ResponseStarted        bool
    BusinessBytesSent      bool
    FinalState              VirtualRequestFinalState
}
```

该结构不得保存当前 internal 候选的 `Billing`、`PriceData`、`ChannelMeta` 或 `LastError` 喵。

### 4.2 CandidateAttempt

每次候选实际开始执行时创建一个独立的 `CandidateAttempt` 喵。

```go
type CandidateAttempt struct {
    CandidateAttemptID     string
    VirtualRequestID       string
    CandidateID            int
    CandidateIndex         int
    SourceType             model.VirtualModelSourceType
    RealModelName          string
    GroupName              string
    AttemptNumber          int
    StartedAt              time.Time
    FinishedAt             time.Time
    ResponseStarted        bool
    BusinessBytesSent      bool
    Result                 CandidateAttemptResult
    Failure                *VirtualModelCandidateFailure
    RelayInfo              *relaycommon.RelayInfo
    Billing                relaycommon.BillingSettler
}
```

`CandidateAttempt` 是唯一允许持有候选级 `RelayInfo` 和 `Billing` 的对象喵。

### 4.3 CandidateBillingContext

为避免底层 billing 依赖外层 `RelayInfo`，新增候选级计费上下文或等价内部结构喵。

```go
type CandidateBillingContext struct {
    VirtualRequestID       string
    CandidateAttemptID     string
    CandidateID            int
    RequestID              string
    OwnerUserID            int
    TokenID                int
    TokenKey               string
    IsPlayground           bool
    Source                 string
    PreConsumedQuota       int
    ActualQuota            int
    State                  BillingAttemptState
}
```

如果短期无法完全移除 `BillingSession.relayInfo`，必须让它绑定候选专属 `RelayInfo`，且该 `RelayInfo` 在候选终态后不再被后备候选复用喵。

## 5. RelayInfo 创建策略

### 5.1 基础模板

入口仍可创建一个请求级基础模板，用于保存用户、请求格式、Token、原始请求和计费偏好等跨候选字段喵。

基础模板不得直接执行上游调用、预扣费或结算，也不得被传入两个不同候选的 relay helper 喵。

### 5.2 候选 RelayInfo 工厂

新增候选级工厂，按候选快照和基础模板创建全新的 `RelayInfo` 喵。

工厂必须完成以下步骤喵：

1. 复制请求级不可变字段，例如 `TokenId`、`TokenKey`、`UserId`、`UserSetting`、`RelayFormat`、请求 headers、请求路径和原始请求对象引用喵。
2. 设置当前候选的 `OriginModelName`、固定 `TokenGroup`、`UsingGroup` 和候选专属 `RequestId` 喵。
3. 清空 `ChannelMeta`、`TaskRelayInfo`、`StreamStatus`、`ClaudeConvertInfo`、响应计数、`RetryIndex`、`LastError` 和协议转换缓存喵。
4. 将候选真实模型写入候选级请求副本，禁止共享上一个候选已经改写过的请求对象喵。
5. 为当前候选创建新的 `CandidateAttemptID`，并将其写入 billing 幂等键和结构化日志上下文喵。
6. 根据当前候选模型和分组重新执行 `ModelPriceHelper`、tiered billing 准备和预扣喵。
7. 工厂任一步骤失败时返回候选级基础设施错误，并保证已建立的预扣完成同步回滚喵。

### 5.3 不得复制的字段

以下字段不得从前一个候选的 `RelayInfo` 复制喵：

- `ChannelMeta` 和任何上游 API Key、Channel Base URL 或 Channel 选择结果喵。
- `Billing`、预扣额度、订阅预扣、结算状态和退款状态喵。
- `PriceData`、tiered snapshot 和与上一候选模型相关的计费输入喵。
- `RetryIndex`、`LastError`、响应计数、流式状态和最终请求转换链喵。
- `UpstreamModelName`、Channel 认证信息和模型映射结果喵。

## 6. Relay 控制流

### 6.1 普通请求路径

非虚拟模型请求继续使用当前 `Relay` 控制流，不能为了虚拟模型改造而改变普通请求的 defer 顺序、重试规则、计费和错误响应喵。

### 6.2 虚拟请求路径

识别到虚拟模型执行状态后，`Relay` 只负责协议入口和最终响应收尾；候选级循环由虚拟模型执行器控制喵。

建议控制流如下喵：

```text
创建 VirtualRequestExecution
  -> 选择下一个候选
  -> 创建独立 CandidateAttempt
  -> 创建候选 RelayInfo
  -> 计算候选价格并创建候选 Billing
  -> 执行 native Channel retry 或 custom adapter
  -> 候选成功：结算当前候选并结束
  -> 放流前失败：结算/退款当前候选，交给失败规则决策
  -> next/freeze：当前候选进入终态，创建下一个 CandidateAttempt
  -> 候选链耗尽：判断循环或等待冻结
  -> 最终失败：统一收尾、写一次错误响应并结束
```

`controller.Relay` 不得通过递归调用自身启动后备候选喵。

### 6.3 候选切换顺序

候选切换必须严格按以下顺序执行喵：

1. 确认当前候选没有发送有效业务字节喵。
2. 停止当前候选的上游读取和 retry timer 喵。
3. 完成当前候选的 usage 解析和候选级结算；没有可结算 usage 时同步退款预扣喵。
4. 写入候选失败、切换原因和当前候选终态事件喵。
5. 关闭当前候选的 response body、连接和临时凭据引用喵。
6. 更新虚拟请求候选索引和循环状态喵。
7. 创建新的 `CandidateAttempt`、候选 `RelayInfo` 和候选 `Billing` 喵。
8. 从原始请求体恢复新的请求副本，仅替换新候选顶层 `model` 喵。
9. 开始后备候选执行喵。

任何步骤失败都必须停止继续创建后备候选，并返回 `virtual_model_unavailable` 或计费基础设施错误喵。

## 7. 计费生命周期

### 7.1 候选开始

候选开始时为当前 internal 候选计算价格，并以 `CandidateAttemptID` 生成幂等 `RequestID` 喵。

候选预扣成功后，预扣状态只写入当前候选的 `BillingSession` 和 `CandidateAttempt`，不得写入虚拟请求级共享计费字段喵。

免费模型不得创建虚假预扣会话，但仍需创建候选尝试记录以便关联 usage 和最终结果喵。

### 7.2 候选成功

候选成功后只结算当前候选的 billing，会话进入 `settled` 终态喵。

结算成功后才能写入候选成功事件和虚拟请求成功事件；结算失败必须记录高优先级计费错误，并禁止继续执行后备候选喵。

### 7.3 候选失败

候选在放流前失败时，先尝试解析实际 usage 喵。

- 有 usage：按实际 usage 结算当前候选喵。
- 无 usage 且只发生预扣：同步退款当前候选喵。
- 计费状态未知：停止候选链，禁止创建后备 internal 候选喵。
- custom 候选：不调用 new-api billing，仍写入候选结果事件喵。

当前候选的失败结算或退款完成后，才能执行 `next`、`freeze` 或循环喵。

### 7.4 最终失败

最终失败只写一次客户端错误响应，只对仍处于 `pending` 或 `reserved` 的当前候选执行收尾喵。

已经 `settled` 或 `refunded` 的候选不得被外层 defer 再次处理喵。

最终失败不得默认触发虚拟模型级 violation fee；只有该候选的原生调用满足既有 violation fee 判定且该费用属于真实内部调用时，才允许调用原生收费逻辑喵。

## 8. BillingSession 改造要求

`BillingSession` 必须增加明确的状态枚举，而不能仅依赖多个布尔字段隐式组合喵。

建议状态如下喵：

```go
type BillingAttemptState string

const (
    BillingAttemptPending    BillingAttemptState = "pending"
    BillingAttemptReserved   BillingAttemptState = "reserved"
    BillingAttemptSettled    BillingAttemptState = "settled"
    BillingAttemptRefunded   BillingAttemptState = "refunded"
    BillingAttemptFailed     BillingAttemptState = "failed"
)
```

状态迁移必须单向且幂等喵：

```text
pending -> reserved -> settled
pending -> failed
reserved -> refunded
reserved -> failed
```

禁止 `settled -> refunded`、`refunded -> reserved` 和 `failed -> reserved` 喵。

`RefundImmediately` 必须保证资金来源退款、订阅额外预扣回滚和 Token quota 回滚的状态一致性；部分步骤失败时必须保留可重试的内部状态和高优先级日志，不能提前标记为完全 refunded 喵。

`Refund` 异步接口只能用于请求最终结束后的非虚拟兼容路径，虚拟候选切换必须使用可报告错误的同步收尾接口喵。

建议新增候选级接口喵：

```go
type CandidateBillingLifecycle interface {
    Reserve(targetQuota int) error
    Settle(actualQuota int) error
    RefundImmediately(ctx context.Context) error
    FinalizeFailure(ctx context.Context, actualQuota *int) error
    State() BillingAttemptState
    AttemptID() string
}
```

## 9. Violation Fee 语义

将 violation fee 判定从“只要 `newAPIError != nil` 就执行”改为明确的候选级判定输入喵。

```go
type ViolationFeeContext struct {
    IsVirtualModel          bool
    CandidateAttemptID      string
    CandidateFailure        *VirtualModelCandidateFailure
    ResponseStarted         bool
    BusinessBytesSent       bool
    BillingSettled          bool
    NativeViolationEligible bool
}
```

虚拟候选中间失败默认 `NativeViolationEligible=false`，不允许外层 defer 自动收费喵。

只有以下条件全部满足时才允许执行既有 violation fee 逻辑喵：

- 当前候选确实是 internal native 调用喵。
- 原生 relay 明确标记该错误符合既有 violation fee 条件喵。
- 该费用尚未结算或记录喵。
- 当前请求并非仅因候选切换而失败喵。
- 没有已经向客户端提交有效流式业务字节后由连接中断产生的伪违规错误喵。

所有候选切换和最终费用决定必须带 `CandidateAttemptID`，避免用外层请求错误覆盖候选级费用语义喵。

## 10. 错误处理与响应边界

候选级错误必须包含候选 ID、尝试 ID、错误分类、HTTP 状态、是否可重试、是否放流和是否已结算，但对客户端只返回协议允许的受控错误喵。

如果当前候选已写出有效业务字节，外层错误 defer 必须检测 `ResponseStarted` 或 `BusinessBytesSent`，不得再次调用 `c.JSON`、`helper.WssError` 或候选 handoff 喵。

如果 custom 候选已提交完整错误响应，controller 必须清空待处理的 `newAPIError`，防止外层 defer 追加第二个错误正文喵。

如果候选切换前退款失败，系统必须返回计费基础设施错误并停止候选链；不得在旧额度未确认释放时继续预扣新候选喵。

## 11. 日志与关联 ID

每个虚拟请求生成一个 `VirtualRequestID`，每个候选尝试生成一个 `CandidateAttemptID`，每个候选 internal billing 使用独立的幂等 RequestID 喵。

至少记录以下结构化事件喵：

- `candidate_attempt_started` 喵。
- `candidate_billing_reserved` 喵。
- `candidate_attempt_failed` 喵。
- `candidate_billing_refunded` 或 `candidate_billing_settled` 喵。
- `candidate_switched` 喵。
- `virtual_request_finished` 喵。

事件只记录候选 ID、来源、真实模型摘要、分组摘要、错误分类、状态码、耗时、usage 摘要、计费状态和关联 ID，禁止记录完整请求体、完整 URL、API Key 或完整上游正文喵。

## 12. 测试规格

### 12.1 BillingSession 单元测试

必须覆盖以下场景喵：

1. 预扣成功后同步退款只执行一次喵。
2. 资金来源退款成功但 Token quota 回滚失败时不会错误标记完全 refunded 喵。
3. 订阅预扣和额外预扣都能在候选切换时完整回滚喵。
4. 成功结算后再次退款不会产生资金变化喵。
5. 已退款会话再次结算会安全拒绝或返回幂等成功，不能重新扣费喵。
6. 资金来源为空、relay info 为空、负数 quota、溢出 quota 和数据库错误都能安全处理喵。
7. 钱包与订阅两种 funding source 都使用相同的候选级状态机喵。

### 12.2 候选切换集成测试

必须使用两个 internal 候选验证以下场景喵：

1. 候选 A 预扣后失败，候选 A 退款，候选 B 重新预扣，候选 B 成功结算喵。
2. 候选 A 产生 usage 后失败，候选 A 按 usage 结算，候选 B 单独结算喵。
3. 候选 A 退款失败时不创建候选 B 喵。
4. 候选 A 的 `ChannelMeta`、价格、retry index 和 LastError 不会出现在候选 B 喵。
5. 候选 A 的 billing ID 与候选 B 不相同，日志可分别检索喵。
6. 候选链最终失败时只写一次错误响应和一次最终虚拟请求事件喵。
7. native Channel retry 全部耗尽后才发生候选级 handoff 喵。
8. 普通模型和 Token.AutoRoutes 的既有 retry、扣费和错误响应不发生变化喵。

### 12.3 流式与响应边界测试

必须验证候选 A 在放流前失败可以切换候选 B，候选 A 放流后失败不能切换候选 B 喵。

必须验证已写 HTTP 头但未写有效业务字节时，不会追加第二个 JSON 错误或重放请求喵。

必须验证 custom 候选已提交错误 passthrough 后，外层 controller 不再写第二份错误喵。

### 12.4 性能和并发测试

必须验证同一虚拟请求最多只有一个活动候选 billing 和一个活动上游连接喵。

必须并发执行多个虚拟请求，确认它们的候选 billing、RequestID、退款和结算不会互相覆盖喵。

必须并发触发同一候选失败和后备切换，确认不会重复退款或重复预扣喵。

## 13. 实施顺序

第一步，新增候选级 `CandidateAttempt`、`VirtualRequestExecution` 和 billing 状态枚举，但先通过兼容层接入现有 `RelayInfo` 喵。

第二步，将候选切换从“清空同一个 `relayInfo`”改为“结束旧候选并创建新的候选 `RelayInfo`”喵。

第三步，将候选级预扣、退款、usage 结算和 RequestID 改为绑定 `CandidateAttemptID` 喵。

第四步，重构外层 defer，使候选中间失败不触发最终错误响应和 violation fee，只有虚拟请求最终失败才执行一次最终收尾喵。

第五步，补充 BillingSession、候选切换、普通 relay 回归和流式边界测试喵。

第六步，依赖可用时执行 Go 单元测试、controller/middleware 集成测试、race 检查和普通模型冒烟；依赖不可用时必须记录阻塞原因，不得宣称通过喵。

## 14. 完成定义

满足以下条件才可关闭本规格喵：

- 两个连续 internal 候选拥有完全独立的 `RelayInfo`、billing、RequestID、usage 和结算状态喵。
- 任何候选切换前旧候选已完成可验证的同步退款或失败结算喵。
- 中间候选失败不会错误触发最终响应、重复退款或 violation fee 喵。
- 候选放流后严格禁止切换和追加错误喵。
- 普通模型和 Token.AutoRoutes 的行为回归测试通过喵。
- 计费、候选状态和请求关联 ID 测试覆盖正常、空值、非法值、并发和基础设施失败场景喵。
- Go 格式化、静态检查和可用的单元/集成测试均已执行，并如实记录依赖阻塞项喵。

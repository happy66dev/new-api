# 用户私有虚拟模型剩余工作实现规格

## 1. 文档目的

本文将 `2026-08-20-user-private-virtual-model-design.md` 中尚未完全落地的部分拆解为可执行的实现规格、接口边界、测试验收标准和上线顺序喵。

本文不改变已经确认的产品决策，不引入共享虚拟模型、管理员代管、Token AutoRoutes 改造、跨协议主动转换或虚拟模型固定价格计费喵。

本文只描述收尾阶段需要补齐的能力，并将“已经存在但仍需加固”的实现视为待验证能力，不能仅凭代码存在就标记为完成喵。

## 2. 当前基线与剩余范围

当前基线已经包含独立的用户私有虚拟模型实体、用户控制面、候选链、内部候选、自定义候选、凭据加密、基础冻结、失败规则后端和失败规则前端编辑器喵。

剩余工作按以下八个工作包实施喵。

1. 候选级执行上下文和计费生命周期隔离喵。
2. 结构化失败结果和统一候选决策喵。
3. 循环、冻结等待、超时和取消的完整运行时语义喵。
4. 流式预提交探测和不可逆响应边界验证喵。
5. 运行日志、审计、状态接口和请求关联喵。
6. 控制面冲突、冻结、API Key 授权和 WebUI 完整体验喵。
7. 数据库、依赖、回归和安全测试喵。
8. 功能开关、灰度、回滚和发布验收喵。

## 3. 不变的产品约束

虚拟模型仍然是用户私有资源，所有资源查询和写入必须使用认证上下文中的 `owner_user_id`，不得信任客户端提交的所有者字段喵。

调用名称仍固定为 `virtual/<name>`，虚拟模型不注册到普通模型、Group、Ability、Channel.Models 或 Token.AutoRoutes 存储中喵。

内部候选仍由 new-api 原生 relay 负责 Channel 选择、协议适配、原生重试、自动禁用、usage 解析和内部计费，虚拟模型层只负责候选级编排喵。

自定义候选仍只替换上游地址、出站认证和顶层 `model`，保留客户端原始 path、query、body、stream 和协议语义，并且不参与 new-api 额度预留或消费扣费喵。

已经向客户端写出有效业务字节后，任何候选都不得切换、循环、重放或追加 JSON 错误，后续异常只能结束当前响应并记录结果喵。

## 4. 候选级执行上下文

### 4.1 目标

每次候选尝试必须拥有独立的执行上下文，不能把前一个 internal 候选的 `RelayInfo`、定价、Channel、重试计数、响应统计或结算状态隐式带入后一个候选喵。

候选级执行上下文至少包含以下信息喵。

- `virtual_request_id`：一次虚拟模型请求的稳定关联 ID 喵。
- `candidate_attempt_id`：一次候选尝试的唯一 ID 喵。
- 虚拟模型 ID、候选 ID、候选序号和来源类型喵。
- 候选快照中的真实模型、分组、超时、最大重试次数和失败规则版本喵。
- 当前候选独立的 `RelayInfo` 或等价的候选级 relay 状态喵。
- 当前候选独立的计费会话、预扣额度、usage 和结算状态喵。
- 是否已经提交 HTTP 头、有效业务字节和最终响应喵。
- 候选开始时间、结束时间、切换原因和最终结果喵。

### 4.2 生命周期

候选级执行器在开始候选前创建新的执行上下文，并根据候选的真实模型和固定分组重新计算 Channel、价格、tiered billing、请求转换链和预扣额度喵。

internal 候选的每次真实上游调用都必须创建或重置独立的候选级计费会话，不能继承前一个候选的 `FinalPreConsumedQuota`、`Billing`、`PriceData`、`ChannelMeta`、`RetryIndex` 或 `LastError` 喵。

当前候选最终失败且需要继续后备候选时，必须先同步完成当前候选的预扣释放或失败结算，再创建后备候选的计费会话喵。

当前候选已经产生有效 usage 时，失败后仍按真实上游模型和实际 usage 独立结算；没有 usage 且未产生应收费调用时不得错误扣费喵。

所有候选尝试结束后，外层请求只能对仍未结算的当前候选执行一次最终收尾，不能因为候选切换重复退款、重复结算或重复收取 violation fee 喵。

### 4.3 最终费用语义

候选链中间失败不能自动触发虚拟模型级 violation fee，除非 new-api 原生计费明确判定该次实际内部调用符合原有违规收费条件喵。

后备候选成功时，之前失败的 internal 候选只结算其真实产生的 usage，后备候选另行结算；自定义候选不产生 new-api 消费记录喵。

最终所有候选失败时，每个 internal 候选分别完成失败结算或退款，外层请求返回最后一个符合规则的错误摘要；不得把多个候选错误拼接成可能泄露上游内容的响应喵。

计费会话必须具备幂等收尾状态，重复调用退款、失败结算或成功结算不会重复改变余额或额度喵。

## 5. 结构化失败与候选决策

### 5.1 统一失败结构

internal relay、自定义 HTTP 客户端、流式探测器和请求取消路径必须输出同一种候选级失败结构喵。

```go
type VirtualModelCandidateFailure struct {
    HTTPStatus          int
    ErrorClass          string
    BodyPreview         string
    RetryAfterSeconds   int
    NetworkError        bool
    Timeout             bool
    ResponseStarted     bool
    BusinessBytesSent   bool
    Retryable           bool
    Source               string
}
```

`BodyPreview` 只允许保留受限长度的脱敏摘要，用于规则匹配，不得直接作为客户端响应、审计正文或普通日志正文喵。

`ResponseStarted` 表示 HTTP 响应头是否已经写出，`BusinessBytesSent` 表示是否已经写出有效业务字节，二者必须由执行器真实记录而不能根据错误类型猜测喵。

`Source` 只能使用有限枚举，例如 `native_relay`、`custom_upstream`、`stream_probe`、`credential`、`timeout` 和 `cancelled`，不得写入完整异常堆栈或凭据内容喵。

### 5.2 native relay 转换

controller 在 native Channel 重试全部耗尽后，必须将最终 `NewAPIError`、上游状态、有限错误体和响应状态转换为统一失败结构，再交给虚拟模型决策器喵。

middleware 不得根据一段未校验的错误字符串重复推断动作；状态码、错误分类、响应摘要、Retry-After、响应提交状态和请求可重放性必须作为结构化字段传递喵。

如果 native relay 已经向客户端写出有效业务字节，controller 必须直接结束当前流或响应，不得进入候选 handoff 喵。

### 5.3 custom 失败转换

custom 候选的凭据缺失、主密钥不可用、解密失败、URL 校验失败、DNS 失败、建连失败、请求体处理失败、读取超时、空流和流内 error 都必须转换为统一失败结构喵。

在未提交有效业务字节前，结构化失败可以进入规则决策器；已提交有效业务字节后，只能记录失败并结束当前流喵。

所有失败转换必须保留原始错误的安全分类，但不能向控制面、客户端或日志泄露完整 URL、query 参数中的凭据、API Key、请求体或完整上游错误正文喵。

### 5.4 决策器边界

决策器只接收候选快照、候选失败结构、当前尝试次数、剩余总预算和候选状态，输出动作及下一步所需的安全参数喵。

支持动作仍为 `retry`、`next`、`freeze` 和 `passthrough` 喵。

`retry` 只能在候选未达到最大尝试次数、请求未取消、总预算未耗尽且尚未写出有效业务字节时执行喵。

`freeze` 必须先按自定义候选身份写入自动冻结，再尝试下一个候选；internal 候选不得由虚拟模型层建立自定义自动冻结喵。

无规则命中时继续使用默认 `next`，但当候选已经不可重放或已经写出有效业务字节时，决策器必须返回终态，不能强行切换喵。

## 6. 循环、冻结与请求预算

### 6.1 请求预算

请求开始时计算单调时钟意义上的总 deadline，并将其传递给所有候选、退避、冻结等待和上游读取操作喵。

候选级 timeout、custom retry backoff、SSE 静默 timeout 和冻结等待时间都必须夹紧到剩余总预算，不能因为局部 timeout 超过总 timeout 而继续执行喵。

请求取消、连接断开、总 timeout、最大循环轮数和没有可用候选都必须产生稳定的终态错误分类喵。

### 6.2 循环轮次

循环启用时，每一轮都从稳定顺序中的第一个候选开始，并跳过停用、手动冻结、自动冻结、已达到本轮不可重试状态的候选喵。

一轮候选全部失败后，只有在未放出有效业务字节、仍有剩余总预算且未超过最大轮数时才允许重新开始下一轮喵。

最大轮数必须同时受模型配置和不可关闭的系统硬上限约束，超过上限时返回最终错误并记录 `loop_limit_reached` 喵。

### 6.3 自动冻结等待

当所有候选暂时不可用时，只允许等待将在剩余总预算内自动到期的自动冻结，等待时间取最近一个有效到期时间与当前时间的差值喵。

等待期间必须响应客户端取消和总 deadline，不能使用不可取消的睡眠或阻塞数据库查询喵。

手动冻结和停用候选不得被循环自动解除或等待其到期；如果没有可等待的自动冻结，立即返回最终错误喵。

自动冻结到期清理必须使用候选身份、旧 `updated_time` 或等价版本条件执行 compare-and-clear，避免成功请求误删并发失败请求刚写入的新冻结喵。

## 7. 流式预提交与不可逆边界

### 7.1 探测器接口

流式探测器必须消费同一个上游响应迭代器，先在有限缓冲区内识别首个有效业务事件，再将已经读取的缓冲内容回放给正式响应转发器喵。

探测器不能重新读取已经消费的 response body，也不能为了回放再次发起上游请求喵。

缓冲区、静默 timeout、总探测预算和单次读取大小都必须有硬上限；超过上限时按结构化 timeout 或 upstream error 处理喵。

### 7.2 事件识别

OpenAI SSE 和 Anthropic SSE 必须分别识别有效文本、工具调用、thinking/reasoning、usage、finish reason、心跳、空流、显式 error 和 HTTP 成功状态内嵌错误喵。

心跳本身不能被当作有效业务字节，也不能阻止静默 timeout 的正确计算喵。

只有能证明客户端可以继续获得合法协议语义的有效业务事件才算完成预提交；首个事件是 error、空流结束或连接失败时仍允许候选切换喵。

### 7.3 放流后的行为

一旦客户端收到任何有效业务字节，执行器必须设置不可逆标记喵。

不可逆标记设置后，所有后续读取错误只能结束当前响应、记录候选结果并完成当前候选结算，不得调用下一候选、循环、重试请求或追加 JSON 错误喵。

流式响应的日志必须记录是否发生预提交、何时设置不可逆标记、已发送字节数量摘要和最终结束原因，但不得记录完整响应内容喵。

## 8. 冻结、状态与控制面

### 8.1 冻结状态响应

候选详情和状态接口必须区分候选启用状态、手动冻结、自动冻结和运行中暂不可用状态喵。

冻结摘要至少包含来源、是否当前生效、开始时间、到期时间、最近失败分类和安全的连续失败次数喵。

自动冻结的 Base URL、API Key 指纹和身份摘要只能以不可逆摘要或稳定短 ID 展示，不能展示原始凭据或完整 URL 喵。

手动解冻必须具备幂等语义，目标候选不存在、已删除或当前没有冻结时不得破坏其他候选状态喵。

### 8.2 版本冲突

所有模型、候选链、规则、授权和冻结写入均使用模型级版本 CAS 喵。

收到 `409 virtual_model_version_conflict` 时，前端必须保留本地草稿，重新读取服务端详情，并明确提示用户哪些内容已经变化；不得静默覆盖服务端新版本喵。

冲突恢复至少提供重新加载、放弃本地修改和继续编辑三种明确结果；第一期可以不提供字段级自动合并，但不能丢失本地草稿喵。

### 8.3 API Key 授权

API Key 选择器必须支持服务端搜索和分页，不得依赖固定前 100 条列表喵。

列表中必须明确区分可用、禁用、过期、耗尽、无权限和已绑定状态；不可用 Key 默认不能被新绑定，但已存在绑定的状态必须可解释喵。

API Key 编辑页和虚拟模型授权页必须操作同一绑定关系，任一处保存后另一处读取到的结果必须一致喵。

## 9. 可观测性与审计

### 9.1 ID 体系

每次入口请求生成或继承 `request_id`，并生成独立的 `virtual_request_id`；每次候选尝试生成 `candidate_attempt_id` 喵。

所有 internal 消费记录、custom 上游结果、候选切换、冻结写入、最终响应和控制面审计事件都必须携带可检索的关联 ID 喵。

ID 不得包含 API Key、Base URL、请求体或完整模型凭据，长度和字符集必须受限喵。

### 9.2 结构化运行事件

至少记录以下事件类型喵。

- `virtual_request_started` 喵。
- `candidate_attempt_started` 喵。
- `candidate_attempt_failed` 喵。
- `candidate_switched` 喵。
- `candidate_auto_frozen` 喵。
- `candidate_manual_frozen` 和 `candidate_manual_unfrozen` 喵。
- `candidate_attempt_succeeded` 喵。
- `virtual_request_finished` 喵。
- `virtual_request_rejected` 喵。

事件字段只允许记录候选 ID、顺序、来源类型、脱敏模型或分组摘要、错误分类、HTTP 状态、耗时、重试次数、冻结秒数、是否放流和关联 ID 喵。

完整请求体、完整响应体、完整 URL、API Key、Authorization、Cookie、上游 query 凭据和完整错误正文禁止进入日志、审计、缓存和异步队列喵。

### 9.3 状态接口

`GET /api/virtual-models/:id/status` 必须明确说明它是运行摘要而不是实时健康检查喵。

状态响应至少返回模型启用状态、候选总数、启用数、当前冻结数、最近尝试时间、最近失败分类和最近最终结果摘要喵。

如果后端无法提供实时健康检查，字段名称必须使用 `enabled_candidates`、`frozen_candidates` 等明确语义，不得使用 `available_candidates` 暗示真实上游可用性喵。

## 10. WebUI 收尾范围

虚拟模型页面继续沿用当前 new-api 的 `SectionPageLayout` 和 Tabs 结构，不引入新的视觉系统喵。

候选链页面需要展示规则数量、冻结摘要和候选状态；失败规则页面已经具备基础编辑器，后续需要接入版本冲突恢复和服务端错误字段定位喵。

冻结界面需要显示来源、到期时间、失败原因、当前是否生效，并提供带确认和加载状态的解冻操作喵。

API Key 绑定界面需要增加搜索、分页、状态徽标、空结果和加载失败状态，并保留空选择解除全部授权的明确提示喵。

模型列表需要提供名称搜索、启用状态筛选、候选数量和冻结摘要；若后端暂时不提供分页，前端不得伪造分页信息喵。

移动端必须验证 Tabs 换行、候选表单滚动、规则编辑器字段不溢出、错误提示不遮挡保存按钮和图标按钮具备可访问名称喵。

## 11. 数据库与迁移

补充 SQLite、MySQL 和 PostgreSQL 的迁移验证，重点检查外键关联、唯一约束、索引、时间字段、加密字段长度和空值语义喵。

迁移必须可重复执行，已有虚拟模型数据不得因为重复迁移、服务重启或快速迁移路径而产生重复候选、重复规则或重复绑定喵。

删除模型时必须在事务内清理候选、规则、冻结、密文和绑定；审计元数据保留但不得保留可还原凭据喵。

迁移失败时新请求采用拒绝优先策略，普通模型、Token AutoRoutes 和已有非虚拟模型请求不应被虚拟模型迁移逻辑误伤喵。

## 12. 测试规格

### 12.1 单元测试

必须覆盖候选级计费会话创建、失败退款、成功结算、重复收尾、候选切换后的状态隔离和 violation fee 判定喵。

必须覆盖失败结构规范化、native error 转换、custom 网络/解密/凭据失败、规则首条命中、默认 `next`、retry 上限和 freeze TTL 喵。

必须覆盖循环最大轮数、总 deadline、候选跳过、自动冻结等待、compare-and-clear、客户端取消和等待期间取消喵。

必须覆盖流式探测器的空流、心跳、首事件错误、HTTP 200 内嵌 error、跨 chunk UTF-8、工具调用、usage、finish reason、同 iterator 回放和放流后禁止切换喵。

### 12.2 控制器与中间件集成测试

必须验证普通模型和 Token AutoRoutes 回归，且虚拟模型请求不会改变既有模型选择、计费和日志语义喵。

必须验证 API Key 认证后 owner 隔离、绑定撤销、`/v1/models` 过滤、模型停用、功能开关关闭和运行中快照继续执行喵。

必须验证 internal 候选原生 retry 耗尽后进入后备候选、自定义候选失败后进入后备候选、passthrough 终止和候选链耗尽终止喵。

必须验证多个 internal 候选分别产生独立 usage、预扣和结算记录，后备候选成功不会覆盖前一个候选的结算记录喵。

### 12.3 安全测试

必须验证私网、回环、链路本地、保留地址、多地址解析、DNS 重绑定、重定向、代理环境变量、危险端口和危险请求/响应头均被正确防护喵。

必须验证 API Key、Base URL、请求体、完整错误正文、密文和主密钥不会出现在前端响应、日志、审计、错误消息、缓存和测试快照喵。

必须验证主密钥缺失、密文篡改、解密失败和旧认证枚举兼容行为喵。

### 12.4 前端测试

必须覆盖 API client 路径、版本字段、失败规则顺序、规则边界校验、服务端错误、空规则、删除规则、冻结显示和 409 冲突提示喵。

必须覆盖 API Key 搜索分页、状态展示、绑定保存、空选择解绑、候选排序、候选删除确认和凭据不回显喵。

依赖可用后执行前端 typecheck、Vitest、lint、format、build 和页面加载冒烟测试；依赖不可用时必须记录命令、阻塞原因和替代验证喵。

## 13. 开关、灰度和回滚

新增或继续使用以下独立开关喵。

- 虚拟模型总开关喵。
- 自定义上游开关喵。
- 循环模式开关喵。
- 流式预提交探测开关喵。
- 虚拟模型详细运行日志开关喵。

默认先关闭数据面调用，只允许控制面保存和读取经过安全校验的配置喵。

开启总开关前必须通过 owner 隔离、绑定检查、不可变快照、普通模型回归、计费隔离、流式边界和 `/v1/models` 过滤测试喵。

灰度期间必须能按用户或部署实例限制开启范围，并在日志中记录功能开关版本喵。

关闭开关只拒绝关闭之后创建的新虚拟模型请求；已经完成鉴权并创建快照的请求继续遵守自身 deadline 和不可逆流式边界喵。

回滚不得物理删除配置、密文、绑定或审计数据；回滚动作必须记录操作者、开关前后值、原因和关联 request ID 喵。

## 14. 实施顺序

第一阶段先完成候选级 `RelayInfo` 和 billing 生命周期隔离，并补充单元测试，避免后续测试建立在错误计费语义之上喵。

第二阶段统一 native/custom/stream failure 结构和决策器输入，接入规则、冻结和 passthrough 的完整 handoff 喵。

第三阶段完成循环、冻结等待、deadline、取消传播和 compare-and-clear 集成测试喵。

第四阶段完成流式预提交探测器的协议测试和放流后不可切换验证喵。

第五阶段完善 request ID、candidate attempt ID、运行事件、审计和状态接口喵。

第六阶段完善 409 冲突恢复、冻结 UI、API Key 搜索分页、候选状态和移动端交互喵。

第七阶段完成迁移、安全、回归、前端和发布测试，最后执行灰度开启与回滚演练喵。

阶段之间允许继续小范围修复，但未通过上一阶段的核心验收时不得扩大数据面开关范围喵。

## 15. 完成定义

只有同时满足以下条件，剩余工作才可标记为完成喵。

1. 每次 internal 候选尝试拥有独立 relay、计费、usage、退款和结算生命周期喵。
2. native、custom 和流式错误都能转换为统一结构化失败，并且规则动作不会越过放流后的不可逆边界喵。
3. 循环、冻结等待、总超时、候选 timeout、retry 上限和客户端取消均有确定且经过测试的终态喵。
4. 流式探测使用同一 iterator，放流前可安全切换，放流后绝不切换、重放或追加协议外错误喵。
5. 每次虚拟请求和候选尝试都能通过关联 ID 追踪到内部消费、最终响应和审计事件，且没有敏感信息泄漏喵。
6. 冻结、API Key 授权、规则编辑、候选状态、版本冲突和移动端交互均具备可恢复的 WebUI 体验喵。
7. SQLite、MySQL、PostgreSQL 迁移以及普通模型和 Token AutoRoutes 回归均通过验证喵。
8. Go 单元/集成测试、custom HTTP 测试、SSE 测试、前端 typecheck、Vitest、lint、format、build 和页面冒烟测试均已执行，阻塞项已经明确记录喵。
9. 总开关、自定义上游、循环、流式探测和详细日志均完成灰度开启、关闭新请求和在途快照继续执行的回滚演练喵。

在完成定义满足前，发布状态只能写为“虚拟模型基础能力已实现，剩余规格正在验收”，不得写为完整交付喵。

## 16. 模块边界与实现接口

### 16.1 虚拟模型执行器

新增或整理独立的虚拟模型执行器边界，负责请求级快照、候选索引、循环轮次、冻结跳过、候选切换和终态决策喵。

执行器不得直接实现 new-api Channel 选择、协议转换或计费规则；这些能力通过明确的 native relay adapter 接口调用喵。

建议抽象以下能力，具体接口名称可以按代码库惯例调整喵。

```go
type VirtualModelCandidateExecutor interface {
    Execute(ctx context.Context, request *VirtualModelRequestSnapshot) (*VirtualModelExecutionResult, error)
}

type NativeCandidateRunner interface {
    Run(ctx context.Context, attempt *VirtualModelCandidateAttempt) (*VirtualModelCandidateResult, error)
}

type CustomCandidateRunner interface {
    Run(ctx context.Context, attempt *VirtualModelCandidateAttempt) (*VirtualModelCandidateResult, error)
}

type CandidateFailureDecider interface {
    Decide(attempt *VirtualModelCandidateAttempt, failure VirtualModelCandidateFailure) VirtualModelCandidateDecision
}
```

接口返回值必须区分“候选失败但可以继续”“已经提交客户端响应”“最终终止错误”和“内部基础设施错误”四类结果，不能只返回一个布尔值喵。

### 16.2 native relay adapter

native adapter 负责把当前候选的分组和真实模型装载到现有 relay 上下文，调用原生 Channel retry，并在原生 retry 完成后返回结构化结果喵。

adapter 不得递归调用公开的 `controller.Relay`，不得创建嵌套响应 defer、重复预扣费、重复最终错误写入或重复请求日志喵。

adapter 必须提供候选尝试开始、成功、失败和计费收尾的明确生命周期回调，供虚拟模型执行器写入 `candidate_attempt_id` 喵。

### 16.3 custom upstream adapter

custom adapter 只接收已经通过 SSRF 校验并从快照解密的短生命周期凭据，负责请求构造、认证头替换、顶层模型替换、有限探测、响应过滤和结果分类喵。

custom adapter 不得读取当前数据库中的最新候选配置、规则或冻结状态；所有决策输入必须来自请求开始时的不可变快照喵。

custom adapter 必须在返回结果前清理对 URL、API Key、Authorization、Cookie 和响应正文的临时引用；禁止把请求对象或完整响应缓存到异步任务喵。

### 16.4 billing adapter

billing adapter 负责候选级预扣、补扣、退款、成功结算和幂等收尾，虚拟模型执行器只能调用抽象接口，不得直接操作钱包、订阅或 Token quota 表喵。

候选级计费对象必须拥有独立的 request ID 和 candidate attempt ID；候选切换时旧对象进入终态，新候选创建新的对象喵。

如果底层计费接口当前只能绑定一个 `RelayInfo`，需要先提取候选级 billing context，再由兼容层将其映射到现有 relay 字段；兼容层不得把多个候选的状态混合存放喵。

## 17. 候选状态机

每次候选尝试必须遵循以下单向状态迁移，禁止从终态回到执行态喵。

```text
pending
  -> running
  -> retry_waiting
  -> running
  -> succeeded
  -> failed_before_response
  -> response_started
  -> response_finished
  -> cancelled
  -> timed_out
  -> infrastructure_failed
```

`failed_before_response` 才允许进入 `next`、`freeze` 或下一轮循环；`response_started` 之后只能进入 `response_finished`、`cancelled` 或 `timed_out`，不得再进入候选切换喵。

`retry_waiting` 必须同时绑定 retry 序号和截止时间，等待被取消、超过总 deadline 或达到候选最大重试次数时分别转为明确终态喵。

候选状态记录必须与虚拟请求状态分离；候选失败不等于虚拟请求失败，只有执行器确认没有后续候选或循环预算耗尽时，虚拟请求才进入最终失败喵。

虚拟请求状态至少包含 `accepted`、`running`、`succeeded`、`failed`、`cancelled`、`timed_out` 和 `rejected` 喵。

状态迁移必须具备幂等保护，重复收到上游结束、客户端断开或 controller defer 不得重复写终态事件、重复结算或重复退款喵。

## 18. 决策优先级与边界表

为避免规则、重试、冻结和循环之间出现歧义，执行器必须按以下优先级处理喵。

1. 客户端取消、连接关闭或总 deadline 到期优先于所有候选动作喵。
2. 已发送有效业务字节优先于失败规则，直接禁止切换和循环喵。
3. `passthrough` 优先于 `next`、`freeze` 和循环，但只能在响应尚未提交时写出受控错误喵。
4. `retry` 受候选最大重试次数和总预算约束，达到任一限制后降级为该候选的下一步动作喵。
5. `freeze` 先完成冻结写入；冻结写入失败时终止请求，不得假装冻结成功后继续循环喵。
6. `next` 推进到下一候选；没有下一候选时再判断循环，而不是在候选内部隐式重试喵。
7. 只有所有候选完成且循环条件满足时，才允许等待自动冻结或开始下一轮喵。

| 当前条件 | 允许动作 | 禁止动作 |
| --- | --- | --- |
| 尚未写响应，候选失败 | `retry`、`next`、`freeze`、`passthrough` | 忽略总 deadline |
| 已写 HTTP 头但无有效业务字节 | 仅按协议安全结束或受限失败处理 | 重放并追加第二个响应 |
| 已写有效业务字节 | 结束当前流并记录失败 | 切换、循环、重试请求、追加 JSON |
| 客户端已取消 | 取消当前尝试并收尾 | 新建后备上游连接 |
| 自动冻结写入失败 | 返回基础设施错误 | 继续执行同一身份 |
| 候选链耗尽且循环关闭 | 返回最终失败 | 隐式从首候选重启 |
| 候选链耗尽且循环开启 | 满足预算才重启或等待 | 无上限循环 |

## 19. API 契约补充

### 19.1 候选详情与规则响应

候选读取响应必须明确区分候选基础配置、规则列表和冻结摘要，不能把规则数量与完整规则正文混成可变结构喵。

建议响应结构如下，字段名称可以沿用现有 snake_case 约定喵。

```json
{
  "id": 42,
  "source_type": "custom",
  "stable_order": 1,
  "enabled": true,
  "max_retries": 2,
  "timeout_seconds": 60,
  "real_model_name": "example-model",
  "auth_style": "bearer",
  "credential_configured": true,
  "base_url_summary": "https://api.example.com/v1",
  "failure_rule_count": 2,
  "failure_rules": [],
  "freeze": {
    "manual_active": false,
    "automatic_active": true,
    "automatic_expires_at": 1790000000,
    "last_error_class": "rate_limited"
  }
}
```

`failure_rules` 可以在详情接口返回，在列表接口默认只返回 `failure_rule_count`；任何接口都不得返回自定义 API Key 或可用于重放的完整凭据喵。

### 19.2 规则替换

规则替换请求必须携带模型版本和候选 ID，规则数组顺序就是 `rule_order`，空数组表示清空该候选规则喵。

服务端必须先验证候选属于当前用户和当前模型，再在同一事务内执行版本 CAS、规则替换和审计写入；版本冲突时旧规则不得被删除喵。

规则保存失败时响应必须包含稳定错误码和字段级错误位置，例如 `rules[0].body_regex`，但不得返回正则编译器可能包含敏感输入的完整异常文本喵。

### 19.3 状态与运行摘要

状态接口不得声称执行了主动健康检查，除非服务端确实在请求范围内执行了受限探测并明确返回探测时间与结果喵。

建议增加以下只读字段喵。

- `enabled_candidates`：启用候选数量喵。
- `frozen_candidates`：当前冻结候选数量喵。
- `candidate_rule_count`：有规则候选数量喵。
- `last_request_at`：最近虚拟请求时间喵。
- `last_success_at`：最近成功时间喵。
- `last_failure_class`：最近失败分类喵。
- `last_candidate_id`：最近完成的候选 ID 喵。
- `observed_at`：状态摘要生成时间喵。

## 20. 失败与基础设施故障处理

数据库读取快照失败、主密钥缺失、冻结状态查询失败、计费服务不可用和审计写入失败必须区分处理喵。

安全相关前置依赖失败时，新虚拟模型请求采用拒绝优先策略，返回 `virtual_model_unavailable`，不得降级到普通模型或 Token AutoRoutes 喵。

候选级审计写入失败是否阻断请求必须固定为配置项默认值：核心安全审计写入失败阻断新请求，非关键运行采样失败只记录本地错误并允许业务继续喵。

计费收尾失败不能静默吞掉；若余额或订阅状态无法确认，必须写入高优先级系统日志和候选尝试失败事件，并阻止继续创建新的 internal 后备计费会话喵。

客户端错误、上游错误、配置错误、基础设施错误和取消错误必须使用不同的 `ErrorClass`，并且错误分类枚举需要在单元测试中锁定，避免不同 adapter 各自发明字符串喵。

## 21. 性能、资源与并发约束

每个虚拟请求最多同时存在一个活动候选尝试，候选切换前必须完成旧候选的连接关闭和计费收尾，禁止并行竞速多个上游候选喵。

规则匹配最多处理固定数量规则和固定大小响应摘要；正则编译结果可以按规则版本缓存，但缓存键不得包含明文凭据或完整请求内容喵。

自动冻结等待不能为每个请求创建长期独立 goroutine；应使用可取消的 timer 或统一等待机制，并在请求结束时释放 timer 喵。

原始请求体、SSE 预提交缓冲和错误摘要必须受硬大小限制；超过限制时立即返回结构化错误，不允许无限追加内存喵。

运行事件写入应采用有界缓冲或同步关键事件加异步非关键事件的方式，不能因为日志后端缓慢阻塞所有普通模型请求喵。

并发控制面写入必须依赖数据库版本 CAS，不得以进程内 mutex 作为跨实例一致性方案喵。

## 22. 代码变更清单

实现阶段至少需要检查以下模块边界，实际文件以仓库当前结构为准喵。

- `middleware/distributor.go`：请求快照、候选推进、循环、冻结等待和 custom handoff 喵。
- `controller/relay.go`：native relay 生命周期、候选级计费边界和最终 defer 喵。
- `relay/common/relay_info.go`：候选级 relay/billing context 的隔离承载结构喵。
- `service/billing_session.go`：候选级预扣、退款、结算和幂等终态喵。
- `service/virtualmodel/failure_rule.go`：失败结构规范化和规则决策喵。
- `service/virtualmodel/custom_candidate.go`：custom 请求透传、探测、错误转换和响应边界喵。
- `model/virtual_model.go`：冻结、状态、审计和快照查询喵。
- `controller/virtual_model.go`：状态响应、规则/冻结契约、审计和冲突错误喵。
- `router/api-router.go`：新增或调整用户侧资源路由喵。
- `web/src/features/virtual-models/api.ts`：API 类型、分页、状态和错误契约喵。
- `web/src/features/virtual-models/index.tsx` 及其组件：状态、冻结、授权、冲突和移动端体验喵。
- `web/src/i18n/locales/en.json` 与 `web/src/i18n/locales/zh.json`：只维护英文和简体中文文案喵。

任何涉及公共 relay 或计费的改动必须同时增加回归测试，证明普通模型和 Token AutoRoutes 行为未改变喵。

## 23. 分阶段交付门槛

### Gate A：计费与上下文隔离

必须通过候选级独立计费单元测试、连续两个 internal 候选的失败/成功结算测试、重复收尾测试和普通 relay 回归测试喵。

### Gate B：失败决策完整化

必须通过 native/custom 统一失败结构测试、四种规则动作测试、凭据/解密/网络故障 handoff 测试和敏感信息扫描喵。

### Gate C：循环与流式边界

必须通过总 timeout、最大轮数、自动冻结等待、客户端取消、SSE 预提交和放流后禁止切换测试喵。

### Gate D：控制面和可观测性

必须通过状态字段语义测试、审计关联 ID 测试、冻结 UI、API Key 搜索分页、409 草稿恢复和移动端冒烟测试喵。

### Gate E：发布与回滚

必须通过三种数据库迁移、普通模型回归、Token AutoRoutes 回归、功能关闭在途快照测试、密钥泄漏扫描和灰度回滚演练喵。

任何 Gate 未通过时，只能继续在开发或测试环境运行，不得扩大虚拟模型数据面灰度范围喵。

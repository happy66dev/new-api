package constant

// GroupUserShared 是硬编码的"用户共享分组"名，所有用户可用分组默认包含它喵。
// 用户共享模型全部免费，调用只累计共享消耗；共享额度耗尽后仅停止共享，所有者自用不受影响喵。
const GroupUserShared = "user-shared"

// GroupVirtual 是"虚拟模型分组"名，仅在分组管理（可用分组白名单）中出现喵。
// 它不是一个真实的渠道分组：虚拟模型走 virtual/ 模型命名空间，由 middleware 单独路由喵。
const GroupVirtual = "virtual"

// GroupUserUpstream 是"自定上游分组"名，仅在分组管理（可用分组白名单）中出现喵。
// 它同样不是真实渠道分组：自定上游走 user/ 模型命名空间，由 middleware 单独路由喵。
const GroupUserUpstream = "user-upstream"

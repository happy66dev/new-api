package common

import "strings"

// LoopGuardHeaderKey 回环检测标记头的名称喵。
// 本实例转发请求到上游时注入，回环请求重新进入本实例时由入口中间件检查喵。
const LoopGuardHeaderKey = "X-New-Api-Loop-Guard"

// BuildLoopGuardValue 构造回环检测标记值：格式为 实例ID:请求ID 喵。
// requestID 为空时自动生成一个新的请求 ID，保证标记格式恒定可解析喵。
func BuildLoopGuardValue(requestID string) string {
	if requestID == "" {
		requestID = NewRequestId()
	}
	return InstanceID() + ":" + requestID
}

// ParseLoopGuardInstanceID 从标记值解析出实例 ID 部分喵。
// 格式非法（分隔符缺失或位于首尾）时返回空串，表示该标记无法识别归属喵。
func ParseLoopGuardInstanceID(guardValue string) string {
	// 喵~防御：分隔符必须在中间且两侧非空，避免把整串或空串误判为实例 ID 喵。
	if colonIndex := strings.Index(guardValue, ":"); colonIndex > 0 {
		return guardValue[:colonIndex]
	}
	return ""
}

// IsLoopGuardFromSelf 判断标记值是否由本实例发出：实例 ID 与当前进程一致即为回环喵。
// 无标记值、格式非法或实例 ID 不匹配时返回 false，放行普通请求与多级代理链路喵。
func IsLoopGuardFromSelf(guardValue string) bool {
	return ParseLoopGuardInstanceID(guardValue) == InstanceID()
}

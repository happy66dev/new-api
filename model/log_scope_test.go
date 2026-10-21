package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSumUsedQuotaSharedScope 验证普通用户「全部」范围统计包含共享模型被调日志，且「仅自己」口径不变喵。
// 统计口径与日志列表 GetUserLogs 的 scope 分支保持一致喵。
func TestSumUsedQuotaSharedScope(t *testing.T) {
	truncateTables(t)
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)

	// 当前时间落在 rpm/tpm 的最近 60 秒窗口内，保证统计可复现喵。
	now := time.Now().Unix()

	// 自己的消费日志（type=1）：quota 100、token 30，应计入两个范围喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 10, CompletionTokens: 20, Quota: 100, CreatedAt: now}).Error)
	// 别人调用我的共享模型（type=8、user-shared 分组）：quota 200、token 70，只计入「全部」范围喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "other", Type: LogTypeCustomUpstream, ModelName: "user/shared-a", Group: constant.GroupUserShared, PromptTokens: 30, CompletionTokens: 40, Quota: 200, CreatedAt: now}).Error)
	// 别人的普通消费日志：不属于我，两个范围都不应计入喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "other", Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 1, CompletionTokens: 1, Quota: 999, CreatedAt: now}).Error)
	// 非共享模型被调日志（不在共享名集合内）：即使 type=8/user-shared 也不应计入喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "other2", Type: LogTypeCustomUpstream, ModelName: "user/not-shared", Group: constant.GroupUserShared, PromptTokens: 1, CompletionTokens: 1, Quota: 500, CreatedAt: now}).Error)

	// 「全部」范围：quota 仍含共享被调(300)；但 rpm/tpm 新口径只计自己的内部模型(gpt-4o)喵。
	statAll, err := SumUsedQuota(LogTypeConsume, 0, 0, "", "me", "", 0, "", "user/shared-a")
	require.NoError(t, err)
	assert.Equal(t, 300, statAll.Quota) // quota 口径不变：自己的消费 + 共享被调喵。
	assert.Equal(t, 1, statAll.Rpm)     // 共享被调(user/shared-a) 从 rpm 剔除，仅剩自己的 gpt-4o 一条喵。
	assert.Equal(t, 30, statAll.Tpm)    // tpm 仅自己 gpt-4o 的 10+20=30，不含共享被调的 70 喵。

	// 「仅自己」范围（不传共享名）：只统计自己的消费，quota 100 喵。
	statSelf, err := SumUsedQuota(LogTypeConsume, 0, 0, "", "me", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 100, statSelf.Quota)
}

// TestSumUsedQuotaRpmTpmInternalModelScope 验证 rpm/tpm 新口径：计入虚拟模型内部候选(type=9 无前缀)，
// 排除用户自定义上游(user/)与虚拟模型自定义候选(virtual/)，且 quota 口径不受影响喵。
func TestSumUsedQuotaRpmTpmInternalModelScope(t *testing.T) {
	truncateTables(t)
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)

	// 当前时间，落在 rpm/tpm 的最近 60 秒窗口内喵。
	now := time.Now().Unix()
	// 60 秒窗口外的时间点，用于验证 rpm/tpm 窗口边界喵。
	outsideWindow := time.Now().Add(-120 * time.Second).Unix()

	// 内部真实模型普通消费(type=2)：窗口内，应计入 rpm/tpm 与 quota 喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 10, CompletionTokens: 20, Quota: 100, CreatedAt: now}).Error)
	// 虚拟模型内部候选(type=9，真实模型名无前缀)：窗口内，rpm/tpm 应计入（修 bug a）；quota 不计入（非 type=2）喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeVirtualModel, ModelName: "gpt-4o-mini", PromptTokens: 5, CompletionTokens: 10, Quota: 50, CreatedAt: now}).Error)
	// 虚拟模型自定义候选(virtual/ 前缀)：按前缀排除出 rpm/tpm（判据 b）喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeVirtualModel, ModelName: "virtual/my-vm", PromptTokens: 100, CompletionTokens: 100, Quota: 50, CreatedAt: now}).Error)
	// 用户自定义上游(user/ 前缀)：按前缀排除出 rpm/tpm（判据 b）喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeVirtualModel, ModelName: "user/my-upstream", PromptTokens: 100, CompletionTokens: 100, Quota: 50, CreatedAt: now}).Error)
	// 内部真实模型消费(type=2)但在 60 秒窗口外：rpm/tpm 排除，quota 仍计入喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 7, CompletionTokens: 8, Quota: 999, CreatedAt: outsideWindow}).Error)

	// 「仅自己」范围：rpm/tpm 恒取 type IN(2,9) 且无 user//virtual/ 前缀的窗口内行喵。
	stat, err := SumUsedQuota(LogTypeConsume, 0, 0, "", "me", "", 0, "")
	require.NoError(t, err)
	// quota：type=2 的两条(100+999)，type=9 不进 quota 喵。
	assert.Equal(t, 1099, stat.Quota)
	// rpm：窗口内的 gpt-4o + gpt-4o-mini 两条喵。
	assert.Equal(t, 2, stat.Rpm)
	// tpm：gpt-4o(30) + gpt-4o-mini(15) = 45，不含被前缀排除的 user//virtual/ 行喵。
	assert.Equal(t, 45, stat.Tpm)
}

// TestSumUsedQuotaTpmIncludesAnthropicCacheRead 验证 anthropic 语义的缓存读 token 会补进 TPM，
// 而非 anthropic 语义（prompt_tokens 已含缓存命中）不会被重复相加喵。
func TestSumUsedQuotaTpmIncludesAnthropicCacheRead(t *testing.T) {
	truncateTables(t)
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)

	// 当前时间，落在 rpm/tpm 的最近 60 秒窗口内喵。
	now := time.Now().Unix()

	// anthropic 原生 messages 日志：输入 10 + 输出 20 + 缓存读 100，TPM 应为 130 喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "claude-sonnet-4", PromptTokens: 10, CompletionTokens: 20, Quota: 100, CreatedAt: now, Other: `{"usage_semantic":"anthropic","cache_tokens":100}`}).Error)
	// OpenAI 语义日志：缓存读已经含在 prompt_tokens 里，other 的 cache_tokens 不得再补，只算 10+20 喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 10, CompletionTokens: 20, Quota: 100, CreatedAt: now, Other: `{"cache_tokens":50}`}).Error)
	// 空 other 的普通日志：解析失败按无缓存处理，只算 5+5 喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "gpt-4o-mini", PromptTokens: 5, CompletionTokens: 5, Quota: 50, CreatedAt: now}).Error)
	// anthropic 语义的另一种缓存命中拼写 cached_tokens 也必须补进 TPM，只算 10+20+40 喵。
	require.NoError(t, LOG_DB.Create(&Log{Username: "me", Type: LogTypeConsume, ModelName: "claude-haiku-4", PromptTokens: 10, CompletionTokens: 20, Quota: 100, CreatedAt: now, Other: `{"usage_semantic":"anthropic","cached_tokens":40}`}).Error)

	stat, err := SumUsedQuota(LogTypeConsume, 0, 0, "", "me", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 4, stat.Rpm)
	// 130 + 30 + 10 + 70 = 240 喵。
	assert.Equal(t, 240, stat.Tpm)
	// quota 口径不受 TPM 缓存补算影响喵。
	assert.Equal(t, 350, stat.Quota)
}

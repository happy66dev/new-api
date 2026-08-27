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

	// 「全部」范围：自己的消费 + 共享被调，quota 300、rpm 2、tpm 100 喵。
	statAll, err := SumUsedQuota(LogTypeConsume, 0, 0, "", "me", "", 0, "", "user/shared-a")
	require.NoError(t, err)
	assert.Equal(t, 300, statAll.Quota)
	assert.Equal(t, 2, statAll.Rpm)
	assert.Equal(t, 100, statAll.Tpm)

	// 「仅自己」范围（不传共享名）：只统计自己的消费，quota 100 喵。
	statSelf, err := SumUsedQuota(LogTypeConsume, 0, 0, "", "me", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 100, statSelf.Quota)
}

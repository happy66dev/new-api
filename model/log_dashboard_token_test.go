package model

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestRecordConsumeLogDataTokenUsedAppliesToQuotaDataOnly 验证看板/排行榜的 token 计数来源喵：
// DataTokenUsed > 0 时 quota_data.token_used 采用该值（anthropic 补了缓存读取），
// 但消费日志行的 prompt/completion 列保持原始语义不变；不传时回退为 prompt+completion 喵。
func TestRecordConsumeLogDataTokenUsedAppliesToQuotaDataOnly(t *testing.T) {
	// 清空日志表与 quota_data，重置看板缓存，保证断言不依赖其他测试残留喵。
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, DB.Exec("DELETE FROM quota_data").Error)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	// 测试结束后恢复缓存与数据导出开关，避免污染后续用例喵。
	defer func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = make(map[string]*QuotaData)
		CacheQuotaDataLock.Unlock()
	}()

	// 数据导出开关关闭时不写 quota_data，因此测试内临时开启、退出后还原喵。
	originalDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	defer func() { common.DataExportEnabled = originalDataExportEnabled }()

	// 构造带用户名上下文的测试请求，供日志写用户名喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"claude-x","messages":[]}`))
	ctx.Set("username", "alice")

	// 场景一：anthropic 语义请求显式传 DataTokenUsed = 输入70 + 缓存读取30 + 输出7 喵。
	RecordConsumeLog(ctx, 7, RecordConsumeLogParams{
		ChannelId:        1,
		PromptTokens:     70,
		CompletionTokens: 7,
		ModelName:        "claude-x",
		Quota:            300,
		Group:            "default",
		DataTokenUsed:    107,
	})
	// 场景二：普通请求不传 DataTokenUsed，看板 token 回退为 prompt+completion = 30 喵。
	RecordConsumeLog(ctx, 7, RecordConsumeLogParams{
		ChannelId:        1,
		PromptTokens:     10,
		CompletionTokens: 20,
		ModelName:        "gpt-x",
		Quota:            100,
		Group:            "default",
	})

	// 日志行必须保持原 prompt/completion：anthropic 输入列仍为不含缓存的 70 喵。
	var claudeLog Log
	require.NoError(t, LOG_DB.Where("model_name = ?", "claude-x").Order("id DESC").First(&claudeLog).Error)
	require.Equal(t, 70, claudeLog.PromptTokens)
	require.Equal(t, 7, claudeLog.CompletionTokens)

	// quota_data 侧 token 按模型分别记录为 107 与 30 喵。
	tokenUsedByModel := make(map[string]int)
	CacheQuotaDataLock.Lock()
	for _, quotaData := range CacheQuotaData {
		tokenUsedByModel[quotaData.ModelName] = quotaData.TokenUsed
	}
	CacheQuotaDataLock.Unlock()
	require.Equal(t, 107, tokenUsedByModel["claude-x"])
	require.Equal(t, 30, tokenUsedByModel["gpt-x"])
}

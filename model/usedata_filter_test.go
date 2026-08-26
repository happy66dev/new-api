package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLogQuotaDataExcludesUserAndVirtualModels 验证看板/排行榜只统计 new-api 内部模型喵。
func TestLogQuotaDataExcludesUserAndVirtualModels(t *testing.T) {
	// 清空看板缓存，避免与其他测试互相污染喵。
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	// 用户自带/共享上游（user/ 前缀）与虚拟模型自定义上游（virtual/ 前缀）不计入看板喵。
	LogQuotaData(QuotaDataLogParams{UserID: 7, Username: "u", ModelName: "user/my-upstream", CreatedAt: 1000, Quota: 10, TokenUsed: 5})
	LogQuotaData(QuotaDataLogParams{UserID: 7, Username: "u", ModelName: "virtual/custom-candidate", CreatedAt: 1000, Quota: 20, TokenUsed: 5})

	// new-api 内部模型（含虚拟模型 internal 候选的真实模型名，无前缀）正常计入喵。
	LogQuotaData(QuotaDataLogParams{UserID: 7, Username: "u", ModelName: "gpt-4o", CreatedAt: 1000, Quota: 30, TokenUsed: 5})

	CacheQuotaDataLock.Lock()
	count := len(CacheQuotaData)
	CacheQuotaDataLock.Unlock()
	// 只有 gpt-4o 一条被记录喵。
	require.Equal(t, 1, count)
}

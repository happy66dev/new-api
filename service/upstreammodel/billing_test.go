package upstreammodel

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateUpstreamModelCostCents 用表驱动验证各 token 分类按直接价格计费喵。
// 每个价格代表每百万该类型 token 的 RMB 元，费用 = 各分类 token × 价格 求和 ÷ 1e6 × 100 分喵。
func TestCalculateUpstreamModelCostCents(t *testing.T) {
	cases := []struct {
		name  string
		model *model.UserUpstreamModel
		usage *dto.Usage
		want  int64
	}{
		{
			name:  "基础输入输出按各自价格计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CompletionRatio: "1"},
			usage: &dto.Usage{PromptTokens: 100000, CompletionTokens: 50000},
			// 输入 100000×10 + 输出 50000×1 = 1050000 → 105 分喵。
			want: 105,
		},
		{
			name:  "缓存命中按 cache_ratio 价格计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheRatio: "0.1"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 10000}},
			// 基础输入 90000×10 + 缓存 10000×0.1 = 901000 → 90.1 → 90 分喵。
			want: 90,
		},
		{
			name:  "缓存写入按 cache_creation_ratio 价格计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheCreationRatio: "2"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 5000}},
			// 基础输入 95000×10 + 写入 5000×2 = 960000 → 96 分喵。
			want: 96,
		},
		{
			name:  "Claude 5m/1h 缓存写入拆分计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheCreationRatio: "1", CacheCreation5mRatio: "3", CacheCreation1hRatio: "5"},
			usage: &dto.Usage{
				PromptTokens:                100000,
				PromptTokensDetails:         dto.InputTokenDetails{CachedCreationTokens: 4000},
				ClaudeCacheCreation5mTokens: 3000,
				ClaudeCacheCreation1hTokens: 1000,
			},
			// 基础输入 96000×10 + 5m 3000×3 + 1h 1000×5 = 974000 → 97.4 → 97 分喵。
			want: 97,
		},
		{
			name:  "图片输入按 image_ratio 价格计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", ImageRatio: "2"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{ImageTokens: 2000}},
			// 基础输入 98000×10 + 图片 2000×2 = 984000 → 98.4 → 98 分喵。
			want: 98,
		},
		{
			name:  "音频输入按 audio_ratio 价格计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", AudioRatio: "0.5"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{AudioTokens: 3000}},
			// 基础输入 97000×10 + 音频 3000×0.5 = 971500 → 97.15 → 97 分喵。
			want: 97,
		},
		{
			name:  "输出按 completion_ratio 价格计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CompletionRatio: "2"},
			usage: &dto.Usage{PromptTokens: 100000, CompletionTokens: 50000},
			// 输入 100000×10 + 输出 50000×2 = 1100000 → 110 分喵。
			want: 110,
		},
		{
			name:  "音频输出按 audio_completion_ratio 单独计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CompletionRatio: "1", AudioCompletionRatio: "3"},
			usage: &dto.Usage{PromptTokens: 100000, CompletionTokens: 50000, CompletionTokenDetails: dto.OutputTokenDetails{AudioTokens: 10000}},
			// 输入 100000×10 + 文本输出 40000×1 + 音频输出 10000×3 = 1070000 → 107 分喵。
			want: 107,
		},
		{
			name:  "ModelRatio 为空按默认 1 元计费",
			model: &model.UserUpstreamModel{CompletionRatio: "1"},
			usage: &dto.Usage{PromptTokens: 100000},
			// 输入价格默认 1 元：100000×1 /1e6×100 = 10 分喵。
			want: 10,
		},
		{
			name:  "ModelRatio 非法回退为零不计费",
			model: &model.UserUpstreamModel{ModelRatio: "abc"},
			usage: &dto.Usage{PromptTokens: 100000},
			want:  0,
		},
		{
			name:  "usage 为 nil 不计费",
			model: &model.UserUpstreamModel{ModelRatio: "10"},
			usage: nil,
			want:  0,
		},
		{
			name:  "分类价格非法回退为 1 元",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheRatio: "oops"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 10000}},
			// 基础输入 90000×10 + 缓存 10000×1 = 910000 → 91 分喵。
			want: 91,
		},
		{
			name:  "零 token usage 不计费",
			model: &model.UserUpstreamModel{ModelRatio: "10"},
			usage: &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0},
			want:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			costCents, err := CalculateUpstreamModelCostCents(tc.model, tc.usage)
			require.NoError(t, err)
			assert.Equal(t, tc.want, costCents)
		})
	}
}

// TestCalculateUpstreamModelCostCentsClampsOversize 验证超大 usage 被饱和钳制而非溢出成负值喵。
func TestCalculateUpstreamModelCostCentsClampsOversize(t *testing.T) {
	// 空对象防御：nil 模型对象返回零费用喵。
	costCents, err := CalculateUpstreamModelCostCents(nil, &dto.Usage{PromptTokens: 100})
	require.NoError(t, err)
	assert.Equal(t, int64(0), costCents)

	// 超大 token 数即使 ×1000 倍率也钳制到 int32 上限，绝不溢出成负数喵。
	oversize := &model.UserUpstreamModel{ModelRatio: "1000"}
	costCents, err = CalculateUpstreamModelCostCents(oversize, &dto.Usage{PromptTokens: 1 << 40, CompletionTokens: 1 << 40})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, costCents, int64(0))
	assert.LessOrEqual(t, costCents, int64(common.MaxQuota))
}

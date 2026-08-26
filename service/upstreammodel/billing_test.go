package upstreammodel

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateUpstreamModelCostCents 用表驱动验证各 token 分类与倍率的加权计费喵。
// 费用 = 加权 token × ModelRatio（每百万 token RMB）÷ 1e6 × 100 分，结果四舍五入到分喵。
func TestCalculateUpstreamModelCostCents(t *testing.T) {
	cases := []struct {
		name  string
		model *model.UserUpstreamModel
		usage *dto.Usage
		want  int64
	}{
		{
			name:  "基础输入输出按 1 倍计费",
			model: &model.UserUpstreamModel{ModelRatio: "10", CompletionRatio: "1"},
			usage: &dto.Usage{PromptTokens: 100000, CompletionTokens: 50000},
			// 15 万加权 token × 10 元/百万 × 100 分 = 150 分喵。
			want: 150,
		},
		{
			name:  "缓存命中按 cache_ratio 加权",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheRatio: "0.1"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 10000}},
			// 基础 90000 + 缓存 10000×0.1=1000 → 91000 → 91 分喵。
			want: 91,
		},
		{
			name:  "缓存写入按 cache_creation_ratio 加权",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheCreationRatio: "2"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 5000}},
			// 基础 95000 + 写入 5000×2=10000 → 105000 → 105 分喵。
			want: 105,
		},
		{
			name:  "Claude 5m/1h 缓存写入拆分加权",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheCreationRatio: "1", CacheCreation5mRatio: "3", CacheCreation1hRatio: "5"},
			usage: &dto.Usage{
				PromptTokens:                100000,
				PromptTokensDetails:         dto.InputTokenDetails{CachedCreationTokens: 4000},
				ClaudeCacheCreation5mTokens: 3000,
				ClaudeCacheCreation1hTokens: 1000,
			},
			// 基础 96000 + 5m 3000×3 + 1h 1000×5 = 14000 → 110000 → 110 分喵。
			want: 110,
		},
		{
			name:  "图片输入按 image_ratio 加权",
			model: &model.UserUpstreamModel{ModelRatio: "10", ImageRatio: "2"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{ImageTokens: 2000}},
			// 基础 98000 + 图片 2000×2=4000 → 102000 → 102 分喵。
			want: 102,
		},
		{
			name:  "音频输入按 audio_ratio 加权",
			model: &model.UserUpstreamModel{ModelRatio: "10", AudioRatio: "0.5"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{AudioTokens: 3000}},
			// 基础 97000 + 音频 3000×0.5=1500 → 98500 ×10/1e6×100=98.5 → 四舍五入 99 分喵。
			want: 99,
		},
		{
			name:  "输出按 completion_ratio 加权",
			model: &model.UserUpstreamModel{ModelRatio: "10", CompletionRatio: "2"},
			usage: &dto.Usage{PromptTokens: 100000, CompletionTokens: 50000},
			// 输入 100000 + 输出 50000×2=100000 → 200000 → 200 分喵。
			want: 200,
		},
		{
			name:  "ModelRatio 为空不计费",
			model: &model.UserUpstreamModel{CompletionRatio: "1"},
			usage: &dto.Usage{PromptTokens: 100000},
			want:  0,
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
			name:  "分类倍率非法回退为 1",
			model: &model.UserUpstreamModel{ModelRatio: "10", CacheRatio: "oops"},
			usage: &dto.Usage{PromptTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 10000}},
			// 基础 90000 + 缓存 10000×1=10000 → 100000 → 100 分喵。
			want: 100,
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

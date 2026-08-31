package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

// TestDegradeZeroCompletionUsage 表驱动验证输出 token 为 0 时的降级补全喵。
func TestDegradeZeroCompletionUsage(t *testing.T) {
	cases := []struct {
		name            string
		usage           *dto.Usage
		modelName       string
		responseText    string
		wantComplete    int
		wantTotal       int
		wantEstimatedGT bool // 期望被文本估算且结果必须大于 0 喵。
	}{
		{
			// 空 usage 不崩溃、不改动喵。
			name:         "空 usage 直接跳过",
			usage:        nil,
			responseText: "有内容",
			wantComplete: 0,
			wantTotal:    0,
		},
		{
			// 输出已非零时不降级，保留真实值喵。
			name:         "输出已非零不降级",
			usage:        &dto.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			responseText: "有内容",
			wantComplete: 50,
			wantTotal:    150,
		},
		{
			// 上游只报推理 token 未并入 completion：推理量直接作为输出喵。
			name: "推理 token 并入输出",
			usage: &dto.Usage{PromptTokens: 23518, CompletionTokens: 0,
				CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 203}, TotalTokens: 23518},
			responseText: "思考与答案",
			wantComplete: 203,
			wantTotal:    23518 + 203,
		},
		{
			// 无推理 token：用响应文本（content+思考）估算补全，总量刷新喵。
			name:            "响应文本估算补全",
			usage:           &dto.Usage{PromptTokens: 100, CompletionTokens: 0, TotalTokens: 100},
			modelName:       "deepseek-chat",
			responseText:    "这是一段真实回答内容，用于估算输出 token 数量喵。",
			wantComplete:    0,
			wantTotal:       0,
			wantEstimatedGT: true,
		},
		{
			// 响应文本为空：无素材可估，保持 0 不越权喵。
			name:         "无文本不估算",
			usage:        &dto.Usage{PromptTokens: 100, CompletionTokens: 0, TotalTokens: 100},
			responseText: "",
			wantComplete: 0,
			wantTotal:    100,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			DegradeZeroCompletionUsage(tc.usage, tc.modelName, tc.responseText)
			// 喵~防御：空 usage 用例直接返回，无字段可断言喵。
			if tc.usage == nil {
				return
			}
			if tc.wantEstimatedGT {
				// 文本估算结果不可精确预测，只断言大于 0 且总量与两侧一致喵。
				require.Greater(t, tc.usage.CompletionTokens, 0, "非空响应文本必须估出正数输出 token")
				require.Equal(t, tc.usage.PromptTokens+tc.usage.CompletionTokens, tc.usage.TotalTokens)
				return
			}
			require.Equal(t, tc.wantComplete, tc.usage.CompletionTokens)
			require.Equal(t, tc.wantTotal, tc.usage.TotalTokens)
		})
	}
}

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseAutoCheckinBalance 覆盖余额文本解析：带货币符号/千分位/负号/空白/乱码等形态喵~
func TestParseAutoCheckinBalance(t *testing.T) {
	// 每个用例：输入文本、期望数值、期望是否解析成功
	cases := []struct {
		name  string  // 用例名
		text  string  // 输入余额原文
		value float64 // 期望解析出的数值
		ok    bool    // 期望是否成功
	}{
		{"带美元符号", "$9.99", 9.99, true},
		{"带千分位逗号", "1,234.56", 1234.56, true},
		{"纯小数", "45.60", 45.6, true},
		{"负数余额", "-12.5", -12.5, true},
		{"空文本", "", 0, false},
		{"纯空白", "   ", 0, false},
		{"纯字母乱码", "abc", 0, false},
		{"只有货币符号", "$", 0, false},
		{"只有负号", "-", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value, ok := parseAutoCheckinBalance(c.text)
			assert.Equal(t, c.ok, ok)
			if c.ok {
				// 浮点比较留一点容差，避免二进制表示误差
				assert.InDelta(t, c.value, value, 1e-9)
			}
		})
	}
}

// TestFoldAutoCheckinSite 覆盖站点折叠：变体键折回主站、主站原样、空值兜底 agentrouter、首尾空白清理喵~
func TestFoldAutoCheckinSite(t *testing.T) {
	// 每个用例：输入站点键、期望折叠后的主站键
	cases := []struct {
		in   string // 输入站点键
		want string // 期望主站键
	}{
		{"agentrouter-linuxdo", "agentrouter"},     // linuxdo 变体折回 agentrouter
		{"budsai-email", "budsai"},                 // budsai-email 折回 budsai
		{"agentrouter", "agentrouter"},             // 主站原样返回
		{"justwoker", "justwoker"},                 // 独立站点原样返回
		{"", "agentrouter"},                        // 空值兜底 agentrouter
		{"  agentrouter-linuxdo  ", "agentrouter"}, // 首尾空白先清理再折叠
	}
	for _, c := range cases {
		assert.Equal(t, c.want, foldAutoCheckinSite(c.in))
	}
}

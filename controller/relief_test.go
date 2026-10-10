package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestReliefReasonMessage 覆盖领取失败机器码 → 中性中文提示的映射，含未知码兜底喵~
func TestReliefReasonMessage(t *testing.T) {
	// 每个用例：机器码、期望的中性中文提示（对外文案，不含语气词）
	cases := map[string]string{
		"balance_too_high":       "当前钱包余额高于领取门槛，暂不可领取",
		"subscription_available": "您当前仍有可用的订阅额度，暂不可领取",
		"cooldown":               "救济金冷却中，请稍后再试",
		"pool_empty":             "救济池额度不足，暂时无法发放",
		"unknown_code":           "当前不满足救济金领取条件", // 未知码走兜底
		"":                       "当前不满足救济金领取条件", // 空码也走兜底
	}
	for code, want := range cases {
		assert.Equal(t, want, reliefReasonMessage(code))
	}
}

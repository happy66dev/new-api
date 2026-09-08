package virtualmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeCandidateTimeoutSeconds 验证候选请求超时的统一规整规则喵。
// 未配置或超出 600s 硬顶一律回退 600s；显式配置的 (0,600] 内超时原样保留喵。
func TestNormalizeCandidateTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name     string
		configured int
		want     int
	}{
		{name: "unset falls back to hard cap", configured: 0, want: 600},
		{name: "at hard cap stays", configured: 600, want: 600},
		{name: "above hard cap falls back", configured: 601, want: 600},
		{name: "short explicit timeout kept", configured: 90, want: 90},
		{name: "one second kept", configured: 1, want: 1},
		{name: "negative falls back to hard cap", configured: -5, want: 600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NormalizeCandidateTimeoutSeconds(tc.configured))
		})
	}
}

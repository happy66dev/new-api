package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteModelByEffortUsesConfiguredChannelTypeAndEffort(t *testing.T) {
	original := GetGlobalSettings().EffortModelRoutes
	t.Cleanup(func() { GetGlobalSettings().EffortModelRoutes = original })
	GetGlobalSettings().EffortModelRoutes = map[string]map[string]map[string]string{
		"gemini": {
			"gemini-3.1-flash-lite": {
				"high": "gemini-3.1-flash-lite-high",
				"low":  "gemini-3.1-flash-lite-low",
			},
		},
	}

	routed, ok := GetGlobalSettings().RouteModelByEffort(constant.ChannelTypeGemini, "gemini-3.1-flash-lite", "HIGH")
	require.True(t, ok)
	assert.Equal(t, "gemini-3.1-flash-lite-high", routed)
	_, ok = GetGlobalSettings().RouteModelByEffort(constant.ChannelTypeAnthropic, "gemini-3.1-flash-lite", "high")
	assert.False(t, ok)
}

func TestValidateEffortModelRoutesRejectsUnknownCategory(t *testing.T) {
	err := ValidateEffortModelRoutes(`{"vertex":{"model":{"high":"upstream"}}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported effort route category")
}

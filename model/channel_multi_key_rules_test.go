package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchesMultiKeyDisableRuleMatchesStatusAndMessage(t *testing.T) {
	channel := &Channel{ChannelInfo: ChannelInfo{
		IsMultiKey: true,
		MultiKeyDisableRules: []MultiKeyDisableRule{{
			StatusCode: 429,
			Message:    "Daily usage limit exceeded",
		}},
	}}

	assert.True(t, channel.MatchesMultiKeyDisableRule(429, "Daily usage limit exceeded: maximum $1 per period"))
	assert.False(t, channel.MatchesMultiKeyDisableRule(500, "Daily usage limit exceeded"))
	assert.False(t, channel.MatchesMultiKeyDisableRule(429, "rate limited"))
}

func TestMatchesMultiKeyDisableRuleSkipsEmptyRulesAndNonMultiKeyChannels(t *testing.T) {
	channel := &Channel{ChannelInfo: ChannelInfo{
		MultiKeyDisableRules: []MultiKeyDisableRule{{}},
	}}
	assert.False(t, channel.MatchesMultiKeyDisableRule(429, "anything"))

	channel.ChannelInfo.IsMultiKey = true
	channel.ChannelInfo.MultiKeyDisableRules = []MultiKeyDisableRule{{StatusCode: 429}}
	assert.True(t, channel.MatchesMultiKeyDisableRule(429, ""))
}

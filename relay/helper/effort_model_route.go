package helper

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

// ApplyEffortModelRoute resolves the configured model for the selected channel
// type and effort. It intentionally returns false when no route is configured,
// leaving the existing model-mapping and provider behavior unchanged.
func ApplyEffortModelRoute(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	routed, ok := model_setting.GetGlobalSettings().RouteModelByEffort(
		info.ChannelMeta.ChannelType,
		info.UpstreamModelName,
		info.GetReasoningEffort(),
	)
	if !ok || routed == info.UpstreamModelName {
		return false
	}
	info.UpstreamModelName = routed
	info.ChannelMeta.UpstreamModelName = routed
	info.EffortModelRouted = true
	return true
}

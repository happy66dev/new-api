package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// setUserUpstreamMasterOptionForControllerTest 测试期间把总开关(UserUpstreamEnabled)置为指定值并在结束后恢复喵。
func setUserUpstreamMasterOptionForControllerTest(t *testing.T, enabled string) {
	t.Helper()
	// 喵~防御：OptionMap 可能尚未初始化，先补默认空映射避免写空指针喵。
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldMaster := common.OptionMap[model.UserUpstreamEnabledKey]
	common.OptionMap[model.UserUpstreamEnabledKey] = enabled
	t.Cleanup(func() { common.OptionMap[model.UserUpstreamEnabledKey] = oldMaster })
}

// TestValidateVirtualModelCandidateSourceInputMasterDisabled 验证总开关关闭时，
// 自定义候选的直填 url/key 与引用用户上游两条配置路径都被拒绝，内部候选不受影响喵。
func TestValidateVirtualModelCandidateSourceInputMasterDisabled(t *testing.T) {
	setUserUpstreamMasterOptionForControllerTest(t, "false")

	// 直填 url/key 路径：总开关关闭时一律拒绝，即便字段看起来合法喵。
	directFill := virtualModelCandidateInput{
		SourceType:    model.VirtualModelSourceCustom,
		RealModelName: "gpt-direct",
		BaseURL:       "https://example.com/v1",
		APIKey:        "sk-test",
		AuthStyle:     model.VirtualModelAuthBearer,
	}
	require.Error(t, validateVirtualModelCandidateSourceInput(model.VirtualModelSourceCustom, directFill, true))

	// 引用用户上游路径：总开关关闭时同样拒绝，避免通过引用绕过冻结喵。
	upstreamID := int64(5)
	upstreamReference := virtualModelCandidateInput{
		SourceType:      model.VirtualModelSourceCustom,
		UpstreamModelID: &upstreamID,
	}
	require.Error(t, validateVirtualModelCandidateSourceInput(model.VirtualModelSourceCustom, upstreamReference, true))

	// 内部候选不受总开关影响，仍可正常校验通过喵。
	internalInput := virtualModelCandidateInput{
		SourceType:    model.VirtualModelSourceInternal,
		GroupName:     "default",
		RealModelName: "gpt-4o",
	}
	require.NoError(t, validateVirtualModelCandidateSourceInput(model.VirtualModelSourceInternal, internalInput, true))
}

// TestValidateVirtualModelCandidateSourceInputMasterEnabled 对比验证总开关开启时
// 直填候选按原校验规则放行/拒绝，证明门禁只在关闭时生效喵。
func TestValidateVirtualModelCandidateSourceInputMasterEnabled(t *testing.T) {
	setUserUpstreamMasterOptionForControllerTest(t, "true")

	// 直填候选缺少地址时仍按原规则报错，说明开关开启时校验没有被旁路喵。
	incomplete := virtualModelCandidateInput{
		SourceType:    model.VirtualModelSourceCustom,
		RealModelName: "gpt-direct",
		AuthStyle:     model.VirtualModelAuthBearer,
	}
	require.Error(t, validateVirtualModelCandidateSourceInput(model.VirtualModelSourceCustom, incomplete, true))
}

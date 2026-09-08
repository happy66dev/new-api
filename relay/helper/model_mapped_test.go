package helper

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelMappedHelperKeepsUpstreamModelWhenChannelMappingIsEmpty(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	request := &dto.GeneralOpenAIRequest{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-terra",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "openai/gpt-5.6-terra",
		},
	}

	// 渠道已把模型解析成具体上游名；model_mapping 为空时不应改写它喵。
	require.NoError(t, ModelMappedHelper(c, info, request))
	require.Equal(t, "openai/gpt-5.6-terra", info.UpstreamModelName)
	require.Equal(t, "openai/gpt-5.6-terra", request.Model)
}

func TestModelMappedHelperUsesCommonJSONDecoder(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set("model_mapping", `{"gpt-4o":"openai/gpt-4o-2024-08-06"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-4o"}
	request := &dto.GeneralOpenAIRequest{}

	require.NoError(t, ModelMappedHelper(c, info, request))
	require.Equal(t, "openai/gpt-4o-2024-08-06", request.Model)
}

package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestSetupApiRequestHeaderInjectsLoopGuard 验证转发请求头注入回环检测标记喵。
func TestSetupApiRequestHeaderInjectsLoopGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(writer)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	// 模拟 RequestId 中间件已生成请求唯一标识喵。
	requestID := common2.NewRequestId()
	context.Set(common2.RequestIdKey, requestID)

	info := &relaycommon.RelayInfo{IsStream: true}
	upstreamHeaders := http.Header{}
	SetupApiRequestHeader(info, context, &upstreamHeaders)

	// 标记必须存在、以本实例 ID 开头，且能被本实例识别为回环喵。
	guardValue := upstreamHeaders.Get(common2.LoopGuardHeaderKey)
	require.NotEmpty(t, guardValue, "loop guard header must be injected")
	require.True(t, strings.HasPrefix(guardValue, common2.InstanceID()+":"), "guard value must start with self instance id")
	require.True(t, common2.IsLoopGuardFromSelf(guardValue), "guard value must be recognizable as self")
}

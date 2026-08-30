package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
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

// TestDoRequestNoPingWhenProbeEnabled 验证流式探测/伪流启用时 doRequest 级 ping 保活被抑制，
// 避免提前向客户端写字节提交 200 导致探测失败无法回 502/切换候选喵。
func TestDoRequestNoPingWhenProbeEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	// 保存并缩短全局 ping 间隔，便于在观察窗口内捕获 ping 字节喵。
	setting := operation_setting.GetGeneralSetting()
	oldEnabled := setting.PingIntervalEnabled
	oldSeconds := setting.PingIntervalSeconds
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	t.Cleanup(func() {
		setting.PingIntervalEnabled = oldEnabled
		setting.PingIntervalSeconds = oldSeconds
	})

	run := func(t *testing.T, probeEnabled bool) string {
		t.Helper()
		// 上游延迟 1.5s 才返回响应头；若 doRequest 级 ping 已启动，会在该窗口内写出至少一个 ping 喵。
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(1500 * time.Millisecond)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}))
		defer server.Close()

		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if probeEnabled {
			// 写入有效探测参数，模拟虚拟模型内部候选喵。
			common2.SetContextKey(c, constant.ContextKeyVirtualModelProbeParameters, virtualmodelservice.ProbeParameters{
				StallTimeoutSeconds:      60,
				MinContentChars:          5,
				ProbeTotalTimeoutSeconds: 60,
			})
		}

		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
		require.NoError(t, err)
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}

		resp, err := DoRequest(c, req, info)
		require.NoError(t, err)
		defer resp.Body.Close()
		return recorder.Body.String()
	}

	// 探测启用：doRequest 级 ping 被抑制，不向客户端写任何字节喵。
	probeBody := run(t, true)
	assert.NotContains(t, probeBody, ": PING", "探测启用时不得提前提交 200 状态")

	// 普通流式（无探测）：doRequest 级 ping 行为保持不变，观察窗口内应写出 ping 喵。
	normalBody := run(t, false)
	assert.Contains(t, normalBody, ": PING", "普通流式请求的 keep-alive ping 必须保持不变")
}

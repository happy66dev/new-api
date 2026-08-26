package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestExecuteCustomVirtualModelCandidateUpstreamReference 验证候选引用用户上游模型的加载与硬检查喵。
func TestExecuteCustomVirtualModelCandidateUpstreamReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 引用不存在的用户上游模型条目：候选不可用返回 503 喵。
	missingID := int64(9999)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vm-ref","messages":[]}`))
	ctx.Set("id", 7)
	executed := executeCustomVirtualModelCandidate(ctx, &model.VirtualModelInternalCandidateSnapshot{CandidateID: 1, UpstreamModelID: &missingID}, &model.VirtualModelExecutionSnapshot{})
	require.False(t, executed)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	// 引用余额为 0 的用户上游模型：请求前硬检查返回额度不足喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "empty-balance", Enabled: true, BalanceCents: 0}).Error)
	var created model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "empty-balance").First(&created).Error)
	emptyBalanceID := created.ID
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vm-ref","messages":[]}`))
	ctx.Set("id", 7)
	executed = executeCustomVirtualModelCandidate(ctx, &model.VirtualModelInternalCandidateSnapshot{CandidateID: 2, UpstreamModelID: &emptyBalanceID}, &model.VirtualModelExecutionSnapshot{})
	require.False(t, executed)
	require.Equal(t, http.StatusConflict, recorder.Code)

	// 引用余额充足且上限未耗尽的模型：硬检查放行，密文无效返回 503 而非 409 喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "quota-ok", Enabled: true, BalanceCents: 500, SpendLimitCents: 300, TotalSpentCents: 100, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o"}).Error)
	var okModel model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "quota-ok").First(&okModel).Error)
	quotaOKID := okModel.ID
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vm-ref","messages":[]}`))
	ctx.Set("id", 7)
	executed = executeCustomVirtualModelCandidate(ctx, &model.VirtualModelInternalCandidateSnapshot{CandidateID: 3, UpstreamModelID: &quotaOKID}, &model.VirtualModelExecutionSnapshot{})
	require.False(t, executed)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

package controller

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupVirtualModelCreateTestDB 用内存 SQLite 替换全局数据库，供创建与更新接口用例使用喵。
// 输入：测试对象；输出：无。
// 边界条件：用例结束后必须恢复原来的全局连接，避免污染同包其他测试喵。
func setupVirtualModelCreateTestDB(t *testing.T) {
	t.Helper()
	testDB, openError := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, openError)
	// 建齐创建/更新接口会读写的表，尤其是 VirtualModel 上带唯一索引的属主+名称列喵。
	require.NoError(t, testDB.AutoMigrate(
		&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{},
		&model.VirtualModelCustomCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{},
		&model.VirtualModelTokenBinding{}, &model.VirtualModelAuditLog{},
	))
	originalDB := model.DB
	model.DB = testDB
	t.Cleanup(func() { model.DB = originalDB })
}

// newVirtualModelCreateTestModel 构造一条可入库的虚拟模型，供重名用例预置数据喵。
// 输入：属主编号与规范化名称；输出：可写入数据库的模型实体。
// 边界条件：超时与循环轮数取合法最小值以上的默认值，确保能通过配置校验喵。
func newVirtualModelCreateTestModel(ownerUserID int, normalizedName string) *model.VirtualModel {
	return &model.VirtualModel{
		OwnerUserID:         ownerUserID, // 资源属主：重名判定只在同一属主内部生效喵
		NormalizedName:      normalizedName,
		DisplayName:         normalizedName,
		Enabled:             true,
		Version:             1,
		TotalTimeoutSeconds: 120,
		MaxLoopRounds:       1,
	}
}

// readVirtualModelErrorResponse 读取控制面错误响应里的 success/code/message 三个字段喵。
// 输入：httptest 记录器；输出：错误响应结构体。
// 边界条件：响应体不是合法 JSON 时用例直接失败，避免静默读到零值喵。
func readVirtualModelErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	t.Helper()
	errorResponse := struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &errorResponse))
	return errorResponse
}

// TestCreateVirtualModelDuplicateNameReturnsFriendlyConflict 验证同属主重名创建返回 409 与可读提示喵。
// 回归 ① 的重名提示部分：此前直接把数据库唯一索引报错原文透给用户，前端拿不到 code 也无法翻译喵。
func TestCreateVirtualModelDuplicateNameReturnsFriendlyConflict(t *testing.T) {
	setupVirtualModelCreateTestDB(t)
	// 属主 7 先占用 happy-plan 这个名称喵。
	require.NoError(t, model.DB.Create(newVirtualModelCreateTestModel(7, "happy-plan")).Error)

	// 同一属主再次提交同名创建请求，只有显示名不同喵。
	createBody := []byte(`{"normalized_name":"happy-plan","display_name":"另一个显示名","enabled":true,"total_timeout_seconds":120,"max_loop_rounds":1}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/virtual-models", bytes.NewReader(createBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)
	CreateVirtualModel(ctx)

	// 必须用 409 + 固定错误码表达冲突，前端据此展示本地化文案喵。
	require.Equal(t, http.StatusConflict, recorder.Code)
	conflictResponse := readVirtualModelErrorResponse(t, recorder)
	require.False(t, conflictResponse.Success)
	require.Equal(t, "virtual_model_name_conflict", conflictResponse.Code)
	// 喵~防御：提示里禁止出现驱动级关键字，否则等于把数据库错误原文泄露给最终用户喵。
	loweredMessage := strings.ToLower(conflictResponse.Message)
	require.NotContains(t, loweredMessage, "unique")
	require.NotContains(t, loweredMessage, "duplicate")
	require.NotContains(t, loweredMessage, "constraint")
	// 重名请求被拒后库里仍只有原来那一条，不能留下半条脏数据喵。
	var storedModelCount int64
	require.NoError(t, model.DB.Model(&model.VirtualModel{}).Where("owner_user_id = ?", 7).Count(&storedModelCount).Error)
	require.Equal(t, int64(1), storedModelCount)
}

// TestCreateVirtualModelAllowsSameNameAcrossOwners 验证跨用户重名仍然允许创建，重名冲突只在属主内部成立喵。
func TestCreateVirtualModelAllowsSameNameAcrossOwners(t *testing.T) {
	setupVirtualModelCreateTestDB(t)
	// 属主 7 先占住 shared-plan 名称喵。
	require.NoError(t, model.DB.Create(newVirtualModelCreateTestModel(7, "shared-plan")).Error)

	// 属主 8 提交同名创建请求，应当成功而不是报重名喵。
	createBody := []byte(`{"normalized_name":"shared-plan","display_name":"Shared Plan","enabled":true,"total_timeout_seconds":120,"max_loop_rounds":1}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/virtual-models", bytes.NewReader(createBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 8)
	CreateVirtualModel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	// 两个属主各有一条同名模型，证明唯一索引只约束属主+名称组合喵。
	var storedModelCount int64
	require.NoError(t, model.DB.Model(&model.VirtualModel{}).Where("normalized_name = ?", "shared-plan").Count(&storedModelCount).Error)
	require.Equal(t, int64(2), storedModelCount)
}

// TestUpdateVirtualModelRenameConflictReturnsFriendlyConflict 验证改名撞上同属主既有名称时同样返回受控 409 喵。
func TestUpdateVirtualModelRenameConflictReturnsFriendlyConflict(t *testing.T) {
	setupVirtualModelCreateTestDB(t)
	// 属主 7 名下已有 plan-a 与 plan-b 两个模型喵。
	require.NoError(t, model.DB.Create(newVirtualModelCreateTestModel(7, "plan-a")).Error)
	renamingModel := newVirtualModelCreateTestModel(7, "plan-b")
	require.NoError(t, model.DB.Create(renamingModel).Error)

	// 把 plan-b 改名成已被占用的 plan-a，版本号与库内保持一致以通过乐观锁喵。
	updateBody := []byte(`{"normalized_name":"plan-a","display_name":"Plan B","enabled":true,"total_timeout_seconds":120,"max_loop_rounds":1,"version":1}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/virtual-models/%d", renamingModel.ID), bytes.NewReader(updateBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", renamingModel.ID)}}
	ctx.Set("id", 7)
	UpdateVirtualModel(ctx)

	require.Equal(t, http.StatusConflict, recorder.Code)
	renameConflictResponse := readVirtualModelErrorResponse(t, recorder)
	require.False(t, renameConflictResponse.Success)
	require.Equal(t, "virtual_model_name_conflict", renameConflictResponse.Code)

	// 喵~防御：改名失败后原模型必须保持 plan-b，不能被部分写入喵。
	var storedModel model.VirtualModel
	require.NoError(t, model.DB.First(&storedModel, renamingModel.ID).Error)
	require.Equal(t, "plan-b", storedModel.NormalizedName)
}

// TestIsVirtualModelNameConflictErrorMatchesAllDialects 验证三种数据库的重名报错都能被识别，且不会误判其他约束错误喵。
func TestIsVirtualModelNameConflictErrorMatchesAllDialects(t *testing.T) {
	// 三类数据库的唯一索引报错文案各不相同，必须逐一覆盖喵。
	dialectCases := []struct {
		caseName    string // 用例名称，标明数据库方言
		errMessage  string // 模拟的数据库报错原文
		wantMatched bool   // 期望是否识别为重名冲突
	}{
		{"sqlite unique", "UNIQUE constraint failed: virtual_models.owner_user_id, virtual_models.normalized_name", true},
		{"mysql 1062", "Error 1062 (23000): Duplicate entry 'happy-plan' for key 'virtual_models.idx_virtual_model_owner_name'", true},
		{"postgres 23505", `duplicate key value violates unique constraint "idx_virtual_model_owner_name"`, true},
		{"foreign key", "FOREIGN KEY constraint failed", false},
		{"unique on another table", "UNIQUE constraint failed: tokens.key", false},
		{"plain error", "database is locked", false},
	}
	for _, dialectCase := range dialectCases {
		t.Run(dialectCase.caseName, func(t *testing.T) {
			// 每种方言只改报错文本，识别函数的行为应保持稳定喵。
			require.Equal(t, dialectCase.wantMatched, isVirtualModelNameConflictError(errors.New(dialectCase.errMessage)))
		})
	}
	// 喵~防御：nil 错误必须安全返回 false，不能让调用方在空错误上崩溃喵。
	require.False(t, isVirtualModelNameConflictError(nil))
}

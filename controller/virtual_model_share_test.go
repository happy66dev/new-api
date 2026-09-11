package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// virtualModelShareUnusableGroup 是一个不在默认可用分组集合里的分组名喵。
// 默认可用分组是 setting 里的 default 与 vip，因此用 vip 测"无权限"会测错方向喵。
const virtualModelShareUnusableGroup = "restricted"

// virtualModelShareTestMasterKey 是测试用的 32 字节凭据主密钥（无填充 base64 编码）喵。
const virtualModelShareTestMasterKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

// setupVirtualModelShareTestDB 初始化分享码测试的共享内存库并迁移相关表喵。
func setupVirtualModelShareTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false

	// 凭据加解密依赖专用主密钥环境变量，测试里显式注入，结束后还原避免污染其他用例喵。
	originalMasterKey, hadMasterKey := os.LookupEnv(virtualmodelservice.CredentialMasterKeyEnvironmentName)
	t.Cleanup(func() {
		if hadMasterKey {
			require.NoError(t, os.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, originalMasterKey))
		} else {
			require.NoError(t, os.Unsetenv(virtualmodelservice.CredentialMasterKeyEnvironmentName))
		}
	})
	require.NoError(t, os.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, virtualModelShareTestMasterKey))

	// 保存并恢复所有被改动的全局状态喵。
	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	t.Cleanup(func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainType, originalLogType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	})

	// 用独立命名内存库初始化全局连接，保证分享码相关用例互不污染喵。
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "virtual_model_share_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(
		&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{},
		&model.VirtualModelCustomCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{},
		&model.VirtualModelTokenBinding{}, &model.VirtualModelManualFreeze{}, &model.VirtualModelInternalFreezeState{},
		&model.VirtualModelCustomFreezeState{}, &model.VirtualModelAuditLog{}, &model.VirtualModelShareCode{},
		&model.Ability{}, &model.EntityProbeState{},
	))
	// 共享内存库跨用例存活，按表名逐一清空避免主键或唯一索引残留冲突喵。
	clearTables := []string{
		"virtual_models", "virtual_model_candidates", "virtual_model_internal_candidates",
		"virtual_model_custom_candidates", "virtual_model_failure_rules", "virtual_model_global_failure_rules",
		"virtual_model_token_bindings", "virtual_model_manual_freezes", "virtual_model_internal_freeze_states",
		"virtual_model_custom_freeze_states", "virtual_model_audit_logs", "virtual_model_share_codes",
		"abilities", "entity_probe_states",
	}
	for _, tableName := range clearTables {
		require.NoError(t, model.DB.Exec("DELETE FROM "+tableName).Error)
	}
}

// seedVirtualModelShareAbility 写入一条分组下可用的模型能力记录，供模型可用性判定使用喵。
func seedVirtualModelShareAbility(t *testing.T, groupName string, modelName string, channelID int) {
	t.Helper()
	ability := &model.Ability{Group: groupName, Model: modelName, ChannelId: channelID, Enabled: true}
	require.NoError(t, model.DB.Create(ability).Error)
}

// newVirtualModelShareContext 构造带会话身份与用户分组的 JSON 请求上下文喵。
func newVirtualModelShareContext(ownerUserID int, userGroup string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/virtual-models/import", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", ownerUserID)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, userGroup)
	return ctx, recorder
}

// createVirtualModelShareTestModel 建立一个"内部候选 + 直填自定义候选"的两候选方案喵。
// 返回模型、内部候选编号、自定义候选编号以及自定义候选的上游地址明文喵。
func createVirtualModelShareTestModel(t *testing.T, ownerUserID int, normalizedName string) (*model.VirtualModel, int, int, string) {
	t.Helper()
	virtualModel := &model.VirtualModel{
		OwnerUserID: ownerUserID, NormalizedName: normalizedName, DisplayName: "分享方案",
		Enabled: true, TotalTimeoutSeconds: 120, MaxLoopRounds: 1, CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(virtualModel).Error)

	internalCandidate := &model.VirtualModelCandidate{
		VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal,
		Enabled: true, TimeoutSeconds: 60,
	}
	require.NoError(t, model.DB.Create(internalCandidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelInternalCandidate{
		CandidateID: internalCandidate.ID, GroupName: "default", RealModelName: "gpt-4o",
	}).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelFailureRule{
		CandidateID: internalCandidate.ID, RuleOrder: 0, HTTPStatus: 429, Action: model.VirtualModelActionNext,
	}).Error)

	const plainBaseURL = "https://upstream.example.com"
	encryptedBaseURL, credentialVersion, encryptError := virtualmodelservice.EncryptCredential(plainBaseURL)
	require.NoError(t, encryptError)
	encryptedAPIKey, _, keyEncryptError := virtualmodelservice.EncryptCredential("sk-share-secret-key-1234567890")
	require.NoError(t, keyEncryptError)
	customCandidate := &model.VirtualModelCandidate{
		VirtualModelID: virtualModel.ID, StableOrder: 1, SourceType: model.VirtualModelSourceCustom,
		Enabled: true, TimeoutSeconds: 60,
	}
	require.NoError(t, model.DB.Create(customCandidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelCustomCandidate{
		CandidateID: customCandidate.ID, EncryptedBaseURL: encryptedBaseURL, CredentialVersion: credentialVersion,
		BaseURLSummary: virtualmodelservice.SummarizeCustomBaseURL(mustParseVirtualModelShareTestURL(t, plainBaseURL)),
		// 喵~防御：夹具必须带上真实存在的凭据密文，否则"导出剔除凭据"这条断言会形同虚设喵。
		EncryptedAPIKey:   encryptedAPIKey,
		APIKeyFingerprint: virtualmodelservice.CredentialFingerprint("sk-share-secret-key-1234567890"),
		RealModelName:     "gpt-4o-mini", AuthStyle: model.VirtualModelAuthBearer,
	}).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelGlobalFailureRule{
		VirtualModelID: virtualModel.ID, RuleOrder: 0, HTTPStatus: 500, Action: model.VirtualModelActionFreeze, FreezeSeconds: 30,
	}).Error)
	return virtualModel, internalCandidate.ID, customCandidate.ID, plainBaseURL
}

// mustParseVirtualModelShareTestURL 解析测试地址，解析失败直接让用例失败喵。
func mustParseVirtualModelShareTestURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, parseError := virtualmodelservice.ValidateCustomBaseURL(rawURL)
	require.NoError(t, parseError)
	return parsedURL
}

// TestBuildVirtualModelSharePayloadStripsCredentials 验证快照保留地址明文、剔除一切凭据与指纹喵。
// 这是分享功能的核心安全不变量：分享码一旦发出就收不回来，凭据绝不能进快照喵。
func TestBuildVirtualModelSharePayloadStripsCredentials(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	virtualModel, _, _, plainBaseURL := createVirtualModelShareTestModel(t, 7, "vm-share-strip")

	// 追加一个引用型自定义候选：它必须被整体省略而不是导出成半残配置喵。
	referenceUpstreamID := int64(42)
	referenceCandidate := &model.VirtualModelCandidate{
		VirtualModelID: virtualModel.ID, StableOrder: 2, SourceType: model.VirtualModelSourceCustom,
		Enabled: true, TimeoutSeconds: 60,
	}
	require.NoError(t, model.DB.Create(referenceCandidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelCustomCandidate{
		CandidateID: referenceCandidate.ID, UpstreamModelID: &referenceUpstreamID, AuthStyle: model.VirtualModelAuthBearer,
	}).Error)

	payload, omittedReferenceCandidates, buildError := buildVirtualModelSharePayload(virtualModel)
	require.NoError(t, buildError)
	require.Equal(t, 1, omittedReferenceCandidates)
	require.Len(t, payload.Candidates, 2)

	encodedPayload, marshalError := common.Marshal(payload)
	require.NoError(t, marshalError)
	payloadText := string(encodedPayload)

	// 喵~防御：凭据明文、密文和任何指纹都不得出现在分享码里喵。
	assert.NotContains(t, payloadText, "sk-share-secret-key-1234567890")
	assert.NotContains(t, payloadText, virtualmodelservice.CredentialFingerprint("sk-share-secret-key-1234567890"))
	assert.NotContains(t, payloadText, virtualmodelservice.CredentialFingerprint(plainBaseURL))
	assert.NotContains(t, payloadText, "api_key")
	assert.NotContains(t, payloadText, "fingerprint")
	assert.NotContains(t, payloadText, "upstream_model_id")
	// 上游地址明文按产品约定保留，方便导入方知道要往哪个上游补填凭据喵。
	assert.Contains(t, payloadText, plainBaseURL)
	// 自定义候选必须标记为需要导入方自行补填凭据喵。
	assert.True(t, payload.Candidates[1].RequiresCredential)
	assert.Equal(t, "gpt-4o-mini", payload.Candidates[1].RealModelName)
	// 内部候选零秘密，分组与真实模型原样导出喵。
	assert.Equal(t, "default", payload.Candidates[0].GroupName)
	assert.Equal(t, "gpt-4o", payload.Candidates[0].RealModelName)
	// 模型级全局兜底规则一并导出喵。
	require.Len(t, payload.GlobalFailureRules, 1)
	assert.Equal(t, 500, payload.GlobalFailureRules[0].HTTPStatus)
}

// TestResolveVirtualModelShareImportPlanSkipReasons 表驱动验证各类不可用候选都被跳过并给出正确原因码喵。
func TestResolveVirtualModelShareImportPlanSkipReasons(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	// 只有 default 分组下的 gpt-4o 存在可用渠道，vip 分组没有任何能力记录喵。
	seedVirtualModelShareAbility(t, "default", "gpt-4o", 1)

	testCases := []struct {
		name           string
		groupName      string
		realModelName  string
		expectedReason string
	}{
		{name: "可用候选被保留", groupName: "default", realModelName: "gpt-4o", expectedReason: ""},
		{name: "空分组被跳过", groupName: "", realModelName: "gpt-4o", expectedReason: virtualModelShareSkipGroupNotConfigured},
		{name: "auto分组被跳过", groupName: "auto", realModelName: "gpt-4o", expectedReason: virtualModelShareSkipAutoGroup},
		{name: "分组无权限被跳过", groupName: virtualModelShareUnusableGroup, realModelName: "gpt-4o", expectedReason: virtualModelShareSkipGroupNotAccessible},
		{name: "分组下无该模型被跳过", groupName: "default", realModelName: "gpt-nowhere", expectedReason: virtualModelShareSkipModelNotInGroup},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := &model.VirtualModelSharePayload{
				Version: model.VirtualModelSharePayloadVersion,
				Candidates: []model.VirtualModelShareCandidate{{
					StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true,
					TimeoutSeconds: 60, GroupName: testCase.groupName, RealModelName: testCase.realModelName,
				}},
			}
			// 调用方属于 default 分组：restricted 不在其可用分组集合内喵。
			ctx, _ := newVirtualModelShareContext(9, "default", "{}")
			plan, planError := resolveVirtualModelShareImportPlan(ctx, payload)
			require.NoError(t, planError)
			if testCase.expectedReason == "" {
				require.Len(t, plan.KeptCandidates, 1)
				assert.Empty(t, plan.SkippedCandidates)
				return
			}
			require.Empty(t, plan.KeptCandidates)
			require.Len(t, plan.SkippedCandidates, 1)
			assert.Equal(t, testCase.expectedReason, plan.SkippedCandidates[0].Reason)
			assert.Equal(t, 1, plan.SkippedCandidates[0].Order)
			// 全部候选被跳过时必须带"整份方案落空"的提示码喵。
			assert.Contains(t, plan.Warnings, virtualModelShareWarningAllCandidatesSkipped)
		})
	}
}

// TestResolveVirtualModelShareImportPlanRejectsUnsafeBaseURL 验证未通过地址策略的自定义候选被跳过喵。
func TestResolveVirtualModelShareImportPlanRejectsUnsafeBaseURL(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	payload := &model.VirtualModelSharePayload{
		Version: model.VirtualModelSharePayloadVersion,
		Candidates: []model.VirtualModelShareCandidate{{
			StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true,
			TimeoutSeconds: 60, RealModelName: "gpt-4o-mini",
			// 喵~防御：字面 IP 在生产模式下不被允许，导入时就该拦下而不是等拨号阶段兜底喵。
			BaseURL: "http://127.0.0.1:8080", RequiresCredential: true,
		}},
	}
	ctx, _ := newVirtualModelShareContext(9, "default", "{}")
	plan, planError := resolveVirtualModelShareImportPlan(ctx, payload)
	require.NoError(t, planError)
	require.Empty(t, plan.KeptCandidates)
	require.Len(t, plan.SkippedCandidates, 1)
	assert.Equal(t, virtualModelShareSkipBaseURLRejected, plan.SkippedCandidates[0].Reason)
}

// TestImportVirtualModelShareCodeCreatesDisabledCandidates 验证导入落库结果：
// 内部候选照常导入，自定义候选一律停用且不带凭据，被跳过的候选不会写入喵。
func TestImportVirtualModelShareCodeCreatesDisabledCandidates(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	// default 分组下有 gpt-4o 可用渠道，导入方只属于 default 分组喵。
	seedVirtualModelShareAbility(t, "default", "gpt-4o", 1)
	sharerModel, _, _, plainBaseURL := createVirtualModelShareTestModel(t, 7, "vm-share-import")

	// 追加一个导入方无权限分组的内部候选：必须被跳过喵。
	inaccessibleCandidate := &model.VirtualModelCandidate{
		VirtualModelID: sharerModel.ID, StableOrder: 2, SourceType: model.VirtualModelSourceInternal,
		Enabled: true, TimeoutSeconds: 60,
	}
	require.NoError(t, model.DB.Create(inaccessibleCandidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelInternalCandidate{
		CandidateID: inaccessibleCandidate.ID, GroupName: virtualModelShareUnusableGroup, RealModelName: "gpt-4o",
	}).Error)

	payload, _, buildError := buildVirtualModelSharePayload(sharerModel)
	require.NoError(t, buildError)
	encodedPayload, marshalError := common.Marshal(payload)
	require.NoError(t, marshalError)
	shareCode := &model.VirtualModelShareCode{
		Code: "SHARETESTCODE234567", OwnerUserID: 7, SourceVirtualModelID: sharerModel.ID,
		DisplayName: sharerModel.DisplayName, Payload: string(encodedPayload),
		PayloadDigest: model.VirtualModelSharePayloadDigest(string(encodedPayload)),
		Visibility:    model.VirtualModelShareVisibilityPrivate,
	}
	require.NoError(t, model.CreateVirtualModelShareCode(shareCode))

	ctx, recorder := newVirtualModelShareContext(9, "default", `{"code":"sharetestcode234567"}`)
	ImportVirtualModelShareCode(ctx)
	// 分享码大小写不敏感，规范化后应能命中喵。
	require.Equal(t, http.StatusOK, recorder.Code)

	var importResponse struct {
		Success bool `json:"success"`
		Data    struct {
			ID                     int    `json:"id"`
			NormalizedName         string `json:"normalized_name"`
			ImportedCandidateCount int    `json:"imported_candidate_count"`
			Enabled                bool   `json:"enabled"`
			SkippedCandidates      []struct {
				Order  int    `json:"order"`
				Reason string `json:"reason"`
			} `json:"skipped_candidates"`
		} `json:"data"`
	}
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &importResponse))
	require.True(t, importResponse.Success)
	// 三个候选里 vip 那个被跳过，其余两个导入喵。
	assert.Equal(t, 2, importResponse.Data.ImportedCandidateCount)
	require.Len(t, importResponse.Data.SkippedCandidates, 1)
	assert.Equal(t, virtualModelShareSkipGroupNotAccessible, importResponse.Data.SkippedCandidates[0].Reason)
	// 喵~防御：导入的模型必须默认停用，不能"贴码即打上游"喵。
	assert.False(t, importResponse.Data.Enabled)

	// 断言落库结果喵。
	importedModel := &model.VirtualModel{}
	require.NoError(t, model.DB.Where("id = ? AND owner_user_id = ?", importResponse.Data.ID, 9).First(importedModel).Error)
	assert.False(t, importedModel.Enabled)
	assert.Equal(t, 120, importedModel.TotalTimeoutSeconds)

	var importedCandidates []model.VirtualModelCandidate
	require.NoError(t, model.DB.Where("virtual_model_id = ?", importedModel.ID).Order("stable_order asc").Find(&importedCandidates).Error)
	require.Len(t, importedCandidates, 2)
	// 候选顺序被重新压紧，不留空洞喵。
	assert.Equal(t, 0, importedCandidates[0].StableOrder)
	assert.Equal(t, 1, importedCandidates[1].StableOrder)

	var importedInternal model.VirtualModelInternalCandidate
	require.NoError(t, model.DB.Where("candidate_id = ?", importedCandidates[0].ID).First(&importedInternal).Error)
	assert.Equal(t, "default", importedInternal.GroupName)
	assert.Equal(t, "gpt-4o", importedInternal.RealModelName)

	// 喵~防御：自定义候选必须停用且不带任何凭据，只保留地址好在用户补填后可用喵。
	assert.False(t, importedCandidates[1].Enabled)
	var importedCustom model.VirtualModelCustomCandidate
	require.NoError(t, model.DB.Where("candidate_id = ?", importedCandidates[1].ID).First(&importedCustom).Error)
	assert.Empty(t, importedCustom.EncryptedAPIKey)
	assert.Empty(t, importedCustom.APIKeyFingerprint)
	assert.Nil(t, importedCustom.UpstreamModelID)
	assert.Equal(t, "gpt-4o-mini", importedCustom.RealModelName)
	decryptedBaseURL, decryptError := virtualmodelservice.DecryptCredential(importedCustom.EncryptedBaseURL, importedCustom.CredentialVersion)
	require.NoError(t, decryptError)
	assert.Equal(t, plainBaseURL, decryptedBaseURL)

	// 内部候选的失败规则与模型级全局兜底规则都应被复制喵。
	var importedRules []model.VirtualModelFailureRule
	require.NoError(t, model.DB.Where("candidate_id = ?", importedCandidates[0].ID).Find(&importedRules).Error)
	require.Len(t, importedRules, 1)
	assert.Equal(t, 429, importedRules[0].HTTPStatus)
	var importedGlobalRules []model.VirtualModelGlobalFailureRule
	require.NoError(t, model.DB.Where("virtual_model_id = ?", importedModel.ID).Find(&importedGlobalRules).Error)
	require.Len(t, importedGlobalRules, 1)
	assert.Equal(t, 500, importedGlobalRules[0].HTTPStatus)

	// 导入计数与审计摘要都应被记录喵。
	reloadedShareCode := &model.VirtualModelShareCode{}
	require.NoError(t, model.DB.Where("id = ?", shareCode.ID).First(reloadedShareCode).Error)
	assert.Equal(t, 1, reloadedShareCode.ImportCount)
	var importAuditCount int64
	require.NoError(t, model.DB.Model(&model.VirtualModelAuditLog{}).
		Where("virtual_model_id = ? AND action = ?", importedModel.ID, "share_code_import").Count(&importAuditCount).Error)
	assert.Equal(t, int64(1), importAuditCount)

	// 导入方与分享者是不同用户，导入方不能看到分享者的模型喵。
	var leakedCount int64
	require.NoError(t, model.DB.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ?", sharerModel.ID, 9).Count(&leakedCount).Error)
	assert.Zero(t, leakedCount)
}

// TestImportVirtualModelShareCodeRejectsWhenNothingImportable 验证候选全不可用时整体拒绝导入喵。
func TestImportVirtualModelShareCodeRejectsWhenNothingImportable(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	payload := &model.VirtualModelSharePayload{
		Version: model.VirtualModelSharePayloadVersion,
		Candidates: []model.VirtualModelShareCandidate{{
			StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true,
			TimeoutSeconds: 60, GroupName: virtualModelShareUnusableGroup, RealModelName: "gpt-4o",
		}},
	}
	encodedPayload, marshalError := common.Marshal(payload)
	require.NoError(t, marshalError)
	shareCode := &model.VirtualModelShareCode{
		Code: "NOTHINGIMPORTABLE234", OwnerUserID: 7, SourceVirtualModelID: 1,
		DisplayName: "空方案", Payload: string(encodedPayload), Visibility: model.VirtualModelShareVisibilityPrivate,
	}
	require.NoError(t, model.CreateVirtualModelShareCode(shareCode))

	ctx, recorder := newVirtualModelShareContext(9, "default", `{"code":"NOTHINGIMPORTABLE234"}`)
	ImportVirtualModelShareCode(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	// 喵~防御：被拒绝的导入不得留下任何虚拟模型记录喵。
	var createdCount int64
	require.NoError(t, model.DB.Model(&model.VirtualModel{}).Where("owner_user_id = ?", 9).Count(&createdCount).Error)
	assert.Zero(t, createdCount)
}

// TestLoadVirtualModelSharePayloadRejectsUnusableCodes 验证不存在、已过期、次数用尽的分享码都被拒绝喵。
// 撤销不在本用例范围内：撤销是硬删除，被撤销的码直接归入"不存在"这一档喵。
func TestLoadVirtualModelSharePayloadRejectsUnusableCodes(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	validPayload := &model.VirtualModelSharePayload{
		Version: model.VirtualModelSharePayloadVersion,
		Candidates: []model.VirtualModelShareCandidate{{
			StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true,
			TimeoutSeconds: 60, GroupName: "default", RealModelName: "gpt-4o",
		}},
	}
	encodedPayload, marshalError := common.Marshal(validPayload)
	require.NoError(t, marshalError)

	testCases := []struct {
		name         string
		code         string
		mutate       func(*model.VirtualModelShareCode)
		expectedCode int
	}{
		{name: "分享码不存在", code: "UNKNOWNCODE2345678", mutate: nil, expectedCode: http.StatusNotFound},
		{name: "分享码已过期", code: "EXPIREDCODE2345678", mutate: func(shareCode *model.VirtualModelShareCode) {
			shareCode.ExpiresAt = common.GetTimestamp() - 60
		}, expectedCode: http.StatusGone},
		{name: "分享码次数用尽", code: "EXHAUSTEDCODE23456", mutate: func(shareCode *model.VirtualModelShareCode) {
			shareCode.MaxImports = 2
			shareCode.ImportCount = 2
		}, expectedCode: http.StatusGone},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.mutate != nil {
				shareCode := &model.VirtualModelShareCode{
					Code: testCase.code, OwnerUserID: 7, SourceVirtualModelID: 1,
					DisplayName: "方案", Payload: string(encodedPayload), Visibility: model.VirtualModelShareVisibilityPrivate,
				}
				testCase.mutate(shareCode)
				require.NoError(t, model.CreateVirtualModelShareCode(shareCode))
			}
			ctx, recorder := newVirtualModelShareContext(9, "default", "{}")
			_, _, ok := loadVirtualModelSharePayload(ctx, testCase.code)
			assert.False(t, ok)
			assert.Equal(t, testCase.expectedCode, recorder.Code)
		})
	}
}

// TestDeleteVirtualModelShareCodeIsOwnerScoped 验证删除只能作用于本人分享码，
// 且删除是硬删除——行真的从表里消失，再拿这枚码导入会得到"不存在"喵。
func TestDeleteVirtualModelShareCodeIsOwnerScoped(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	shareCode := &model.VirtualModelShareCode{
		Code: "OWNERSCOPED23456789", OwnerUserID: 7, SourceVirtualModelID: 1,
		DisplayName: "方案", Payload: `{"version":1,"candidates":[]}`,
		Visibility: model.VirtualModelShareVisibilityPrivate,
	}
	require.NoError(t, model.CreateVirtualModelShareCode(shareCode))

	// 他人删除必须失败，且不能影响这条记录喵。
	assert.ErrorIs(t, model.DeleteVirtualModelShareCodeByOwner(8, shareCode.ID), gorm.ErrRecordNotFound)
	var survivedCount int64
	require.NoError(t, model.DB.Unscoped().Model(&model.VirtualModelShareCode{}).Where("id = ?", shareCode.ID).Count(&survivedCount).Error)
	assert.Equal(t, int64(1), survivedCount)

	// 本人删除成功，重复删除按未找到处理（幂等）喵。
	require.NoError(t, model.DeleteVirtualModelShareCodeByOwner(7, shareCode.ID))
	assert.ErrorIs(t, model.DeleteVirtualModelShareCodeByOwner(7, shareCode.ID), gorm.ErrRecordNotFound)

	// 喵~防御：必须用 Unscoped 复查，才能证明是硬删除而不是软删除留了行喵。
	var remainingCount int64
	require.NoError(t, model.DB.Unscoped().Model(&model.VirtualModelShareCode{}).Where("id = ?", shareCode.ID).Count(&remainingCount).Error)
	assert.Zero(t, remainingCount)

	// 删除后的分享码立刻不可再导入喵。
	ctx, _ := newVirtualModelShareContext(9, "default", "{}")
	_, _, loaded := loadVirtualModelSharePayload(ctx, shareCode.Code)
	assert.False(t, loaded)
}

// TestCreateVirtualModelShareCodeRejectsPlanWithoutCandidate 验证快照里一条候选都没有时禁止生成分享码喵。
// 引用型自定义候选因归属问题会被整体省略，所以"只有引用型候选"的模型正好命中这个分支喵。
func TestCreateVirtualModelShareCodeRejectsPlanWithoutCandidate(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	virtualModel := &model.VirtualModel{
		OwnerUserID: 7, NormalizedName: "vm-share-empty", DisplayName: "空方案",
		Enabled: true, TotalTimeoutSeconds: 120, MaxLoopRounds: 1, CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(virtualModel).Error)
	referenceUpstreamID := int64(42)
	candidate := &model.VirtualModelCandidate{
		VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceCustom,
		Enabled: true, TimeoutSeconds: 60,
	}
	require.NoError(t, model.DB.Create(candidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelCustomCandidate{
		CandidateID: candidate.ID, UpstreamModelID: &referenceUpstreamID, AuthStyle: model.VirtualModelAuthBearer,
	}).Error)

	ctx, recorder := newVirtualModelShareContext(7, "default", fmt.Sprintf(`{"virtual_model_id":%d}`, virtualModel.ID))
	CreateVirtualModelShareCode(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	// 喵~防御：被拒绝的分享不能留下任何分享码记录喵。
	var shareCodeCount int64
	require.NoError(t, model.DB.Model(&model.VirtualModelShareCode{}).Where("owner_user_id = ?", 7).Count(&shareCodeCount).Error)
	assert.Zero(t, shareCodeCount)
}

// TestResolveVirtualModelShareImportNameSuffixesOnConflict 验证未指定名称时自动追加序号避开冲突喵。
func TestResolveVirtualModelShareImportNameSuffixesOnConflict(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	// 预先占用基础名，导入时应自动改用带 -2 后缀的名称喵。
	require.NoError(t, model.DB.Create(&model.VirtualModel{
		OwnerUserID: 9, NormalizedName: "imported-plan", DisplayName: "已存在的方案", CreatedTime: common.GetTimestamp(),
	}).Error)

	ctx, _ := newVirtualModelShareContext(9, "default", "{}")
	payload := &model.VirtualModelSharePayload{DisplayName: "imported plan"}
	normalizedName, displayName, resolveError := resolveVirtualModelShareImportName(ctx, virtualModelShareCodeImportInput{}, payload)
	require.NoError(t, resolveError)
	assert.Equal(t, "imported-plan-2", normalizedName)
	assert.Equal(t, "imported plan", displayName)

	// 显式指定已占用的名称时必须返回冲突而不是静默改名喵。
	_, _, conflictError := resolveVirtualModelShareImportName(ctx, virtualModelShareCodeImportInput{NormalizedName: "imported-plan"}, payload)
	assert.ErrorIs(t, conflictError, errVirtualModelShareNameConflict)

	// 软删除的行仍占用唯一索引，因此也不允许复用该名称喵。
	softDeleted := &model.VirtualModel{OwnerUserID: 9, NormalizedName: "gone-plan", DisplayName: "已删除", CreatedTime: common.GetTimestamp()}
	require.NoError(t, model.DB.Create(softDeleted).Error)
	require.NoError(t, model.DB.Delete(&model.VirtualModel{}, softDeleted.ID).Error)
	_, _, softDeleteConflictError := resolveVirtualModelShareImportName(ctx, virtualModelShareCodeImportInput{NormalizedName: "gone-plan"}, payload)
	assert.ErrorIs(t, softDeleteConflictError, errVirtualModelShareNameConflict)
}

// TestValidateVirtualModelSharePayloadRejectsMalformedSnapshots 验证畸形快照被拒绝喵。
func TestValidateVirtualModelSharePayloadRejectsMalformedSnapshots(t *testing.T) {
	validCandidate := model.VirtualModelShareCandidate{
		StableOrder: 0, SourceType: model.VirtualModelSourceInternal, TimeoutSeconds: 60,
		GroupName: "default", RealModelName: "gpt-4o",
	}
	testCases := []struct {
		name        string
		payload     *model.VirtualModelSharePayload
		expectError bool
	}{
		{name: "版本不匹配", payload: &model.VirtualModelSharePayload{Version: 99, Candidates: []model.VirtualModelShareCandidate{validCandidate}}, expectError: true},
		{name: "候选为空", payload: &model.VirtualModelSharePayload{Version: model.VirtualModelSharePayloadVersion}, expectError: true},
		{name: "候选来源无效", payload: &model.VirtualModelSharePayload{Version: model.VirtualModelSharePayloadVersion, Candidates: []model.VirtualModelShareCandidate{{
			StableOrder: 0, SourceType: "unknown", TimeoutSeconds: 60,
		}}}, expectError: true},
		{name: "内部候选缺真实模型", payload: &model.VirtualModelSharePayload{Version: model.VirtualModelSharePayloadVersion, Candidates: []model.VirtualModelShareCandidate{{
			StableOrder: 0, SourceType: model.VirtualModelSourceInternal, TimeoutSeconds: 60, GroupName: "default",
		}}}, expectError: true},
		{name: "失败规则超上限", payload: &model.VirtualModelSharePayload{Version: model.VirtualModelSharePayloadVersion, Candidates: []model.VirtualModelShareCandidate{{
			StableOrder: 0, SourceType: model.VirtualModelSourceInternal, TimeoutSeconds: 60, GroupName: "default", RealModelName: "gpt-4o",
			FailureRules: make([]model.VirtualModelShareFailureRule, virtualModelShareMaxFailureRulesPerCandidate+1),
		}}}, expectError: true},
		{name: "合法快照通过", payload: &model.VirtualModelSharePayload{Version: model.VirtualModelSharePayloadVersion, Candidates: []model.VirtualModelShareCandidate{validCandidate}}, expectError: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			validateError := validateVirtualModelSharePayload(testCase.payload)
			if testCase.expectError {
				assert.Error(t, validateError)
				return
			}
			assert.NoError(t, validateError)
		})
	}
}

// TestVirtualModelShareFixtureAndCodeValue 固定住两件夹具假设，避免用例悄悄测错方向喵：
// 1) 默认可用分组集合含 default 且不含 restricted，这是"无权限分组"用例成立的前提喵。
// 2) 分享码生成器输出固定长度的 base32 串，且规范化后大小写差异被抹平喵。
func TestVirtualModelShareFixtureAndCodeValue(t *testing.T) {
	setupVirtualModelShareTestDB(t)
	usableGroups := service.GetUserUsableGroupsForUser(9, "default")
	_, hasDefaultGroup := usableGroups["default"]
	assert.True(t, hasDefaultGroup)
	_, hasRestrictedGroup := usableGroups[virtualModelShareUnusableGroup]
	assert.False(t, hasRestrictedGroup)

	generatedCode, generateError := model.GenerateVirtualModelShareCodeValue()
	require.NoError(t, generateError)
	// base32 编码 20 字节得到 32 个字符喵。
	assert.Len(t, generatedCode, 32)
	assert.Equal(t, generatedCode, model.NormalizeVirtualModelShareCode("  "+strings.ToLower(generatedCode)+"  "))
}

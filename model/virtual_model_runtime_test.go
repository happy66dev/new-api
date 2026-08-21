package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestVirtualModelRuntimeSnapshotQueries 验证候选、规则和手动冻结运行时快照的隔离与排序喵。
func TestVirtualModelRuntimeSnapshotQueries(t *testing.T) {
	// 使用独立内存数据库，避免运行时快照测试污染全局连接喵。
	database, openError := gorm.Open(sqlite.Open("file:virtual-model-runtime-snapshot-test?mode=memory&cache=shared"), &gorm.Config{})
	// 喵~防御：数据库初始化失败时立即终止测试，避免后续断言产生假阳性喵。
	require.NoError(t, openError)
	// 测试结束后关闭临时数据库连接，避免资源泄漏喵。
	t.Cleanup(func() {
		sqlDatabase, closeError := database.DB()
		if closeError == nil {
			_ = sqlDatabase.Close()
		}
	})
	// 创建运行时快照查询涉及的最小表集合喵。
	require.NoError(t, database.AutoMigrate(&VirtualModelCandidate{}, &VirtualModelInternalCandidate{}, &VirtualModelCustomCandidate{}, &VirtualModelFailureRule{}, &VirtualModelManualFreeze{}, &VirtualModelCustomFreezeState{}))
	// 创建顺序靠前的内部候选及运行参数喵。
	internalCandidate := VirtualModelCandidate{VirtualModelID: 301, StableOrder: 0, SourceType: VirtualModelSourceInternal, Enabled: true, MaxRetries: 0, TimeoutSeconds: 30}
	require.NoError(t, database.Create(&internalCandidate).Error)
	require.NoError(t, database.Create(&VirtualModelInternalCandidate{CandidateID: internalCandidate.ID, GroupName: "default", RealModelName: "gpt-test"}).Error)
	// 创建顺序靠后的自定义候选并写入加密配置摘要喵。
	customCandidate := VirtualModelCandidate{VirtualModelID: 301, StableOrder: 1, SourceType: VirtualModelSourceCustom, Enabled: true, MaxRetries: 2, TimeoutSeconds: 45}
	require.NoError(t, database.Create(&customCandidate).Error)
	require.NoError(t, database.Create(&VirtualModelCustomCandidate{CandidateID: customCandidate.ID, EncryptedBaseURL: "url-cipher", EncryptedAPIKey: "key-cipher", BaseURLFingerprint: "url-digest", APIKeyFingerprint: "key-digest", CredentialVersion: 1, RealModelName: "custom-test", AuthStyle: VirtualModelAuthBearer}).Error)
	// 写入乱序规则，验证查询按照 candidate 和 rule_order 排序喵。
	require.NoError(t, database.Create(&VirtualModelFailureRule{CandidateID: customCandidate.ID, RuleOrder: 2, Action: VirtualModelActionNext}).Error)
	require.NoError(t, database.Create(&VirtualModelFailureRule{CandidateID: customCandidate.ID, RuleOrder: 1, Action: VirtualModelActionFreeze}).Error)
	// 写入有效、过期和未来开始的手动冻结，验证仅已开始且未到期的冻结阻断候选喵。
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: customCandidate.ID, OperatorID: 9, StartedAt: 10, ExpiresAt: 101}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: internalCandidate.ID, OperatorID: 9, StartedAt: 10, ExpiresAt: 99}).Error)
	futureCandidate := VirtualModelCandidate{VirtualModelID: 301, StableOrder: 2, SourceType: VirtualModelSourceInternal, Enabled: true, MaxRetries: 0, TimeoutSeconds: 30}
	require.NoError(t, database.Create(&futureCandidate).Error)
	require.NoError(t, database.Create(&VirtualModelInternalCandidate{CandidateID: futureCandidate.ID, GroupName: "default", RealModelName: "future-test"}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: futureCandidate.ID, OperatorID: 9, StartedAt: 200, ExpiresAt: 300}).Error)
	// 查询候选快照必须保留稳定顺序、内部目标和自定义密文摘要喵。
	candidateSnapshots, snapshotsError := GetEnabledVirtualModelCandidateSnapshotsWithDB(database, 301)
	require.NoError(t, snapshotsError)
	require.Len(t, candidateSnapshots, 3)
	require.Equal(t, internalCandidate.ID, candidateSnapshots[0].CandidateID)
	require.Equal(t, "default", candidateSnapshots[0].GroupName)
	require.Equal(t, customCandidate.ID, candidateSnapshots[1].CandidateID)
	require.Equal(t, "url-cipher", candidateSnapshots[1].EncryptedBaseURL)
	require.Equal(t, "key-digest", candidateSnapshots[1].APIKeyFingerprint)
	// 查询规则必须按 rule_order 返回，保证第一条命中语义稳定喵。
	failureRulesByCandidateID, rulesError := GetVirtualModelFailureRulesByCandidateIDsWithDB(database, []int{customCandidate.ID})
	require.NoError(t, rulesError)
	require.Len(t, failureRulesByCandidateID[customCandidate.ID], 2)
	require.Equal(t, 1, failureRulesByCandidateID[customCandidate.ID][0].RuleOrder)
	// 同一事务读取的执行快照必须同时携带有序候选与同一时刻的规则集合喵。
	originalDatabase := DB
	DB = database
	t.Cleanup(func() {
		DB = originalDatabase
	})
	executionSnapshot, executionSnapshotError := GetVirtualModelExecutionSnapshot(301)
	require.NoError(t, executionSnapshotError)
	require.Len(t, executionSnapshot.Candidates, 3)
	require.Len(t, executionSnapshot.FailureRulesByCandidateID[customCandidate.ID], 2)
	// 当前时间为一百时，只有自定义候选应位于有效手动冻结集合喵。
	frozenCandidateIDs, freezeError := GetActiveVirtualModelManualFreezeCandidateIDsWithDB(database, []int{internalCandidate.ID, customCandidate.ID, futureCandidate.ID}, 100)
	require.NoError(t, freezeError)
	require.False(t, frozenCandidateIDs[internalCandidate.ID])
	require.True(t, frozenCandidateIDs[customCandidate.ID])
	require.False(t, frozenCandidateIDs[futureCandidate.ID])
	// 写入自动冻结状态后，只有相同 owner 和身份摘要的活动状态可以被查询到喵。
	require.NoError(t, UpsertVirtualModelCustomFreezeStateWithDB(database, 7, "identity-one", 200, "rate_limited", 100))
	automaticFreezeStates, automaticFreezeError := GetVirtualModelCustomFreezeStatesWithDB(database, 7, []string{"identity-one", "identity-other"}, 100)
	require.NoError(t, automaticFreezeError)
	require.Equal(t, int64(200), automaticFreezeStates["identity-one"].FrozenUntil)
	require.Equal(t, 1, automaticFreezeStates["identity-one"].ConsecutiveFails)
	// 成功候选清理后，自动冻结查询不得继续返回已恢复身份喵。
	require.NoError(t, ClearVirtualModelCustomFreezeStateWithDB(database, 7, "identity-one", 101))
	automaticFreezeStates, automaticFreezeError = GetVirtualModelCustomFreezeStatesWithDB(database, 7, []string{"identity-one"}, 101)
	require.NoError(t, automaticFreezeError)
	require.Empty(t, automaticFreezeStates)
}

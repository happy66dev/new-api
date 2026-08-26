package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVirtualModelCandidateSnapshotCarriesUpstreamModelID 验证候选快照携带用户上游模型引用喵。
func TestVirtualModelCandidateSnapshotCarriesUpstreamModelID(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModel{}, &VirtualModelCandidate{}, &VirtualModelInternalCandidate{}, &VirtualModelCustomCandidate{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_models").Error)
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_candidates").Error)
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_custom_candidates").Error)

	now := time.Now().Unix()
	// 构造引用用户上游模型的 custom 候选喵。
	virtualModel := &VirtualModel{OwnerUserID: 7, NormalizedName: "ref-vm", DisplayName: "Ref VM", Enabled: true, LoopEnabled: false, TotalTimeoutSeconds: 120, MaxLoopRounds: 1, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, DB.Create(virtualModel).Error)
	candidate := &VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: VirtualModelSourceCustom, Enabled: true, MaxRetries: 1, TimeoutSeconds: 60, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, DB.Create(candidate).Error)
	upstreamModelID := int64(999)
	require.NoError(t, DB.Create(&VirtualModelCustomCandidate{CandidateID: candidate.ID, EncryptedBaseURL: "enc-url", EncryptedAPIKey: "enc-key", RealModelName: "gpt-4o", AuthStyle: VirtualModelAuthBearer, UpstreamModelID: &upstreamModelID}).Error)

	// 快照组装应原样带出引用编号，供执行层按条目加载凭据喵。
	snapshots, err := GetEnabledVirtualModelCandidateSnapshotsWithDB(DB, virtualModel.ID)
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.NotNil(t, snapshots[0].UpstreamModelID)
	assert.Equal(t, int64(999), *snapshots[0].UpstreamModelID)
	assert.Equal(t, "gpt-4o", snapshots[0].RealModelName)

	// 未配置引用的直填候选，快照引用字段保持 nil 喵。
	candidateNoRef := &VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 1, SourceType: VirtualModelSourceCustom, Enabled: true, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, DB.Create(candidateNoRef).Error)
	require.NoError(t, DB.Create(&VirtualModelCustomCandidate{CandidateID: candidateNoRef.ID, EncryptedBaseURL: "enc-url2", EncryptedAPIKey: "enc-key2", RealModelName: "gpt-4o-mini", AuthStyle: VirtualModelAuthBearer}).Error)
	snapshots, err = GetEnabledVirtualModelCandidateSnapshotsWithDB(DB, virtualModel.ID)
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	assert.Nil(t, snapshots[1].UpstreamModelID)
}

package virtualmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// CustomCandidateIdentityDigest 生成同一用户内共享自动冻结状态的不可逆候选身份摘要喵。
func CustomCandidateIdentityDigest(candidate model.VirtualModelCandidateSnapshot) string {
	// 喵~防御：优先使用持久化的稳定 Base URL 摘要；历史记录缺失时退回公开摘要而绝不使用随机 GCM 密文喵。
	baseURLIdentity := strings.TrimSpace(candidate.BaseURLFingerprint)
	if baseURLIdentity == "" {
		baseURLIdentity = strings.TrimSpace(candidate.BaseURLSummary)
	}
	identityMaterial := strings.Join([]string{
		baseURLIdentity,
		strings.TrimSpace(candidate.APIKeyFingerprint),
		strings.TrimSpace(candidate.RealModelName),
		string(candidate.AuthStyle),
	}, "\n")
	identityHash := sha256.Sum256([]byte(identityMaterial))
	return hex.EncodeToString(identityHash[:])
}

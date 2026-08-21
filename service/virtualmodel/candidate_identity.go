package virtualmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// CustomCandidateIdentityDigest 生成同一用户内共享自动冻结状态的不可逆候选身份摘要喵。
func CustomCandidateIdentityDigest(candidate model.VirtualModelCandidateSnapshot) string {
	// 喵~防御：身份只基于已加密或不可逆字段，禁止解密或拼接 API Key 明文喵。
	identityMaterial := strings.Join([]string{
		strings.TrimSpace(candidate.EncryptedBaseURL),
		strings.TrimSpace(candidate.APIKeyFingerprint),
		strings.TrimSpace(candidate.RealModelName),
		string(candidate.AuthStyle),
	}, "\n")
	identityHash := sha256.Sum256([]byte(identityMaterial))
	return hex.EncodeToString(identityHash[:])
}

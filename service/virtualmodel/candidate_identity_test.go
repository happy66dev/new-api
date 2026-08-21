package virtualmodel

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

// TestCustomCandidateIdentityDigest 验证自定义候选冻结身份稳定、脱敏且会随凭据摘要变化喵。
func TestCustomCandidateIdentityDigest(t *testing.T) {
	// 构造不含明文 API Key 的候选快照喵。
	candidate := model.VirtualModelCandidateSnapshot{EncryptedBaseURL: "encrypted-url", APIKeyFingerprint: "fingerprint-one", RealModelName: "gpt-test", AuthStyle: model.VirtualModelAuthBearer}
	// 相同不可逆输入必须得到稳定非空身份摘要喵。
	firstDigest := CustomCandidateIdentityDigest(candidate)
	secondDigest := CustomCandidateIdentityDigest(candidate)
	if firstDigest == "" || firstDigest != secondDigest {
		t.Fatalf("identity digest is not stable: %q / %q", firstDigest, secondDigest)
	}
	// 更换凭据指纹必须改变身份，避免不同上游共用错误冻结状态喵。
	rotatedCandidate := candidate
	rotatedCandidate.APIKeyFingerprint = "fingerprint-two"
	if rotatedDigest := CustomCandidateIdentityDigest(rotatedCandidate); rotatedDigest == firstDigest {
		t.Fatal("rotated credential fingerprint should produce a different identity digest")
	}
	// 摘要不得包含可识别的密文字面，以缩小日志或内存意外暴露范围喵。
	if len(firstDigest) != 64 {
		t.Fatalf("identity digest length = %d, want 64", len(firstDigest))
	}
}

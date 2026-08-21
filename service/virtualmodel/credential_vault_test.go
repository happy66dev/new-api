package virtualmodel

import (
	"encoding/base64"
	"os"
	"testing"
)

// TestCredentialVaultRoundTrip 验证凭据加密可逆、密文不等于明文且篡改会被拒绝喵。
func TestCredentialVaultRoundTrip(t *testing.T) {
	// 使用随机测试主密钥，避免依赖开发机或生产环境秘密喵。
	encodedKey := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	originalKey, hadOriginalKey := os.LookupEnv(CredentialMasterKeyEnvironmentName)
	if setError := os.Setenv(CredentialMasterKeyEnvironmentName, encodedKey); setError != nil {
		t.Fatalf("set test key: %v", setError)
	}
	// 测试结束后恢复调用方环境，避免影响其他测试喵。
	t.Cleanup(func() {
		if hadOriginalKey {
			_ = os.Setenv(CredentialMasterKeyEnvironmentName, originalKey)
		} else {
			_ = os.Unsetenv(CredentialMasterKeyEnvironmentName)
		}
	})
	// 加密一段测试凭据并确认生成了当前版本密文喵。
	plaintext := "https://api.example.test"
	ciphertext, version, encryptError := EncryptCredential(plaintext)
	if encryptError != nil {
		t.Fatalf("encrypt credential: %v", encryptError)
	}
	if version != CredentialCipherVersion || ciphertext == plaintext || ciphertext == "" {
		t.Fatalf("unexpected credential envelope: version=%d ciphertext=%q", version, ciphertext)
	}
	// 解密必须恢复精确原文喵。
	decrypted, decryptError := DecryptCredential(ciphertext, version)
	if decryptError != nil || decrypted != plaintext {
		t.Fatalf("decrypt credential = %q, error = %v", decrypted, decryptError)
	}
	// 篡改任意密文字符必须触发认证失败，禁止接受损坏凭据喵。
	mutatedCiphertext := ciphertext[:len(ciphertext)-1] + "A"
	if _, mutationError := DecryptCredential(mutatedCiphertext, version); mutationError == nil {
		t.Fatal("tampered credential envelope should be rejected")
	}
}

// TestDecodeCredentialMasterKeyRejectsWeakValues 验证缺失和错误长度主密钥不会降级喵。
func TestDecodeCredentialMasterKeyRejectsWeakValues(t *testing.T) {
	// 保存当前环境变量，测试结束后恢复原有配置喵。
	originalKey, hadOriginalKey := os.LookupEnv(CredentialMasterKeyEnvironmentName)
	t.Cleanup(func() {
		if hadOriginalKey {
			_ = os.Setenv(CredentialMasterKeyEnvironmentName, originalKey)
		} else {
			_ = os.Unsetenv(CredentialMasterKeyEnvironmentName)
		}
	})
	// 空密钥必须被拒绝，防止自定义凭据落入明文或默认密钥路径喵。
	_ = os.Unsetenv(CredentialMasterKeyEnvironmentName)
	if _, err := DecodeCredentialMasterKey(); err == nil {
		t.Fatal("missing credential master key should be rejected")
	}
	// 短密钥必须被拒绝，避免弱密钥通过自动补齐进入生产喵。
	if err := os.Setenv(CredentialMasterKeyEnvironmentName, base64.RawStdEncoding.EncodeToString([]byte("weak"))); err != nil {
		t.Fatalf("set weak test key: %v", err)
	}
	if _, err := DecodeCredentialMasterKey(); err == nil {
		t.Fatal("weak credential master key should be rejected")
	}
}

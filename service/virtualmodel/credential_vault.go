package virtualmodel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// CredentialMasterKeyEnvironmentName 保存专用虚拟模型凭据主密钥的环境变量名称喵。
const CredentialMasterKeyEnvironmentName = "VIRTUAL_MODEL_CREDENTIAL_MASTER_KEY"

// CredentialCipherVersion 标识当前密文封装格式，便于未来安全轮换喵。
const CredentialCipherVersion = 1

// DecodeCredentialMasterKey 从专用环境变量读取严格的 32 字节 AES 主密钥喵。
func DecodeCredentialMasterKey() ([]byte, error) {
	// 喵~防御：缺少专用密钥时拒绝处理自定义上游，绝不降级复用会话或登录密钥喵。
	configuredKey := strings.TrimSpace(os.Getenv(CredentialMasterKeyEnvironmentName))
	if configuredKey == "" {
		return nil, errors.New("virtual model credential master key is not configured")
	}
	decodedKey, decodeError := base64.RawStdEncoding.DecodeString(configuredKey)
	if decodeError != nil {
		decodedKey, decodeError = base64.StdEncoding.DecodeString(configuredKey)
	}
	if decodeError != nil {
		decodedKey, decodeError = hex.DecodeString(configuredKey)
	}
	// 喵~防御：只接受可验证的编码密钥，禁止把任意短文本自动哈希成弱配置喵。
	if decodeError != nil || len(decodedKey) != 32 {
		return nil, errors.New("virtual model credential master key must encode exactly 32 bytes")
	}
	return decodedKey, nil
}

// EncryptCredential 使用 AES-256-GCM 和随机 nonce 加密单个敏感字段喵。
func EncryptCredential(plaintext string) (string, int, error) {
	// 喵~防御：空秘密通常代表漏填凭据，拒绝保存而不是产生不可执行配置喵。
	if strings.TrimSpace(plaintext) == "" {
		return "", 0, errors.New("credential must not be empty")
	}
	masterKey, keyError := DecodeCredentialMasterKey()
	if keyError != nil {
		return "", 0, keyError
	}
	block, blockError := aes.NewCipher(masterKey)
	if blockError != nil {
		return "", 0, fmt.Errorf("create credential cipher: %w", blockError)
	}
	gcm, gcmError := cipher.NewGCM(block)
	if gcmError != nil {
		return "", 0, fmt.Errorf("create credential gcm: %w", gcmError)
	}
	nonce := make([]byte, gcm.NonceSize())
	// 喵~防御：每次加密必须产生独立随机 nonce，避免 GCM nonce 重用泄露明文关系喵。
	if _, randomError := io.ReadFull(rand.Reader, nonce); randomError != nil {
		return "", 0, fmt.Errorf("generate credential nonce: %w", randomError)
	}
	sealedValue := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	encodedEnvelope := append(nonce, sealedValue...)
	return base64.RawStdEncoding.EncodeToString(encodedEnvelope), CredentialCipherVersion, nil
}

// DecryptCredential 解密由 EncryptCredential 生成的专用凭据密文喵。
func DecryptCredential(encodedEnvelope string, credentialVersion int) (string, error) {
	// 喵~防御：未知版本或空密文不尝试猜测格式，避免静默错误地使用秘密喵。
	if credentialVersion != CredentialCipherVersion || strings.TrimSpace(encodedEnvelope) == "" {
		return "", errors.New("unsupported or empty credential envelope")
	}
	masterKey, keyError := DecodeCredentialMasterKey()
	if keyError != nil {
		return "", keyError
	}
	envelope, decodeError := base64.RawStdEncoding.DecodeString(encodedEnvelope)
	if decodeError != nil {
		return "", errors.New("invalid credential envelope encoding")
	}
	block, blockError := aes.NewCipher(masterKey)
	if blockError != nil {
		return "", fmt.Errorf("create credential cipher: %w", blockError)
	}
	gcm, gcmError := cipher.NewGCM(block)
	if gcmError != nil {
		return "", fmt.Errorf("create credential gcm: %w", gcmError)
	}
	// 喵~防御：密文必须至少含 nonce 与 GCM authentication tag，避免切片越界和篡改输入喵。
	if len(envelope) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("credential envelope is too short")
	}
	nonce := envelope[:gcm.NonceSize()]
	ciphertext := envelope[gcm.NonceSize():]
	plaintext, openError := gcm.Open(nil, nonce, ciphertext, nil)
	if openError != nil {
		return "", errors.New("credential envelope authentication failed")
	}
	return string(plaintext), nil
}

// CredentialFingerprint 返回不可逆摘要，用于识别同一凭据但不存储明文喵。
func CredentialFingerprint(secret string) string {
	// 喵~防御：空值返回空摘要，避免把缺失凭据和真实凭据混同喵。
	if secret == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

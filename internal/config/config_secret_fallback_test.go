package config

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// encryptWithKey 用指定密钥做 AES-256-GCM 加密，输出与 encryptConfigSecret 相同格式的密文。
// 仅用于测试构造旧版本密钥加密的密文。
func encryptWithKey(t *testing.T, plain string, key []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("构造 cipher 失败: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("构造 GCM 失败: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		t.Fatalf("生成 nonce 失败: %v", err)
	}
	sealed := aead.Seal(nil, nonce, []byte(plain), nil)
	payload := append(nonce, sealed...)
	return encryptedConfigValuePrefix + base64.RawStdEncoding.EncodeToString(payload)
}

// TestDecryptConfigSecretFallbackChain 验证解密回退链 v3 → v2 → v1：
// 旧版本密钥（含 hostname 的 v1/v2）加密的密文，必须能被当前代码解开。
// 这是"密钥去除 hostname"治本修复的向后兼容保证。
func TestDecryptConfigSecretFallbackChain(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv(rootDirEnvName, rootDir)

	const plain = "回退测试-token-🔑-123"

	// v1 旧密钥（home + hostname + rootDir，无 seed）加密的密文应可解
	v1Cipher := encryptWithKey(t, plain, configSecretKeyLegacy())
	if got, err := decryptConfigSecret(v1Cipher); err != nil || got != plain {
		t.Fatalf("v1 回退解密失败: got=%q err=%v", got, err)
	}

	// 生成 seed 文件后，v2 旧密钥（seed + home + hostname + rootDir）加密的密文应可解
	if err := ensureConfigSecretSeed(); err != nil {
		t.Fatalf("生成 seed 失败: %v", err)
	}
	v2Cipher := encryptWithKey(t, plain, configSecretKeyV2Legacy())
	if got, err := decryptConfigSecret(v2Cipher); err != nil || got != plain {
		t.Fatalf("v2 回退解密失败: got=%q err=%v", got, err)
	}

	// v3 当前密钥（seed + home + rootDir，无 hostname）加密的密文应可解
	v3Cipher := encryptWithKey(t, plain, configSecretKey())
	if got, err := decryptConfigSecret(v3Cipher); err != nil || got != plain {
		t.Fatalf("v3 解密失败: got=%q err=%v", got, err)
	}
}

// TestConfigSecretKeyV3ExcludesHostname 验证 v3 密钥派生不含 hostname：
// v3 与 v2 的唯一差异就是去掉了 hostname 因子，二者密钥必须不同；
// 且 v3 密钥不应等于任何含 hostname 的派生结果。
func TestConfigSecretKeyV3ExcludesHostname(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv(rootDirEnvName, rootDir)
	if err := ensureConfigSecretSeed(); err != nil {
		t.Fatalf("生成 seed 失败: %v", err)
	}

	v3 := configSecretKey()
	v2 := configSecretKeyV2Legacy()
	if strings.EqualFold(
		base64.RawStdEncoding.EncodeToString(v3),
		base64.RawStdEncoding.EncodeToString(v2),
	) {
		t.Fatal("v3 密钥不应等于 v2 密钥（v3 已去除 hostname 因子）")
	}
}

// TestDecryptConfigSecretCorrupted 验证损坏密文返回明确错误而非 panic。
func TestDecryptConfigSecretCorrupted(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv(rootDirEnvName, rootDir)

	corrupted := encryptedConfigValuePrefix + strings.Repeat("A", 60)
	if _, err := decryptConfigSecret(corrupted); err == nil {
		t.Fatal("损坏密文应返回错误")
	}
}

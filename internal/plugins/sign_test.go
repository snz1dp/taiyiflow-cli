package plugins

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratePluginKeyPair 验证 keygen 产物：PEM 文件存在、私钥 0600、可加载、指纹 16 hex。
func TestGeneratePluginKeyPair(t *testing.T) {
	tmpDir := t.TempDir()
	result, err := GeneratePluginKeyPair(tmpDir, "test-publisher")
	if err != nil {
		t.Fatalf("生成密钥对失败: %v", err)
	}
	if result.PrivateKeyPath != filepath.Join(tmpDir, "test-publisher-private.pem") {
		t.Errorf("私钥路径不符合约定: %s", result.PrivateKeyPath)
	}
	// 私钥权限 0600（发布者保密底线）
	info, err := os.Stat(result.PrivateKeyPath)
	if err != nil {
		t.Fatalf("stat 私钥失败: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("私钥权限应为 0600，实际 %v", info.Mode().Perm())
	}
	// 指纹为 16 位 hex
	if len(result.Fingerprint) != 16 {
		t.Errorf("指纹长度应为 16，实际 %d", len(result.Fingerprint))
	}
	// 私钥可加载
	privateKey, err := LoadPluginPrivateKey(result.PrivateKeyPath)
	if err != nil {
		t.Fatalf("加载私钥失败: %v", err)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		t.Errorf("私钥长度非法: %d", len(privateKey))
	}
}

// TestGeneratePluginKeyPairDefaults 验证默认参数（name=plugin, dir=.）。
func TestGeneratePluginKeyPairDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir 失败: %v", err)
	}
	defer os.Chdir(origWd)
	result, err := GeneratePluginKeyPair("", "")
	if err != nil {
		t.Fatalf("默认参数生成失败: %v", err)
	}
	if !strings.HasSuffix(result.PrivateKeyPath, "plugin-private.pem") {
		t.Errorf("默认密钥名应为 plugin，实际 %s", result.PrivateKeyPath)
	}
}

// TestSignPluginZip 验证 pack --sign 产物：.sig 两行格式、签名可被独立验签。
func TestSignPluginZip(t *testing.T) {
	tmpDir := t.TempDir()
	// 生成密钥对
	keyResult, err := GeneratePluginKeyPair(tmpDir, "publisher")
	if err != nil {
		t.Fatalf("keygen 失败: %v", err)
	}
	// 伪 zip（签名对象为字节流，内容任意）
	zipPath := filepath.Join(tmpDir, "demo-plugin-1.0.0.zip")
	zipBytes := []byte("fake zip bytes for signing test")
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		t.Fatalf("写 zip 失败: %v", err)
	}
	// 签名
	signResult, err := SignPluginZip(zipPath, keyResult.PrivateKeyPath)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	if signResult.SignaturePath != zipPath+".sig" {
		t.Errorf("签名文件路径不符合约定: %s", signResult.SignaturePath)
	}
	// .sig 内容两行：base64 签名 + base64 SPKI 公钥
	sigContent, err := os.ReadFile(signResult.SignaturePath)
	if err != nil {
		t.Fatalf("读 .sig 失败: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(sigContent)), "\n")
	if len(lines) != 2 {
		t.Fatalf(".sig 应为两行，实际 %d 行", len(lines))
	}
	signature, err := base64.StdEncoding.DecodeString(lines[0])
	if err != nil {
		t.Fatalf("签名 base64 解码失败: %v", err)
	}
	publicKeyDer, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		t.Fatalf("公钥 base64 解码失败: %v", err)
	}
	// 独立验签（模拟宿主 verifySignature 逻辑）
	publicKey, err := x509.ParsePKIXPublicKey(publicKeyDer)
	if err != nil {
		t.Fatalf("解析公钥失败: %v", err)
	}
	if !ed25519.Verify(publicKey.(ed25519.PublicKey), zipBytes, signature) {
		t.Error("签名验证失败（应通过）")
	}
	// 篡改后验签失败
	if ed25519.Verify(publicKey.(ed25519.PublicKey), append(zipBytes, 'x'), signature) {
		t.Error("篡改后验签不应通过")
	}
}

// TestSignPluginZipInvalidKey 验证非法私钥路径报错。
func TestSignPluginZipInvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "x.zip")
	if err := os.WriteFile(zipPath, []byte("zip"), 0o644); err != nil {
		t.Fatalf("写 zip 失败: %v", err)
	}
	_, err := SignPluginZip(zipPath, filepath.Join(tmpDir, "not-exist.pem"))
	if err == nil {
		t.Error("私钥不存在应报错")
	}
}

// TestLoadPluginPrivateKeyInvalidPEM 验证非法 PEM 报错。
func TestLoadPluginPrivateKeyInvalidPEM(t *testing.T) {
	tmpDir := t.TempDir()
	pemPath := filepath.Join(tmpDir, "bad.pem")
	if err := os.WriteFile(pemPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("写坏 PEM 失败: %v", err)
	}
	if _, err := LoadPluginPrivateKey(pemPath); err == nil {
		t.Error("非法 PEM 应报错")
	}
}

// TestKeygenSignRoundTrip 验证 keygen 公钥与 .sig 内公钥一致（同一发布者身份）。
func TestKeygenSignRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	keyResult, err := GeneratePluginKeyPair(tmpDir, "publisher")
	if err != nil {
		t.Fatalf("keygen 失败: %v", err)
	}
	zipPath := filepath.Join(tmpDir, "p.zip")
	if err := os.WriteFile(zipPath, []byte("zip content"), 0o644); err != nil {
		t.Fatalf("写 zip 失败: %v", err)
	}
	signResult, err := SignPluginZip(zipPath, keyResult.PrivateKeyPath)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	// .sig 第 2 行公钥 = keygen 公钥 PEM 解出的 SPKI DER
	publicPEM, err := os.ReadFile(keyResult.PublicKeyPath)
	if err != nil {
		t.Fatalf("读公钥失败: %v", err)
	}
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		t.Fatalf("公钥 PEM 解码失败")
	}
	if base64.StdEncoding.EncodeToString(block.Bytes) != signResult.PublicKey {
		t.Error("keygen 公钥与 .sig 内公钥不一致")
	}
}

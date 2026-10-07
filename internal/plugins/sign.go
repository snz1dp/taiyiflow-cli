package plugins

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// 签名链（P5-B，架构文档 12.5）：
// - plugin keygen 生成 ed25519 密钥对（PEM 文件，私钥本地保存，公钥可公开）；
// - plugin pack --sign <私钥路径> 打包后对 zip 字节流签名，写旁置 .sig 文件
//   （第 1 行 base64 签名，第 2 行 base64 SPKI 公钥，宿主 install 验签）；
// - 宿主 plugin:install 验签 + 验 hash 双保险（防篡改防劫持）。

// KeygenResult 表示 plugin keygen 的产物信息。
type KeygenResult struct {
	PrivateKeyPath string // 私钥 PEM 文件路径（发布者本地保存，勿外传）
	PublicKeyPath  string // 公钥 PEM 文件路径（可公开，交付给用户用于信任）
	Fingerprint    string // 公钥 SHA-256 指纹（前 16 位 hex，便于人工核对）
}

// SignResult 表示 pack --sign 的签名产物信息。
type SignResult struct {
	SignaturePath string // 旁置 .sig 文件路径（base64 签名 + base64 SPKI 公钥两行）
	PublicKey     string // base64 SPKI 公钥（.sig 第 2 行，与 keygen 公钥一致）
}

// GeneratePluginKeyPair 生成 ed25519 密钥对并写 PEM 文件（plugin keygen 核心）。
//
// 参数：
//   - keyDir: 密钥输出目录（默认当前目录）
//   - name: 密钥名（文件名前缀，默认 "plugin"）
//
// 产物：{name}-private.pem / {name}-public.pem
func GeneratePluginKeyPair(keyDir string, name string) (KeygenResult, error) {
	if name == "" {
		name = "plugin"
	}
	if keyDir == "" {
		keyDir = "."
	}
	absDir, err := filepath.Abs(keyDir)
	if err != nil {
		return KeygenResult{}, fmt.Errorf("解析密钥目录失败: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return KeygenResult{}, fmt.Errorf("创建密钥目录失败: %w", err)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeygenResult{}, fmt.Errorf("生成密钥对失败: %w", err)
	}

	privateKeyPath := filepath.Join(absDir, name+"-private.pem")
	publicKeyPath := filepath.Join(absDir, name+"-public.pem")

	// 私钥 PEM（PKCS#8），权限 0600（仅发布者可读）
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return KeygenResult{}, fmt.Errorf("序列化私钥失败: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if err := os.WriteFile(privateKeyPath, privatePEM, 0o600); err != nil {
		return KeygenResult{}, fmt.Errorf("写私钥文件失败: %w", err)
	}

	// 公钥 PEM（SPKI），权限 0644（可公开分发）
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return KeygenResult{}, fmt.Errorf("序列化公钥失败: %w", err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	if err := os.WriteFile(publicKeyPath, publicPEM, 0o644); err != nil {
		return KeygenResult{}, fmt.Errorf("写公钥文件失败: %w", err)
	}

	fingerprint := fmt.Sprintf("%x", sha256.Sum256(publicDER))[:16]
	return KeygenResult{
		PrivateKeyPath: privateKeyPath,
		PublicKeyPath:  publicKeyPath,
		Fingerprint:    fingerprint,
	}, nil
}

// SignPluginZip 对 pack 产物 zip 签名并写旁置 .sig 文件（pack --sign 核心）。
//
// 签名对象为 zip 文件字节流（与宿主 verifySignature 验签对象一致）。
// .sig 文件格式（两行）：
//
//	第 1 行: base64(ed25519 签名)
//	第 2 行: base64(SPKI 公钥 DER)
//
// 参数：
//   - zipPath: pack 产物 zip 路径
//   - privateKeyPath: 发布者私钥 PEM 文件路径
func SignPluginZip(zipPath string, privateKeyPath string) (SignResult, error) {
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		return SignResult{}, fmt.Errorf("读取 zip 失败: %w", err)
	}
	privateKey, err := LoadPluginPrivateKey(privateKeyPath)
	if err != nil {
		return SignResult{}, err
	}

	signature := ed25519.Sign(privateKey, zipBytes)
	publicKeyDer, err := x509.MarshalPKIXPublicKey(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		return SignResult{}, fmt.Errorf("序列化公钥失败: %w", err)
	}

	sigContent := base64.StdEncoding.EncodeToString(signature) + "\n" +
		base64.StdEncoding.EncodeToString(publicKeyDer) + "\n"
	sigPath := zipPath + ".sig"
	if err := os.WriteFile(sigPath, []byte(sigContent), 0o644); err != nil {
		return SignResult{}, fmt.Errorf("写签名文件失败: %w", err)
	}
	return SignResult{
		SignaturePath: sigPath,
		PublicKey:     base64.StdEncoding.EncodeToString(publicKeyDer),
	}, nil
}

// LoadPluginPrivateKey 从 PEM 文件加载 ed25519 私钥。
func LoadPluginPrivateKey(privateKeyPath string) (ed25519.PrivateKey, error) {
	pemBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("读取私钥文件失败: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("私钥文件不是合法 PEM 格式: %s", privateKeyPath)
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("PEM 类型应为 PRIVATE KEY，实际 %q", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("私钥类型不是 ed25519")
	}
	return privateKey, nil
}

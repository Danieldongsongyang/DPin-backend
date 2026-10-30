package service

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type belloBuddyUpdateKeyResource struct {
	SchemaVersion int    `json:"schemaVersion"`
	Algorithm     string `json:"algorithm"`
	PublicKey     string `json:"publicKey"`
	KeyID         string `json:"keyId"`
}

type belloBuddyUpdateSignature struct {
	KeyID     string `json:"keyId"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type belloBuddyVerifiedReleaseReport struct {
	Passed   bool `json:"passed"`
	Manifest struct {
		Signing     string `json:"signing"`
		UpdateKeyID string `json:"update_key_id"`
	} `json:"manifest"`
	UpdateSignature *belloBuddyUpdateSignature `json:"dpin_update_signature"`
}

// Only the operator-configured key is trusted. A key shipped with an upload is
// useful for matching build inputs, but can never establish publication trust.
func belloBuddyReleaseUpdateKey() (belloBuddyUpdateKeyResource, ed25519.PublicKey, error) {
	var resource belloBuddyUpdateKeyResource
	filename := strings.TrimSpace(os.Getenv("DPIN_RELEASE_UPDATE_KEY_FILE"))
	if filename == "" {
		return resource, nil, errors.New("未配置可信更新公钥 DPIN_RELEASE_UPDATE_KEY_FILE")
	}
	file, err := os.Open(filename)
	if err != nil {
		return resource, nil, errors.New("无法读取可信更新公钥")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<10 {
		return resource, nil, errors.New("可信更新公钥文件无效")
	}
	data, err := io.ReadAll(io.LimitReader(file, 16<<10+1))
	if err != nil || len(data) > 16<<10 || common.Unmarshal(data, &resource) != nil || resource.SchemaVersion != 1 || resource.Algorithm != "ed25519" {
		return resource, nil, errors.New("可信更新公钥资源无效")
	}
	der, err := base64.StdEncoding.Strict().DecodeString(resource.PublicKey)
	if err != nil || base64.StdEncoding.EncodeToString(der) != resource.PublicKey {
		return resource, nil, errors.New("可信更新公钥编码无效")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	key, ok := parsed.(ed25519.PublicKey)
	if err != nil || !ok || len(key) != ed25519.PublicKeySize {
		return resource, nil, errors.New("可信更新公钥不是有效的 Ed25519 SPKI 公钥")
	}
	digest := sha256.Sum256(der)
	if resource.KeyID != hex.EncodeToString(digest[:]) {
		return resource, nil, errors.New("可信更新公钥指纹不匹配")
	}
	return resource, key, nil
}

func verifyBelloBuddyUpdateSignature(envelope *belloBuddyUpdateSignature, version string, installer model.BelloBuddyReleaseArtifact) (belloBuddyUpdateKeyResource, error) {
	resource, key, err := belloBuddyReleaseUpdateKey()
	if err != nil {
		return resource, err
	}
	if envelope == nil || envelope.KeyID != resource.KeyID || len(envelope.Payload) == 0 || len(envelope.Payload) > 16<<10 || len(envelope.Signature) > 256 {
		return resource, errors.New("更新签名或公钥指纹无效")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil || base64.StdEncoding.EncodeToString(payload) != envelope.Payload {
		return resource, errors.New("更新签名正文编码无效")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != envelope.Signature || !ed25519.Verify(key, payload, signature) {
		return resource, errors.New("安装器未通过可信 Ed25519 更新签名校验")
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal(payload, &fields) != nil || len(fields) != 7 {
		return resource, errors.New("更新签名正文必须仅包含约定的七个字段")
	}
	for _, name := range []string{"schemaVersion", "product", "appId", "version", "filename", "size", "sha512"} {
		if _, ok := fields[name]; !ok {
			return resource, errors.New("更新签名正文包含未知或缺少约定字段")
		}
	}
	var binding struct {
		SchemaVersion int    `json:"schemaVersion"`
		Product       string `json:"product"`
		AppID         string `json:"appId"`
		Version       string `json:"version"`
		Filename      string `json:"filename"`
		Size          int64  `json:"size"`
		SHA512        string `json:"sha512"`
	}
	// The signed payload has exactly these seven fields. keyId is in the outer
	// envelope and the separately pinned key resource, never an uploader choice.
	if common.Unmarshal(payload, &binding) != nil || binding.SchemaVersion != 1 || binding.Product != model.BelloBuddyReleaseProduct || binding.AppID != "local.bellobuddy.recovered" || binding.Version != version || binding.Filename != installer.Filename || binding.Size != installer.Size || binding.Size <= 0 || binding.SHA512 != installer.SHA512 {
		return resource, errors.New("更新签名未绑定当前产品、安装器名称、版本、大小或摘要")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(binding.SHA512)
	if err != nil || len(decoded) != sha512.Size || base64.StdEncoding.EncodeToString(decoded) != binding.SHA512 {
		return resource, errors.New("更新签名的 SHA512 编码无效")
	}
	return resource, nil
}

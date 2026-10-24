package service

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Payload 是原始 UTF-8 JSON 的标准 Base64；Signature 对这些原始字节签名。
// 验收私钥只保存在受信任的 Windows 验收环境，不交给上传者或 D-PIN 服务。
type BelloBuddyReleaseEvidence struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type belloBuddyReleaseAcceptancePayload struct {
	SchemaVersion        int    `json:"schema_version"`
	Product              string `json:"product"`
	Version              string `json:"version"`
	Platform             string `json:"platform"`
	Arch                 string `json:"arch"`
	InstallerSHA256      string `json:"installer_sha256"`
	VerifiedAt           int64  `json:"verified_at"`
	WindowsVersion       string `json:"windows_version"`
	MinimumSystemVersion string `json:"minimum_system_version"`
	Authenticode         struct {
		Status                string `json:"status"`
		Publisher             string `json:"publisher"`
		CertificateThumbprint string `json:"certificate_thumbprint"`
	} `json:"authenticode"`
	Checks        map[string]bool   `json:"checks"`
	CheckEvidence map[string]string `json:"check_evidence,omitempty"`
}

// VerifyBelloBuddyReleaseEvidence 校验受信任验收环境的签名及制品绑定。
// 签名说明指定验收者确认过 Windows 结果，并不表示 D-PIN 自己运行过安装器。
func VerifyBelloBuddyReleaseEvidence(evidence BelloBuddyReleaseEvidence, releaseVersion, installerSHA256, minimumSystemVersion string) (map[string]any, error) {
	publisher := strings.TrimSpace(os.Getenv("BELLOBUDDY_RELEASE_PUBLISHER"))
	if publisher == "" || len(publisher) > 2048 {
		return nil, errors.New("未配置可信 Windows 发布者 BELLOBUDDY_RELEASE_PUBLISHER")
	}
	key, err := belloBuddyReleaseVerifierKey()
	if err != nil {
		return nil, err
	}
	if len(evidence.Payload) == 0 || len(evidence.Payload) > 96*1024 || len(evidence.Signature) == 0 || len(evidence.Signature) > 2048 {
		return nil, errors.New("Windows 验收签名或报告大小无效")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(evidence.Payload)
	if err != nil || len(payload) > 64*1024 || base64.StdEncoding.EncodeToString(payload) != evidence.Payload {
		return nil, errors.New("Windows 验收报告必须为规范 Base64 JSON")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(evidence.Signature)
	if err != nil || len(signature) != key.Size() || base64.StdEncoding.EncodeToString(signature) != evidence.Signature {
		return nil, errors.New("Windows 验收签名格式无效")
	}
	digest := sha256.Sum256(payload)
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return nil, errors.New("Windows 验收报告未通过可信验收者签名校验")
	}
	var report belloBuddyReleaseAcceptancePayload
	if err := common.Unmarshal(payload, &report); err != nil {
		return nil, errors.New("Windows 验收报告 JSON 无效")
	}
	if report.SchemaVersion != 1 || report.Product != "bellobuddy" || report.Platform != "win32" || report.Arch != "x64" || report.Version != releaseVersion {
		return nil, errors.New("Windows 验收报告的产品、版本或目标不匹配")
	}
	actualHash, err := hex.DecodeString(report.InstallerSHA256)
	if err != nil || len(actualHash) != sha256.Size || report.InstallerSHA256 != strings.ToLower(report.InstallerSHA256) || report.InstallerSHA256 != installerSHA256 {
		return nil, errors.New("Windows 验收报告没有绑定当前安装器 SHA256")
	}
	if report.MinimumSystemVersion != minimumSystemVersion {
		return nil, errors.New("Windows 验收报告的最低系统版本不匹配")
	}
	minimum, err := belloBuddyWindowsVersion(minimumSystemVersion)
	if err != nil {
		return nil, fmt.Errorf("最低 Windows 系统版本无效：%w", err)
	}
	actual, err := belloBuddyWindowsVersion(report.WindowsVersion)
	if err != nil {
		return nil, fmt.Errorf("验收 Windows 系统版本无效：%w", err)
	}
	for i := range actual {
		if actual[i] > minimum[i] {
			break
		}
		if actual[i] < minimum[i] {
			return nil, errors.New("验收机器不满足最低 Windows 系统版本")
		}
	}
	now := time.Now().Unix()
	if report.VerifiedAt < now-int64(90*24*time.Hour/time.Second) || report.VerifiedAt > now+int64(5*time.Minute/time.Second) {
		return nil, errors.New("Windows 验收报告已超过 90 天或验收时间在未来")
	}
	thumbprint, err := hex.DecodeString(report.Authenticode.CertificateThumbprint)
	if report.Authenticode.Status != "Valid" || report.Authenticode.Publisher != publisher || err != nil || len(thumbprint) != sha256.Size && len(thumbprint) != 20 {
		return nil, errors.New("Windows 代码签名的有效状态、发布者或证书指纹不匹配")
	}
	for _, check := range []string{"installation", "launch", "native_tools", "data_preserved"} {
		if !report.Checks[check] {
			return nil, fmt.Errorf("Windows 验收缺少通过项：%s", check)
		}
	}
	var result map[string]any
	if err := common.Unmarshal(payload, &result); err != nil {
		return nil, errors.New("Windows 验收报告无法保存")
	}
	result["signature_verified"] = true
	result["verifier_key_sha256"] = fmt.Sprintf("%x", sha256.Sum256(x509.MarshalPKCS1PublicKey(key)))
	result["verified_by_server_at"] = now
	return result, nil
}

func belloBuddyReleaseVerifierKey() (*rsa.PublicKey, error) {
	inline := strings.TrimSpace(os.Getenv("BELLOBUDDY_RELEASE_VERIFIER_PUBLIC_KEY"))
	filename := strings.TrimSpace(os.Getenv("BELLOBUDDY_RELEASE_VERIFIER_PUBLIC_KEY_FILE"))
	if inline == "" && filename == "" {
		return nil, errors.New("未配置可信 Windows 验收公钥")
	}
	if inline != "" && filename != "" {
		return nil, errors.New("Windows 验收公钥与公钥文件只能配置一种")
	}
	keyPEM := []byte(inline)
	if filename != "" {
		file, err := os.Open(filename)
		if err != nil {
			return nil, errors.New("无法读取 Windows 验收公钥文件")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
			return nil, errors.New("Windows 验收公钥文件无效")
		}
		keyPEM, err = io.ReadAll(io.LimitReader(file, 64*1024+1))
		if err != nil {
			return nil, errors.New("无法读取 Windows 验收公钥文件")
		}
	}
	if len(keyPEM) > 64*1024 {
		return nil, errors.New("Windows 验收公钥过大")
	}
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("Windows 验收公钥 PEM 无效")
	}
	var key *rsa.PublicKey
	switch block.Type {
	case "PUBLIC KEY":
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, errors.New("Windows 验收公钥格式无效")
		}
		key, _ = parsed.(*rsa.PublicKey)
	case "RSA PUBLIC KEY":
		key, _ = x509.ParsePKCS1PublicKey(block.Bytes)
	}
	if key == nil || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 {
		return nil, errors.New("Windows 验收公钥须为 2048 至 8192 位 RSA 公钥")
	}
	return key, nil
}

func belloBuddyWindowsVersion(value string) ([4]uint64, error) {
	var version [4]uint64
	parts := strings.Split(value, ".")
	if len(parts) < 3 || len(parts) > len(version) {
		return version, errors.New("须使用 Windows 版本号，例如 10.0.19045")
	}
	for i, part := range parts {
		if part == "" || len(part) > 10 || strings.Trim(part, "0123456789") != "" {
			return version, errors.New("系统版本必须为三或四段非负整数")
		}
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return version, errors.New("系统版本数字超出范围")
		}
		version[i] = number
	}
	return version, nil
}

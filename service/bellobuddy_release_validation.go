package service

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"debug/pe"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"golang.org/x/crypto/blake2b"
	"gopkg.in/yaml.v3"
)

type belloBuddyBuilderUpdateMetadata struct {
	Version string `yaml:"version"`
	Files   []struct {
		URL          string `yaml:"url"`
		SHA512       string `yaml:"sha512"`
		Size         int64  `yaml:"size"`
		BlockMapSize int64  `yaml:"blockMapSize,omitempty"`
	} `yaml:"files"`
	Path                 string    `yaml:"path"`
	SHA512               string    `yaml:"sha512"`
	ReleaseDate          string    `yaml:"releaseDate"`
	ReleaseName          string    `yaml:"releaseName,omitempty"`
	ReleaseNotes         yaml.Node `yaml:"releaseNotes,omitempty"`
	MinimumSystemVersion string    `yaml:"minimumSystemVersion,omitempty"`
}

type belloBuddyBuildReleaseManifest struct {
	SchemaVersion            int                          `json:"schemaVersion"`
	Version                  string                       `json:"version"`
	AppID                    string                       `json:"appId"`
	ProductName              string                       `json:"productName"`
	Target                   string                       `json:"target"`
	ElectronVersion          string                       `json:"electronVersion"`
	BuiltAt                  string                       `json:"builtAt"`
	Signing                  string                       `json:"signing"`
	WindowsRuntimeVerified   *bool                        `json:"windowsRuntimeVerified"`
	InAppUpdaterIntegrated   *bool                        `json:"inAppUpdaterIntegrated"`
	ReleaseApproved          *bool                        `json:"releaseApproved"`
	BuildManifestSHA256      string                       `json:"buildManifestSha256"`
	DependencyManifestSHA256 string                       `json:"dependencyManifestSha256"`
	ElectronArchiveSHA256    string                       `json:"electronArchiveSha256"`
	UpdateKey                *belloBuddyUpdateKeyResource `json:"updateKey,omitempty"`
	UpdateSignature          *belloBuddyUpdateSignature   `json:"updateSignature,omitempty"`
	DpinConfig               struct {
		APIOrigin string `json:"apiOrigin"`
	} `json:"dpinConfig"`
	Artifacts []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"artifacts"`
}

type belloBuddyInstallerBlockmap struct {
	Version string `json:"version"`
	Files   []struct {
		Name      string   `json:"name"`
		Offset    int64    `json:"offset"`
		Checksums []string `json:"checksums"`
		Sizes     []int64  `json:"sizes"`
	} `json:"files"`
}

// ValidateBelloBuddyReleaseFiles 只读取服务器私有目录中的四份制品，不执行安装器。
// 文件层校验通过仅允许冻结为 ready，不能代替真实 Windows 安装和签名验收。
func ValidateBelloBuddyReleaseFiles(root *os.Root, release *model.BelloBuddyRelease, artifacts []model.BelloBuddyReleaseArtifact) (map[string]any, error) {
	if root == nil || release == nil || release.ID <= 0 || release.Product != model.BelloBuddyReleaseProduct || release.Platform != model.BelloBuddyReleasePlatform || release.Arch != model.BelloBuddyReleaseArch {
		return nil, errors.New("发布产品或 Windows 目标无效")
	}
	installerName := BelloBuddyReleaseFilename(release.Version, "installer")
	for _, artifact := range artifacts {
		if artifact.Kind == "installer" && belloBuddyReleaseArtifactFilenameMatches(release.Version, "installer", artifact.Filename) {
			installerName = artifact.Filename
		}
	}
	expected := map[string]string{"installer": installerName, "blockmap": installerName + ".blockmap", "metadata": "latest.yml", "manifest": "release-manifest.json"}
	limits := map[string]int64{"installer": 2 << 30, "blockmap": 16 << 20, "metadata": 256 << 10, "manifest": 2 << 20}
	if len(artifacts) != len(expected) || strings.ContainsAny(release.Version, "/\\\x00") || strings.TrimSpace(release.Version) != release.Version || release.Version == "" {
		return nil, errors.New("发布制品须包含完整四种文件，版本不得包含路径字符")
	}
	byKind := make(map[string]model.BelloBuddyReleaseArtifact, len(expected))
	files := make(map[string]*os.File, len(expected))
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	for _, artifact := range artifacts {
		name, exists := expected[artifact.Kind]
		if _, duplicate := byKind[artifact.Kind]; !exists || duplicate || artifact.Filename != name || artifact.ReleaseID != release.ID {
			return nil, errors.New("发布文件种类、文件名或版本归属不匹配")
		}
		if artifact.StorageKey == "" || path.IsAbs(artifact.StorageKey) || path.Clean(artifact.StorageKey) != artifact.StorageKey || strings.HasPrefix(artifact.StorageKey, "../") || strings.ContainsAny(artifact.StorageKey, "\\\x00") {
			return nil, errors.New("发布文件存储引用无效")
		}
		info, err := root.Lstat(artifact.StorageKey)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() != artifact.Size || info.Size() > limits[artifact.Kind] {
			return nil, fmt.Errorf("%s 文件不存在、不是普通文件或大小超限／不一致", artifact.Kind)
		}
		file, err := root.Open(artifact.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("无法读取 %s 文件", artifact.Kind)
		}
		files[artifact.Kind] = file
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return nil, fmt.Errorf("%s 文件在读取时已被替换", artifact.Kind)
		}
		hash256, hash512 := sha256.New(), sha512.New()
		count, err := io.Copy(io.MultiWriter(hash256, hash512), io.LimitReader(file, limits[artifact.Kind]+1))
		if err != nil || count != artifact.Size || hex.EncodeToString(hash256.Sum(nil)) != artifact.SHA256 || base64.StdEncoding.EncodeToString(hash512.Sum(nil)) != artifact.SHA512 {
			return nil, fmt.Errorf("%s 文件大小、SHA256 或 SHA512 不匹配", artifact.Kind)
		}
		after, err := file.Stat()
		if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return nil, fmt.Errorf("%s 文件在校验期间发生变化", artifact.Kind)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("无法重新读取 %s 文件", artifact.Kind)
		}
		byKind[artifact.Kind] = artifact
	}
	if err := belloBuddyValidateNSISStub(files["installer"]); err != nil {
		return nil, err
	}
	if err := belloBuddyValidateBuilderMetadata(files["metadata"], release, byKind["installer"]); err != nil {
		return nil, err
	}
	manifest, err := belloBuddyValidateBuildReleaseManifest(files["manifest"], release, byKind)
	if err != nil {
		return nil, err
	}
	blocks, err := belloBuddyValidateInstallerBlockmap(files["blockmap"], files["installer"], byKind["installer"].Size)
	if err != nil {
		return nil, err
	}
	report := map[string]any{
		"passed": true, "checked_at": common.GetTimestamp(), "version": release.Version,
		"installer_sha256": byKind["installer"].SHA256, "blockmap_blocks": blocks,
		"checks": map[string]bool{"files_complete": true, "file_hashes": true, "metadata_matches_installer": true, "manifest_matches_files": true, "nsis_stub": true, "blockmap_checksums": true},
		"manifest": map[string]any{
			"signing": manifest.Signing, "windows_runtime_verified": *manifest.WindowsRuntimeVerified,
			"in_app_updater_integrated": *manifest.InAppUpdaterIntegrated, "release_approved": *manifest.ReleaseApproved,
			"dpin_api_origin": manifest.DpinConfig.APIOrigin, "electron_version": manifest.ElectronVersion,
			"build_manifest_sha256": manifest.BuildManifestSHA256, "dependency_manifest_sha256": manifest.DependencyManifestSHA256,
		},
		"windows_acceptance_required": true,
	}
	if manifest.Signing == "ed25519-update" {
		report["dpin_update_signature"] = manifest.UpdateSignature
		report["manifest"].(map[string]any)["update_key_id"] = manifest.UpdateKey.KeyID
	}
	return report, nil
}

func belloBuddyValidateNSISStub(file *os.File) error {
	binary, err := pe.NewFile(file)
	if err != nil {
		return errors.New("安装器不是可解析的 Windows PE 文件")
	}
	defer binary.Close()
	if binary.OptionalHeader == nil || binary.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || (binary.Machine != pe.IMAGE_FILE_MACHINE_I386 && binary.Machine != pe.IMAGE_FILE_MACHINE_AMD64) {
		return errors.New("安装器 Windows PE 目标无效")
	}
	// NSIS 的外层 stub 可为 32 位，不能据此推断内部 Electron 的架构。
	prefix, err := io.ReadAll(io.NewSectionReader(file, 0, 8<<20))
	if err != nil || !bytes.Contains(prefix, []byte{'\xef', '\xbe', '\xad', '\xde', 'N', 'u', 'l', 'l', 's', 'o', 'f', 't', 'I', 'n', 's', 't'}) {
		return errors.New("安装器缺少预期的 NSIS 数据头")
	}
	return nil
}

func belloBuddyValidateBuilderMetadata(file *os.File, release *model.BelloBuddyRelease, installer model.BelloBuddyReleaseArtifact) error {
	data, err := io.ReadAll(io.LimitReader(file, 256<<10+1))
	if err != nil || len(data) > 256<<10 {
		return errors.New("builder 更新元数据无法读取或超过 256 KiB")
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return errors.New("builder 更新元数据 YAML 无效")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("builder 更新元数据只能包含一份 YAML 文档")
	}
	nodes := 0
	if err := belloBuddyValidateYAMLNodes(&document, 0, &nodes); err != nil {
		return err
	}
	var metadata belloBuddyBuilderUpdateMetadata
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&metadata); err != nil {
		return errors.New("builder 更新元数据含重复、未知或类型错误的字段")
	}
	if metadata.Version != release.Version || metadata.Path != installer.Filename || metadata.SHA512 != installer.SHA512 || len(metadata.Files) != 1 {
		return errors.New("builder 更新元数据的版本、文件路径或摘要不匹配")
	}
	entry := metadata.Files[0]
	if entry.URL != installer.Filename || entry.SHA512 != installer.SHA512 || entry.Size != installer.Size || entry.BlockMapSize != 0 {
		return errors.New("builder 更新元数据必须引用同版本完整安装器及其实际大小／摘要")
	}
	if metadata.MinimumSystemVersion != "" && metadata.MinimumSystemVersion != release.MinimumSystemVersion {
		return errors.New("builder 更新元数据的最低系统版本不匹配")
	}
	if _, err := time.Parse(time.RFC3339Nano, metadata.ReleaseDate); err != nil {
		return errors.New("builder 更新元数据缺少有效构建日期")
	}
	return nil
}

func belloBuddyValidateYAMLNodes(node *yaml.Node, depth int, nodes *int) error {
	*nodes += 1
	if depth > 16 || *nodes > 4096 || node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("builder 更新元数据不允许 YAML 别名／锚点或过度嵌套")
	}
	for _, child := range node.Content {
		if err := belloBuddyValidateYAMLNodes(child, depth+1, nodes); err != nil {
			return err
		}
	}
	return nil
}

func belloBuddyValidateBuildReleaseManifest(file *os.File, release *model.BelloBuddyRelease, artifacts map[string]model.BelloBuddyReleaseArtifact) (*belloBuddyBuildReleaseManifest, error) {
	data, err := io.ReadAll(io.LimitReader(file, 2<<20+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("构建清单无法读取或超过 2 MiB")
	}
	var manifest belloBuddyBuildReleaseManifest
	if err := common.Unmarshal(data, &manifest); err != nil {
		return nil, errors.New("构建清单 JSON 无效")
	}
	productName := "DPin"
	if strings.HasPrefix(artifacts["installer"].Filename, "BelloBuddy-Recovered-") {
		productName = "BelloBuddy Recovered"
	}
	if manifest.SchemaVersion != 1 || manifest.Version != release.Version || manifest.AppID != "local.bellobuddy.recovered" || manifest.ProductName != productName || manifest.Target != "win32-x64" {
		return nil, errors.New("构建清单的应用标识、名称、版本或目标不匹配")
	}
	for _, fingerprint := range []string{manifest.BuildManifestSHA256, manifest.DependencyManifestSHA256, manifest.ElectronArchiveSHA256} {
		decoded, err := hex.DecodeString(fingerprint)
		if err != nil || len(decoded) != sha256.Size || fingerprint != strings.ToLower(fingerprint) {
			return nil, errors.New("构建清单缺少有效的源码、依赖或 Electron 指纹")
		}
	}
	if manifest.ElectronVersion == "" || manifest.WindowsRuntimeVerified == nil || manifest.InAppUpdaterIntegrated == nil || manifest.ReleaseApproved == nil || (manifest.Signing != "unsigned-internal" && manifest.Signing != "builder-signature-required" && manifest.Signing != "ed25519-update") {
		return nil, errors.New("构建清单缺少明确的签名和验收状态")
	}
	if manifest.Signing == "ed25519-update" {
		resource, err := verifyBelloBuddyUpdateSignature(manifest.UpdateSignature, release.Version, artifacts["installer"])
		if err != nil {
			return nil, err
		}
		if manifest.UpdateKey == nil || *manifest.UpdateKey != resource {
			return nil, errors.New("构建公钥资源与服务端固定的可信更新公钥不匹配")
		}
	}
	origin, err := url.Parse(manifest.DpinConfig.APIOrigin)
	if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("构建清单缺少有效的 D-PIN 服务根地址")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.BuiltAt); err != nil {
		return nil, errors.New("构建清单缺少有效构建日期")
	}
	if len(manifest.Artifacts) != 3 {
		return nil, errors.New("构建清单必须列出安装器、blockmap 和 builder 元数据")
	}
	expected := map[string]model.BelloBuddyReleaseArtifact{}
	for _, kind := range []string{"installer", "blockmap", "metadata"} {
		artifact := artifacts[kind]
		expected[artifact.Filename] = artifact
	}
	for _, entry := range manifest.Artifacts {
		artifact, found := expected[entry.Name]
		if !found || entry.Size != artifact.Size || entry.SHA256 != artifact.SHA256 {
			return nil, errors.New("构建清单中的制品文件名、大小或 SHA256 不匹配")
		}
		delete(expected, entry.Name)
	}
	return &manifest, nil
}

func belloBuddyValidateInstallerBlockmap(blockmapFile, installerFile *os.File, installerSize int64) (int, error) {
	compressed := bufio.NewReader(blockmapFile)
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return 0, errors.New("blockmap 不是有效 gzip 文件")
	}
	reader.Multistream(false)
	data, readErr := io.ReadAll(io.LimitReader(reader, 32<<20+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || len(data) > 32<<20 {
		return 0, errors.New("blockmap 解压失败或超过 32 MiB")
	}
	if _, err := compressed.Peek(1); !errors.Is(err, io.EOF) {
		return 0, errors.New("blockmap 包含额外压缩流或尾随数据")
	}
	var blockmap belloBuddyInstallerBlockmap
	if err := common.Unmarshal(data, &blockmap); err != nil || blockmap.Version != "2" || len(blockmap.Files) != 1 {
		return 0, errors.New("blockmap 版本或文件结构无效")
	}
	entry := blockmap.Files[0]
	if entry.Name != "file" || entry.Offset != 0 || len(entry.Sizes) == 0 || len(entry.Sizes) > 1_000_000 || len(entry.Sizes) != len(entry.Checksums) {
		return 0, errors.New("blockmap 文件名、偏移或数据块数量无效")
	}
	if _, err := installerFile.Seek(0, io.SeekStart); err != nil {
		return 0, errors.New("无法读取安装器以核对 blockmap")
	}
	hash, err := blake2b.New(18, nil)
	if err != nil {
		return 0, errors.New("无法初始化 blockmap 摘要校验")
	}
	var total int64
	buffer := make([]byte, 32*1024)
	for i, size := range entry.Sizes {
		if size <= 0 || size > 1<<20 || size > installerSize-total {
			return 0, errors.New("blockmap 数据块大小无效或超出安装器")
		}
		digest, err := base64.StdEncoding.Strict().DecodeString(entry.Checksums[i])
		if err != nil || len(digest) != 18 || base64.StdEncoding.EncodeToString(digest) != entry.Checksums[i] {
			return 0, errors.New("blockmap 数据块摘要格式无效")
		}
		hash.Reset()
		if count, err := io.CopyBuffer(hash, io.LimitReader(installerFile, size), buffer); err != nil || count != size {
			return 0, errors.New("安装器长度不足，无法核对 blockmap")
		}
		if !bytes.Equal(hash.Sum(nil), digest) {
			return 0, fmt.Errorf("blockmap 第 %d 个数据块与安装器不匹配", i+1)
		}
		total += size
	}
	if total != installerSize {
		return 0, errors.New("blockmap 数据块未完整覆盖安装器")
	}
	return len(entry.Sizes), nil
}

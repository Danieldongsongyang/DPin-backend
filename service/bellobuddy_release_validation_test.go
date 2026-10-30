package service

import (
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"debug/pe"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/blake2b"
	"gopkg.in/yaml.v3"
)

func releaseUpdateKeyFixture(t *testing.T, marker byte) (belloBuddyUpdateKeyResource, ed25519.PrivateKey) {
	t.Helper()
	// Fixed test-only seeds never become application or deployment keys.
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{marker}, ed25519.SeedSize))
	der, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	require.NoError(t, err)
	digest := sha256.Sum256(der)
	resource := belloBuddyUpdateKeyResource{SchemaVersion: 1, Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(der), KeyID: hex.EncodeToString(digest[:])}
	data, err := common.Marshal(resource)
	require.NoError(t, err)
	filename := filepath.Join(t.TempDir(), "update-key.json")
	require.NoError(t, os.WriteFile(filename, data, 0600))
	t.Setenv("DPIN_RELEASE_UPDATE_KEY_FILE", filename)
	return resource, privateKey
}

func TestBelloBuddyReleaseArtifactBrandCompatibility(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		legacy          bool
		mixedBlockmap   bool
		mixedProduct    bool
		mixedMetadata   bool
		mixedManifest   bool
		invalidMetadata bool
		invalidManifest bool
		ownSignature    bool
		untrustedKey    bool
		tampered        bool
		wrongBinding    bool
		wrongResource   bool
		wantError       string
	}{
		{name: "DPin release"},
		{name: "historical BelloBuddy release", legacy: true},
		{name: "new installer with old blockmap", mixedBlockmap: true, wantError: "发布文件种类、文件名或版本归属不匹配"},
		{name: "old installer with new blockmap", legacy: true, mixedBlockmap: true, wantError: "发布文件种类、文件名或版本归属不匹配"},
		{name: "new installer with old product", mixedProduct: true, wantError: "构建清单的应用标识、名称、版本或目标不匹配"},
		{name: "old installer with new product", legacy: true, mixedProduct: true, wantError: "构建清单的应用标识、名称、版本或目标不匹配"},
		{name: "renamed metadata path", mixedMetadata: true, wantError: "builder 更新元数据的版本、文件路径或摘要不匹配"},
		{name: "renamed manifest entry", mixedManifest: true, wantError: "构建清单中的制品文件名、大小或 SHA256 不匹配"},
		{name: "metadata checksum mismatch", invalidMetadata: true, wantError: "builder 更新元数据的版本、文件路径或摘要不匹配"},
		{name: "manifest checksum mismatch", invalidManifest: true, wantError: "构建清单中的制品文件名、大小或 SHA256 不匹配"},
		{name: "free pinned Ed25519 update signature", ownSignature: true},
		{name: "uploader key is not operator trust", ownSignature: true, untrustedKey: true, wantError: "更新签名或公钥指纹无效"},
		{name: "tampered Ed25519 signature", ownSignature: true, tampered: true, wantError: "安装器未通过可信 Ed25519 更新签名校验"},
		{name: "signed payload names a different installer", ownSignature: true, wrongBinding: true, wantError: "更新签名未绑定当前产品、安装器名称、版本、大小或摘要"},
		{name: "uploaded key resource differs from pinned key", ownSignature: true, wrongResource: true, wantError: "构建公钥资源与服务端固定的可信更新公钥不匹配"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			release := &model.BelloBuddyRelease{ID: 1, Product: model.BelloBuddyReleaseProduct, Platform: model.BelloBuddyReleasePlatform, Arch: model.BelloBuddyReleaseArch, Version: "3.0.29", MinimumSystemVersion: "10.0.19045"}
			installerName, alternateName := "DPin-3.0.29-win-x64.exe", "BelloBuddy-Recovered-3.0.29-win-x64.exe"
			productName, alternateProduct := "DPin", "BelloBuddy Recovered"
			if scenario.legacy {
				installerName, alternateName = alternateName, installerName
				productName, alternateProduct = alternateProduct, productName
			}
			if scenario.mixedProduct {
				productName = alternateProduct
			}
			// Synthetic PE/NSIS-shaped bytes are never executed or published.
			installer := make([]byte, 1024)
			copy(installer, "MZ")
			binary.LittleEndian.PutUint32(installer[0x3c:], 0x80)
			copy(installer[0x80:], "PE\x00\x00")
			var headers bytes.Buffer
			require.NoError(t, binary.Write(&headers, binary.LittleEndian, pe.FileHeader{
				Machine: pe.IMAGE_FILE_MACHINE_I386, SizeOfOptionalHeader: uint16(binary.Size(pe.OptionalHeader32{})), Characteristics: pe.IMAGE_FILE_EXECUTABLE_IMAGE,
			}))
			require.NoError(t, binary.Write(&headers, binary.LittleEndian, pe.OptionalHeader32{Magic: 0x10b, NumberOfRvaAndSizes: 16}))
			copy(installer[0x84:], headers.Bytes())
			copy(installer[512:], []byte{'\xef', '\xbe', '\xad', '\xde', 'N', 'u', 'l', 'l', 's', 'o', 'f', 't', 'I', 'n', 's', 't'})
			hash, err := blake2b.New(18, nil)
			require.NoError(t, err)
			_, err = hash.Write(installer)
			require.NoError(t, err)
			blockmapJSON, err := common.Marshal(map[string]any{
				"version": "2", "files": []any{map[string]any{"name": "file", "offset": 0, "sizes": []int{len(installer)}, "checksums": []string{base64.StdEncoding.EncodeToString(hash.Sum(nil))}}},
			})
			require.NoError(t, err)
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, err = writer.Write(blockmapJSON)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			blockmapName := installerName + ".blockmap"
			if scenario.mixedBlockmap {
				blockmapName = alternateName + ".blockmap"
			}
			installerHash := sha512.Sum512(installer)
			installerSHA512 := base64.StdEncoding.EncodeToString(installerHash[:])
			metadataName := installerName
			if scenario.mixedMetadata {
				metadataName = alternateName
			}
			if scenario.invalidMetadata {
				installerSHA512 = base64.StdEncoding.EncodeToString(make([]byte, sha512.Size))
			}
			metadata := []byte(fmt.Sprintf("version: 3.0.29\nfiles:\n  - url: %s\n    sha512: %s\n    size: %d\npath: %s\nsha512: %s\nreleaseDate: '2026-10-27T00:00:00Z'\n", metadataName, installerSHA512, len(installer), metadataName, installerSHA512))
			contents := []struct {
				kind, filename string
				data           []byte
			}{{"installer", installerName, installer}, {"blockmap", blockmapName, compressed.Bytes()}, {"metadata", "latest.yml", metadata}}
			directory := t.TempDir()
			artifacts := make([]model.BelloBuddyReleaseArtifact, 0, 4)
			manifestEntries := make([]map[string]any, 0, 3)
			for _, content := range contents {
				require.NoError(t, os.WriteFile(directory+string(os.PathSeparator)+content.filename, content.data, 0600))
				sum256, sum512 := sha256.Sum256(content.data), sha512.Sum512(content.data)
				artifact := model.BelloBuddyReleaseArtifact{ReleaseID: release.ID, Kind: content.kind, Filename: content.filename, StorageKey: content.filename, Size: int64(len(content.data)), SHA256: hex.EncodeToString(sum256[:]), SHA512: base64.StdEncoding.EncodeToString(sum512[:])}
				artifacts = append(artifacts, artifact)
				manifestEntries = append(manifestEntries, map[string]any{"name": artifact.Filename, "size": artifact.Size, "sha256": artifact.SHA256})
			}
			if scenario.mixedManifest {
				manifestEntries[0]["name"] = alternateName
			}
			if scenario.invalidManifest {
				manifestEntries[0]["sha256"] = strings.Repeat("0", 64)
			}
			manifestData := map[string]any{
				"schemaVersion": 1, "version": release.Version, "appId": "local.bellobuddy.recovered", "productName": productName, "target": "win32-x64", "electronVersion": "39.2.7", "builtAt": "2026-10-27T00:00:00Z",
				"signing": "unsigned-internal", "windowsRuntimeVerified": false, "inAppUpdaterIntegrated": true, "releaseApproved": false,
				"buildManifestSha256": strings.Repeat("1", 64), "dependencyManifestSha256": strings.Repeat("2", 64), "electronArchiveSha256": strings.Repeat("3", 64), "dpinConfig": map[string]any{"apiOrigin": "https://dpin.example"}, "artifacts": manifestEntries,
			}
			var signedEnvelope *belloBuddyUpdateSignature
			if scenario.ownSignature {
				resource, privateKey := releaseUpdateKeyFixture(t, 17)
				payloadBinding := map[string]any{"schemaVersion": 1, "product": "bellobuddy", "appId": "local.bellobuddy.recovered", "version": release.Version, "filename": installerName, "size": len(installer), "sha512": base64.StdEncoding.EncodeToString(installerHash[:])}
				if scenario.wrongBinding {
					payloadBinding["filename"] = "DPin-3.0.28-win-x64.exe"
				}
				payload, err := common.Marshal(payloadBinding)
				require.NoError(t, err)
				signature := ed25519.Sign(privateKey, payload)
				if scenario.tampered {
					signature[0] ^= 0xff
				}
				signedEnvelope = &belloBuddyUpdateSignature{KeyID: resource.KeyID, Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(signature)}
				manifestData["signing"], manifestData["updateKey"], manifestData["updateSignature"] = "ed25519-update", resource, signedEnvelope
				if scenario.untrustedKey {
					releaseUpdateKeyFixture(t, 23)
				}
				if scenario.wrongResource {
					resource.Algorithm = "untrusted-algorithm"
					manifestData["updateKey"] = resource
				}
			}
			manifest, err := common.Marshal(manifestData)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(directory+string(os.PathSeparator)+"release-manifest.json", manifest, 0600))
			manifest256, manifest512 := sha256.Sum256(manifest), sha512.Sum512(manifest)
			artifacts = append(artifacts, model.BelloBuddyReleaseArtifact{ReleaseID: release.ID, Kind: "manifest", Filename: "release-manifest.json", StorageKey: "release-manifest.json", Size: int64(len(manifest)), SHA256: hex.EncodeToString(manifest256[:]), SHA512: base64.StdEncoding.EncodeToString(manifest512[:])})
			root, err := os.OpenRoot(directory)
			require.NoError(t, err)
			defer root.Close()
			report, err := ValidateBelloBuddyReleaseFiles(root, release, artifacts)
			if scenario.wantError != "" {
				require.ErrorContains(t, err, scenario.wantError)
				assert.Nil(t, report)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, true, report["passed"])
			assert.Equal(t, 1, report["blockmap_blocks"])
			if scenario.ownSignature {
				encoded, err := common.Marshal(report)
				require.NoError(t, err)
				release.ValidationReport = string(encoded)
				feed, err := BelloBuddyReleaseFeed(release, &artifacts[0])
				require.NoError(t, err)
				var parsed struct {
					Signature struct {
						KeyID     string `yaml:"keyId"`
						Payload   string `yaml:"payload"`
						Signature string `yaml:"signature"`
					} `yaml:"dpinUpdateSignature"`
				}
				require.NoError(t, yaml.Unmarshal(feed, &parsed))
				assert.Equal(t, signedEnvelope.KeyID, parsed.Signature.KeyID)
				assert.Equal(t, signedEnvelope.Payload, parsed.Signature.Payload)
				assert.Equal(t, signedEnvelope.Signature, parsed.Signature.Signature)
				changedInstaller := artifacts[0]
				changedInstaller.Size++
				_, err = BelloBuddyReleaseFeed(release, &changedInstaller)
				assert.Error(t, err, "feeds must recheck the signature against the actual indexed installer")
			}
		})
	}
}

func TestBelloBuddyFreeUpdatePublicationAcceptancePolicy(t *testing.T) {
	resource, _ := releaseUpdateKeyFixture(t, 31)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey)})
	t.Setenv("BELLOBUDDY_RELEASE_VERIFIER_PUBLIC_KEY", string(publicPEM))
	t.Setenv("BELLOBUDDY_RELEASE_VERIFIER_PUBLIC_KEY_FILE", "")
	t.Setenv("BELLOBUDDY_RELEASE_PUBLISHER", "")
	installerSHA := strings.Repeat("a", 64)
	report := map[string]any{
		"schema_version": 1, "product": "bellobuddy", "version": "3.0.29", "platform": "win32", "arch": "x64", "installer_sha256": installerSHA,
		"verified_at": time.Now().Unix(), "windows_version": "10.0.19045", "minimum_system_version": "10.0.19045", "update_signature_key_id": resource.KeyID,
		"authenticode": map[string]any{"status": "NotSigned", "publisher": "", "certificate_thumbprint": ""},
		"checks":       map[string]bool{"installation": true, "launch": true, "native_tools": true, "data_preserved": true},
	}
	signEvidence := func() BelloBuddyReleaseEvidence {
		payload, err := common.Marshal(report)
		require.NoError(t, err)
		digest := sha256.Sum256(payload)
		signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
		require.NoError(t, err)
		return BelloBuddyReleaseEvidence{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(signature)}
	}
	// These synthetic reports test verification policy only. They are never
	// saved as real acceptance evidence or used to publish a release.
	evidence := signEvidence()
	_, err = verifyBelloBuddyReleaseEvidence(evidence, "3.0.29", installerSHA, "10.0.19045", "ed25519-update", resource.KeyID)
	require.NoError(t, err, "operator-pinned update signatures do not require a paid Authenticode certificate")
	_, err = VerifyBelloBuddyReleaseEvidence(evidence, "3.0.29", installerSHA, "10.0.19045")
	require.ErrorContains(t, err, "未配置可信 Windows 发布者", "the historical lane keeps its original requirement")
	t.Setenv("BELLOBUDDY_RELEASE_PUBLISHER", "Historical Certificate Publisher")
	_, err = VerifyBelloBuddyReleaseEvidence(evidence, "3.0.29", installerSHA, "10.0.19045")
	require.ErrorContains(t, err, "Windows 代码签名的有效状态")
	_, err = verifyBelloBuddyReleaseEvidence(evidence, "3.0.29", installerSHA, "10.0.19045", "ed25519-update", strings.Repeat("0", 64))
	require.ErrorContains(t, err, "未绑定当前可信更新签名公钥")
	report["checks"].(map[string]bool)["data_preserved"] = false
	_, err = verifyBelloBuddyReleaseEvidence(signEvidence(), "3.0.29", installerSHA, "10.0.19045", "ed25519-update", resource.KeyID)
	require.ErrorContains(t, err, "Windows 验收缺少通过项：data_preserved")
	evidence.Signature = base64.StdEncoding.EncodeToString(make([]byte, privateKey.Size()))
	_, err = verifyBelloBuddyReleaseEvidence(evidence, "3.0.29", installerSHA, "10.0.19045", "ed25519-update", resource.KeyID)
	require.ErrorContains(t, err, "未通过可信验收者签名校验")
}

func TestBelloBuddyRealBuildUpdateSignatureInterop(t *testing.T) {
	directory := os.Getenv("DPIN_TEST_RELEASE_DIR")
	if directory == "" {
		t.Skip("DPIN_TEST_RELEASE_DIR selects an existing real JavaScript-built release; no installer is copied or executed")
	}
	require.NotEmpty(t, os.Getenv("DPIN_RELEASE_UPDATE_KEY_FILE"), "the independently configured trusted public key is required")
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	manifestBytes, err := root.ReadFile("release-manifest.json")
	require.NoError(t, err)
	require.LessOrEqual(t, len(manifestBytes), 2<<20)
	var manifest belloBuddyBuildReleaseManifest
	require.NoError(t, common.Unmarshal(manifestBytes, &manifest))
	require.Equal(t, "ed25519-update", manifest.Signing)
	require.NotNil(t, manifest.UpdateKey)
	require.NotNil(t, manifest.UpdateSignature)
	require.Len(t, manifest.Artifacts, 3)
	release := &model.BelloBuddyRelease{ID: 1, Product: model.BelloBuddyReleaseProduct, Platform: model.BelloBuddyReleasePlatform, Arch: model.BelloBuddyReleaseArch, Version: manifest.Version, MinimumSystemVersion: "10.0.17763"}
	metadataBytes, err := root.ReadFile("latest.yml")
	require.NoError(t, err)
	var metadata belloBuddyBuilderUpdateMetadata
	require.NoError(t, yaml.Unmarshal(metadataBytes, &metadata))
	if metadata.MinimumSystemVersion != "" {
		release.MinimumSystemVersion = metadata.MinimumSystemVersion
	}
	artifacts := make([]model.BelloBuddyReleaseArtifact, 0, 4)
	installerName := ""
	for _, entry := range manifest.Artifacts {
		kind := ""
		switch {
		case belloBuddyReleaseArtifactFilenameMatches(manifest.Version, "installer", entry.Name):
			kind, installerName = "installer", entry.Name
		case belloBuddyReleaseArtifactFilenameMatches(manifest.Version, "blockmap", entry.Name):
			kind = "blockmap"
		case entry.Name == "latest.yml":
			kind = "metadata"
		}
		require.NotEmpty(t, kind, "the real manifest must contain only the expected three build artifacts")
		file, err := root.Open(entry.Name)
		require.NoError(t, err)
		info, err := file.Stat()
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular())
		sum256, sum512 := sha256.New(), sha512.New()
		size, readErr := io.Copy(io.MultiWriter(sum256, sum512), file)
		closeErr := file.Close()
		require.NoError(t, readErr)
		require.NoError(t, closeErr)
		require.Equal(t, entry.Size, size)
		require.Equal(t, entry.SHA256, hex.EncodeToString(sum256.Sum(nil)))
		artifacts = append(artifacts, model.BelloBuddyReleaseArtifact{ReleaseID: release.ID, Kind: kind, Filename: entry.Name, StorageKey: entry.Name, Size: size, SHA256: hex.EncodeToString(sum256.Sum(nil)), SHA512: base64.StdEncoding.EncodeToString(sum512.Sum(nil))})
	}
	manifest256, manifest512 := sha256.Sum256(manifestBytes), sha512.Sum512(manifestBytes)
	artifacts = append(artifacts, model.BelloBuddyReleaseArtifact{ReleaseID: release.ID, Kind: "manifest", Filename: "release-manifest.json", StorageKey: "release-manifest.json", Size: int64(len(manifestBytes)), SHA256: hex.EncodeToString(manifest256[:]), SHA512: base64.StdEncoding.EncodeToString(manifest512[:])})
	report, err := ValidateBelloBuddyReleaseFiles(root, release, artifacts)
	require.NoError(t, err, "Go must verify the JavaScript signature over the final, unchanged installer bytes")
	assert.Equal(t, true, report["passed"])
	verified, ok := report["dpin_update_signature"].(*belloBuddyUpdateSignature)
	require.True(t, ok)
	assert.Equal(t, manifest.UpdateSignature, verified)
	assert.Equal(t, manifest.UpdateKey.KeyID, verified.KeyID)
	t.Logf("Verified JavaScript-to-Go artifact and Ed25519 signature interoperability for %s, keyId=%s; this does not verify Windows installation, launch or upgrade.", installerName, verified.KeyID)
}

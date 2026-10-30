package service

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	version "github.com/hashicorp/go-version"
	"gorm.io/gorm"
)

const BelloBuddyReleaseMaxUpload = int64(2 << 30)

var belloReleaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
var belloWindowsVersion = regexp.MustCompile(`^[0-9]{1,5}(?:\.[0-9]{1,5}){2,3}$`)

type BelloBuddyReleaseDraftInput struct {
	Revision             int64  `json:"revision"`
	Version              string `json:"version"`
	BuildID              string `json:"build_id"`
	Title                string `json:"title"`
	Notes                string `json:"notes"`
	MinimumSystemVersion string `json:"minimum_system_version"`
}

// A configured persistent directory is required; application/source and temporary
// directories must never become the durable release store.
func OpenBelloBuddyReleaseStorage() (*os.Root, error) {
	directory := strings.TrimSpace(os.Getenv("RELEASE_STORAGE_ROOT"))
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, errors.New("请先配置绝对路径 RELEASE_STORAGE_ROOT，指向独立的持久化发布目录")
	}
	directory = filepath.Clean(directory)
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for _, forbidden := range []string{os.TempDir(), "/tmp", "/private/tmp", cwd} {
		relative, err := filepath.Rel(forbidden, directory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil, errors.New("发布目录不能位于临时目录或源码工作目录中")
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("无法创建发布目录，请检查服务账号权限")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return nil, errors.New("发布目录必须使用真实绝对路径，不能通过符号链接访问")
	}
	return os.OpenRoot(directory)
}

func BelloBuddyReleaseDraft(db *gorm.DB, actorID, id int, input BelloBuddyReleaseDraftInput) (*model.BelloBuddyRelease, error) {
	if !belloBuddyReleaseValidSemver(input.Version) || len(input.Version) > 96 || strings.TrimSpace(input.BuildID) == "" || len(input.BuildID) > 128 || strings.TrimSpace(input.Title) == "" || len(input.Title) > 256 || len(input.Notes) > 60<<10 || !belloWindowsVersion.MatchString(input.MinimumSystemVersion) {
		return nil, model.ErrBelloBuddyReleaseInput
	}
	if id == 0 {
		release := &model.BelloBuddyRelease{Version: input.Version, BuildID: input.BuildID, Title: input.Title, Notes: input.Notes, MinimumSystemVersion: input.MinimumSystemVersion, CreatedBy: actorID}
		return release, model.CreateBelloBuddyRelease(db, release)
	}
	return model.WithBelloBuddyRelease(db, id, input.Revision, []string{model.BelloBuddyReleaseDraft}, func(tx *gorm.DB, release *model.BelloBuddyRelease) error {
		if input.Version != release.Version {
			return errors.New("版本号不可修改，请另建草稿")
		}
		release.BuildID, release.Title, release.Notes, release.MinimumSystemVersion = input.BuildID, input.Title, input.Notes, input.MinimumSystemVersion
		release.ValidationReport = ""
		return nil
	})
}

// NewSemver also accepts shortened versions and numeric prerelease identifiers
// with leading zeroes; enforce those SemVer constraints at this boundary.
func belloBuddyReleaseValidSemver(value string) bool {
	if !belloReleaseVersion.MatchString(value) {
		return false
	}
	parsed, err := version.NewSemver(value)
	if err != nil {
		return false
	}
	for identifier := range strings.SplitSeq(parsed.Prerelease(), ".") {
		if len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
			return false
		}
	}
	return true
}

// The same per-kind limit governs HTTP framing and the streamed file copy.
func BelloBuddyReleaseUploadLimit(kind string) int64 {
	switch kind {
	case "installer":
		return BelloBuddyReleaseMaxUpload
	case "blockmap":
		return 16 << 20
	case "metadata":
		return 256 << 10
	case "manifest":
		return 2 << 20
	default:
		return 0
	}
}

func BelloBuddyReleaseFilename(version, kind string) string {
	installer := "DPin-" + version + "-win-x64.exe"
	switch kind {
	case "installer":
		return installer
	case "blockmap":
		return installer + ".blockmap"
	case "metadata":
		return "latest.yml"
	case "manifest":
		return "release-manifest.json"
	default:
		return ""
	}
}

func belloBuddyReleaseArtifactFilenameMatches(version, kind, filename string) bool {
	expected := BelloBuddyReleaseFilename(version, kind)
	if expected == "" {
		return false
	}
	if filename == expected {
		return true
	}
	return (kind == "installer" || kind == "blockmap") && filename == "BelloBuddy-Recovered-"+strings.TrimPrefix(expected, "DPin-")
}

// Upload bodies are streamed into a unique private file. The index replacement
// and release revision commit together; a failed transaction leaves only an
// unreferenced immutable file, never a changed current channel.
func StoreBelloBuddyReleaseArtifact(db *gorm.DB, actorID, id int, revision int64, kind, filename string, reader io.Reader) (*model.BelloBuddyRelease, error) {
	release, err := model.GetBelloBuddyRelease(db, id)
	if err != nil {
		return nil, err
	}
	if release.State != model.BelloBuddyReleaseDraft || release.Revision != revision || release.Disabled {
		return nil, model.ErrBelloBuddyReleaseConflict
	}
	if !belloBuddyReleaseArtifactFilenameMatches(release.Version, kind, filename) {
		return nil, errors.New("文件种类或名称与当前版本不一致")
	}
	storage, err := OpenBelloBuddyReleaseStorage()
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	now := common.GetTimestamp()
	upload := model.BelloBuddyReleaseUpload{UploadID: uuid.NewString(), ReleaseID: id, Kind: kind, Filename: filename, State: "uploading", CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&upload).Error; err != nil {
		return nil, err
	}
	var received int64
	completed := false
	defer func() {
		if !completed {
			// A panic, short body or failed commit cannot be recorded as complete.
			db.Model(&model.BelloBuddyReleaseUpload{}).Where("id = ?", upload.ID).Updates(map[string]any{"state": "failed", "error_code": "UPLOAD_FAILED", "bytes_received": received, "updated_at": common.GetTimestamp()})
		}
	}()
	if err = storage.MkdirAll("staging", 0700); err != nil {
		return nil, err
	}
	stage := path.Join("staging", upload.UploadID)
	file, err := storage.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	defer storage.Remove(stage)
	limit := BelloBuddyReleaseUploadLimit(kind)
	h256, h512 := sha256.New(), sha512.New()
	received, err = io.Copy(io.MultiWriter(file, h256, h512), io.LimitReader(reader, limit+1))
	if err == nil && (received == 0 || received > limit) {
		err = errors.New("文件为空或超过上传大小限制")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	sha256Hex := hex.EncodeToString(h256.Sum(nil))
	key := path.Join("bellobuddy", release.Version, "win32", "x64", upload.UploadID, filename)
	updated, err := model.WithBelloBuddyRelease(db, id, revision, []string{model.BelloBuddyReleaseDraft}, func(tx *gorm.DB, locked *model.BelloBuddyRelease) error {
		if err := storage.MkdirAll(path.Dir(key), 0700); err != nil {
			return err
		}
		if err := storage.Rename(stage, key); err != nil {
			return err
		}
		// Persist every newly created parent entry before committing its DB index.
		if err := persistBelloBuddyReleaseDirectory(storage, path.Dir(key)); err != nil {
			return err
		}
		if err := tx.Where("release_id = ? AND kind = ?", id, kind).Delete(&model.BelloBuddyReleaseArtifact{}).Error; err != nil {
			return err
		}
		artifact := model.BelloBuddyReleaseArtifact{ReleaseID: id, Kind: kind, Filename: filename, StorageKey: key, Size: received, SHA256: sha256Hex, SHA512: base64.StdEncoding.EncodeToString(h512.Sum(nil)), CreatedAt: now}
		locked.ValidationReport = ""
		if err := tx.Create(&artifact).Error; err != nil {
			return err
		}
		return tx.Model(&model.BelloBuddyReleaseUpload{}).Where("id = ?", upload.ID).Updates(map[string]any{"state": "complete", "error_code": "", "bytes_received": received, "updated_at": common.GetTimestamp()}).Error
	})
	completed = err == nil
	return updated, err
}

// Windows has no portable directory fsync; files are still synced before rename.
func persistBelloBuddyReleaseDirectory(storage *os.Root, directory string) error {
	if os.PathSeparator == '\\' {
		return nil
	}
	for {
		file, err := storage.Open(directory)
		if err != nil {
			return errors.New("无法打开发布目录以完成持久化")
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil || closeErr != nil {
			return errors.New("发布目录持久化失败，版本未提交")
		}
		if directory == "." {
			return nil
		}
		directory = path.Dir(directory)
	}
}

// Explicit CAS recovery prevents a crashed validator from permanently locking
// the draft. An old validator can no longer commit after this revision changes.
func RecoverBelloBuddyReleaseValidation(db *gorm.DB, id int, revision int64) (*model.BelloBuddyRelease, error) {
	return model.WithBelloBuddyRelease(db, id, revision, []string{model.BelloBuddyReleaseValidating}, func(tx *gorm.DB, release *model.BelloBuddyRelease) error {
		if common.GetTimestamp()-release.UpdatedAt < 10*60 {
			return errors.New("校验开始尚未满 10 分钟，请稍后再恢复；先确认原校验请求已中断")
		}
		report, err := common.Marshal(map[string]any{"passed": false, "checked_at": common.GetTimestamp(), "message": "管理员恢复了中断的校验，请重新校验制品"})
		if err != nil {
			return err
		}
		release.State = model.BelloBuddyReleaseDraft
		release.ValidationReport = string(report)
		return nil
	})
}

func ValidateBelloBuddyRelease(db *gorm.DB, id int, revision int64) (*model.BelloBuddyRelease, error) {
	storage, err := OpenBelloBuddyReleaseStorage()
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	release, err := model.WithBelloBuddyRelease(db, id, revision, []string{model.BelloBuddyReleaseDraft}, func(tx *gorm.DB, release *model.BelloBuddyRelease) error {
		release.State = model.BelloBuddyReleaseValidating
		return nil
	})
	if err != nil {
		return nil, err
	}
	var artifacts []model.BelloBuddyReleaseArtifact
	validationErr := db.Where("release_id = ?", id).Find(&artifacts).Error
	var report map[string]any
	if validationErr == nil {
		report, validationErr = ValidateBelloBuddyReleaseFiles(storage, release, artifacts)
	}
	if validationErr != nil {
		report = map[string]any{"passed": false, "checked_at": common.GetTimestamp(), "message": validationErr.Error()}
	}
	encoded, err := common.Marshal(report)
	if err != nil {
		return nil, err
	}
	updated, err := model.WithBelloBuddyRelease(db, id, release.Revision, []string{model.BelloBuddyReleaseValidating}, func(tx *gorm.DB, locked *model.BelloBuddyRelease) error {
		locked.ValidationReport = string(encoded)
		locked.State = model.BelloBuddyReleaseReady
		if validationErr != nil {
			locked.State = model.BelloBuddyReleaseDraft
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Failed validation is a persisted result. Returning the draft lets the
	// administrator see its report and current revision without blind retries.
	return updated, nil
}

func AcceptBelloBuddyRelease(db *gorm.DB, id int, revision int64, evidence BelloBuddyReleaseEvidence) (*model.BelloBuddyRelease, error) {
	return model.WithBelloBuddyRelease(db, id, revision, []string{model.BelloBuddyReleaseReady}, func(tx *gorm.DB, release *model.BelloBuddyRelease) error {
		var artifact model.BelloBuddyReleaseArtifact
		if err := tx.Where("release_id = ? AND kind = ?", id, "installer").First(&artifact).Error; err != nil {
			return err
		}
		var validation belloBuddyVerifiedReleaseReport
		if common.UnmarshalJsonStr(release.ValidationReport, &validation) != nil || !validation.Passed {
			return errors.New("缺少通过的发布文件校验结果")
		}
		if validation.Manifest.Signing == "ed25519-update" {
			if _, err := verifyBelloBuddyUpdateSignature(validation.UpdateSignature, release.Version, artifact); err != nil {
				return err
			}
		}
		report, err := verifyBelloBuddyReleaseEvidence(evidence, release.Version, artifact.SHA256, release.MinimumSystemVersion, validation.Manifest.Signing, validation.Manifest.UpdateKeyID)
		if err != nil {
			return err
		}
		report["evidence"] = evidence
		encoded, err := common.Marshal(report)
		if err == nil {
			release.AcceptanceReport = string(encoded)
		}
		return err
	})
}

func ApplyBelloBuddyReleaseOperation(db *gorm.DB, input model.BelloBuddyReleaseOperationInput) (*model.BelloBuddyReleaseOperation, error) {
	if input.Action != "publish" && strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New("请填写撤回或禁用原因")
	}
	fingerprint, err := common.Marshal(input)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(fingerprint)
	input.RequestFingerprint = hex.EncodeToString(sum[:])
	return model.ApplyBelloBuddyReleaseOperation(db, input, func(tx *gorm.DB, release *model.BelloBuddyRelease) error {
		storage, err := OpenBelloBuddyReleaseStorage()
		if err != nil {
			return err
		}
		defer storage.Close()
		var artifacts []model.BelloBuddyReleaseArtifact
		if err := tx.Where("release_id = ?", release.ID).Find(&artifacts).Error; err != nil {
			return err
		}
		report, err := ValidateBelloBuddyReleaseFiles(storage, release, artifacts)
		if err != nil {
			return err
		}
		// Builder assertions are additional gates; cryptographically authenticated
		// Windows acceptance remains mandatory and is rechecked at publication.
		manifest, _ := report["manifest"].(map[string]any)
		signing := fmt.Sprint(manifest["signing"])
		if !slices.Contains([]string{"signed", "builder-signature-required", "ed25519-update"}, signing) || manifest["in_app_updater_integrated"] != true {
			return errors.New("正式发布需要可信更新签名并集成更新器；当前版本仅可内部验收")
		}
		var saved struct {
			Evidence BelloBuddyReleaseEvidence `json:"evidence"`
		}
		if common.UnmarshalJsonStr(release.AcceptanceReport, &saved) != nil {
			return errors.New("缺少可信 Windows 验收凭证")
		}
		for _, artifact := range artifacts {
			if artifact.Kind == "installer" {
				_, err = verifyBelloBuddyReleaseEvidence(saved.Evidence, release.Version, artifact.SHA256, release.MinimumSystemVersion, signing, fmt.Sprint(manifest["update_key_id"]))
				return err
			}
		}
		return errors.New("缺少安装器")
	})
}

func BelloBuddyReleaseView(release *model.BelloBuddyRelease) map[string]any {
	encoded, _ := common.Marshal(release)
	view := map[string]any{}
	_ = common.Unmarshal(encoded, &view)
	for key, raw := range map[string]string{"validation_report": release.ValidationReport, "acceptance_report": release.AcceptanceReport} {
		var report map[string]any
		if raw != "" {
			_ = common.UnmarshalJsonStr(raw, &report)
		}
		if report != nil {
			delete(report, "evidence")
		}
		view[key] = report
	}
	return view
}

// This is the sole access policy used by feed, download and installation status.
// Ready artifacts require explicit release read permission and remain internal.
func CheckBelloBuddyReleaseAccess(release *model.BelloBuddyRelease, internal bool) error {
	visible := release.State == model.BelloBuddyReleaseReady && internal ||
		release.PublishedAt > 0 && slices.Contains([]string{model.BelloBuddyReleasePublished, model.BelloBuddyReleaseWithdrawn}, release.State)
	if !visible {
		return &BelloBuddyError{Status: 404, Code: "RELEASE_NOT_AVAILABLE", Message: "该版本尚未发布"}
	}
	if release.Disabled {
		return &BelloBuddyError{Status: 410, Code: "RELEASE_DISABLED", Message: "该版本已被紧急禁用"}
	}
	return nil
}

func BelloBuddyReleaseArtifactByName(db *gorm.DB, filename string) (*model.BelloBuddyRelease, *model.BelloBuddyReleaseArtifact, error) {
	version, found := strings.CutPrefix(filename, "DPin-")
	if !found {
		version, found = strings.CutPrefix(filename, "BelloBuddy-Recovered-")
	}
	if !found || (!strings.HasSuffix(filename, "-win-x64.exe") && !strings.HasSuffix(filename, "-win-x64.exe.blockmap")) {
		return nil, nil, gorm.ErrRecordNotFound
	}
	version = strings.TrimSuffix(strings.TrimSuffix(version, ".blockmap"), "-win-x64.exe")
	if !belloBuddyReleaseValidSemver(version) {
		return nil, nil, gorm.ErrRecordNotFound
	}
	var release model.BelloBuddyRelease
	if err := db.Where("product = ? AND version_hash = ?", model.BelloBuddyReleaseProduct, model.BelloBuddyIdentityHash(version)).First(&release).Error; err != nil {
		return nil, nil, err
	}
	var artifact model.BelloBuddyReleaseArtifact
	err := db.Where("release_id = ? AND filename = ? AND kind IN ?", release.ID, filename, []string{"installer", "blockmap"}).First(&artifact).Error
	return &release, &artifact, err
}

func BelloBuddyReleaseFeed(release *model.BelloBuddyRelease, artifact *model.BelloBuddyReleaseArtifact) ([]byte, error) {
	// JSON strings are valid YAML quoted scalars; the feed is generated from
	// verified database fields, never from arbitrary uploaded URLs or YAML tags.
	quote := func(value string) string { encoded, _ := common.Marshal(value); return string(encoded) }
	url := "/desktop-updates/bellobuddy/artifacts/win32/x64/" + artifact.Filename
	text := fmt.Sprintf("version: %s\nfiles:\n  - url: %s\n    sha512: %s\n    size: %d\npath: %s\nsha512: %s\nminimumSystemVersion: %s\nreleaseName: %s\nreleaseNotes: %s\n", quote(release.Version), quote(url), artifact.SHA512, artifact.Size, quote(url), artifact.SHA512, quote(release.MinimumSystemVersion), quote(release.Title), quote(release.Notes))
	if release.ValidationReport != "" {
		var validation belloBuddyVerifiedReleaseReport
		if common.UnmarshalJsonStr(release.ValidationReport, &validation) != nil {
			return nil, errors.New("版本文件校验记录无效")
		}
		if validation.Manifest.Signing == "ed25519-update" {
			if !validation.Passed || validation.UpdateSignature == nil || validation.UpdateSignature.KeyID != validation.Manifest.UpdateKeyID {
				return nil, errors.New("版本缺少已验证的更新签名")
			}
			if _, err := verifyBelloBuddyUpdateSignature(validation.UpdateSignature, release.Version, *artifact); err != nil {
				return nil, err
			}
			encoded, err := common.Marshal(validation.UpdateSignature)
			if err != nil {
				return nil, err
			}
			text += "dpinUpdateSignature: " + string(encoded) + "\n"
		}
	}
	return []byte(text), nil
}

func BelloBuddyReleaseID(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return id, nil
}

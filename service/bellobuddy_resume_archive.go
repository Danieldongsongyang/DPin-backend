package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const BelloBuddyResumeMaxBytes int64 = 30 << 20

type BelloBuddyResumeInput struct {
	ClientUploadID string `json:"client_upload_id"`
	CandidateID    string `json:"candidate_id"`
	CandidateName  string `json:"candidate_name"`
	DownloadedAt   int64  `json:"downloaded_at"` // 毫秒；客户端实际下载回执，服务端不伪造平台事实。
}

type BelloBuddyResumeEngine struct {
	DB      *gorm.DB
	Config  ResumeArchiveConfig
	Backend FeishuArchiveBackend
	mu      sync.Mutex
}

var resumeEngine *BelloBuddyResumeEngine
var resumeEngineMu sync.RWMutex

func CurrentBelloBuddyResumeEngine() (*BelloBuddyResumeEngine, error) {
	resumeEngineMu.RLock()
	defer resumeEngineMu.RUnlock()
	if resumeEngine == nil {
		return nil, &BelloBuddyError{503, "ARCHIVE_DISABLED", "简历归档未配置或配置无效，请联系管理员"}
	}
	return resumeEngine, nil
}

func StartBelloBuddyResumeArchive() {
	config, err := LoadResumeArchiveConfig()
	if err != nil {
		if os.Getenv("DPIN_RESUME_ARCHIVE_CONFIG") != "" {
			common.SysError("resume archive: " + resumeFailureCode(err))
		}
		return
	}
	engine := &BelloBuddyResumeEngine{DB: model.DB, Config: *config, Backend: &FeishuArchiveCLI{Config: *config}}
	if stats, err := engine.StorageStatus(); err != nil {
		common.SysError("resume archive startup file check failed")
	} else if count, ok := stats["orphan_files"].(int); ok && count > 0 {
		common.SysError(fmt.Sprintf("resume archive: %d uncommitted private files preserved for review", count))
	}
	resumeEngineMu.Lock()
	resumeEngine = engine
	resumeEngineMu.Unlock()
	go func() {
		defer func() {
			if recover() != nil {
				common.SysError("resume archive worker stopped unexpectedly")
			}
		}()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			err := engine.RunOnce(ctx)
			cancel()
			if err != nil {
				common.SysError("resume archive worker: " + resumeFailureCode(err))
			}
			if err := engine.CleanupVerifiedFiles(time.Now().Unix()); err != nil {
				common.SysError("resume archive cleanup: ARCHIVE_DISK_OR_DATABASE_ERROR")
			}
			time.Sleep(3 * time.Second)
		}
	}()
}

func resumeFailureCode(err error) string {
	var failure *FeishuArchiveFailure
	if errors.As(err, &failure) {
		return failure.Code
	}
	var business *BelloBuddyError
	if errors.As(err, &business) {
		return business.Code
	}
	return "ARCHIVE_DATABASE_OR_DISK_ERROR"
}

func (engine *BelloBuddyResumeEngine) ownership(userID int, accountKey, jobID string) (*model.BelloBuddyAccount, *model.BelloBuddyJob, *model.User, error) {
	if ValidateBelloBuddyIdentifier(accountKey) != nil || ValidateBelloBuddyIdentifier(jobID) != nil {
		return nil, nil, nil, ErrBelloBuddyNotFound
	}
	var account model.BelloBuddyAccount
	var job model.BelloBuddyJob
	var user model.User
	if engine.DB.Where("user_id = ? AND account_hash = ?", userID, model.BelloBuddyIdentityHash(accountKey)).First(&account).Error != nil || engine.DB.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(jobID)).First(&job).Error != nil || engine.DB.Select("id", "username", "display_name", "status").First(&user, userID).Error != nil || user.Status != common.UserStatusEnabled {
		return nil, nil, nil, ErrBelloBuddyNotFound
	}
	return &account, &job, &user, nil
}

func (engine *BelloBuddyResumeEngine) Accept(ctx context.Context, userID int, accountKey, jobID, filename string, input BelloBuddyResumeInput, reader io.Reader) (*model.BelloBuddyResumeArchive, error) {
	account, job, _, err := engine.ownership(userID, accountKey, jobID)
	if err != nil {
		return nil, err
	}
	if ValidateBelloBuddyIdentifier(input.ClientUploadID) != nil || ValidateBelloBuddyIdentifier(input.CandidateID) != nil || len(input.CandidateName) > 128 || strings.ContainsAny(input.CandidateName, "\x00\r\n") || input.DownloadedAt <= 0 || input.DownloadedAt > time.Now().UnixMilli()+300000 || strings.ToLower(filepath.Ext(filename)) != ".pdf" {
		return nil, belloInvalid("上传编号、候选人、下载回执时间或 PDF 文件名无效")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	lease, ok, err := model.ClaimBelloBuddyResumeLease(engine.DB, model.BelloBuddyIdentityHash("resume-storage:"+engine.Config.DataDir), uuid.NewString(), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &BelloBuddyError{503, "ARCHIVE_STORAGE_BUSY", "暂存接收正在处理，请按原上传编号核对后重试"}
	}
	defer model.ReleaseBelloBuddyResumeLease(engine.DB, lease, false)
	var used int64
	entries, err := os.ReadDir(filepath.Join(engine.Config.DataDir, "files"))
	if err != nil {
		return nil, &BelloBuddyError{507, "ARCHIVE_DISK_ERROR", "简历暂存目录不可用"}
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, &BelloBuddyError{507, "ARCHIVE_DISK_ERROR", "简历暂存文件状态异常"}
		}
		used += info.Size()
	}
	available, diskErr := availableDiskBytes(engine.Config.DataDir)
	if diskErr != nil || available < uint64(BelloBuddyResumeMaxBytes+(16<<20)) {
		return nil, &BelloBuddyError{507, "ARCHIVE_DISK_FULL", "服务器磁盘可用空间不足，文件未接收"}
	}
	archiveID := uuid.NewString()
	storageName := archiveID + ".pdf"
	path := filepath.Join(engine.Config.DataDir, "files", storageName)
	file, err := createFeishuPrivateFile(path)
	if err != nil {
		return nil, &BelloBuddyError{507, "ARCHIVE_DISK_ERROR", "简历暂存写入失败，文件未接收"}
	}
	limit := min(BelloBuddyResumeMaxBytes, engine.Config.MaxStorageBytes-used)
	if limit <= 0 {
		file.Close()
		os.Remove(path)
		return nil, &BelloBuddyError{507, "ARCHIVE_CAPACITY_FULL", "简历暂存已达容量上限，已有文件保留"}
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, limit+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || ctx.Err() != nil || size > limit {
		os.Remove(path)
		code := "ARCHIVE_DISK_ERROR"
		status := 507
		if size > BelloBuddyResumeMaxBytes {
			code = "RESUME_TOO_LARGE"
			status = 413
		} else if size > limit {
			code = "ARCHIVE_CAPACITY_FULL"
		}
		return nil, &BelloBuddyError{status, code, "PDF 暂存失败或超过文件／容量限制，文件未接收"}
	}
	content, err := os.ReadFile(path)
	if err != nil || size < 20 || !bytes.HasPrefix(content, []byte("%PDF-")) || http.DetectContentType(content[:min(len(content), 512)]) != "application/pdf" || !bytes.Contains(content[max(0, len(content)-1024):], []byte("%%EOF")) {
		os.Remove(path)
		return nil, belloInvalid("文件内容不是完整 PDF")
	}
	for _, unsafe := range []string{"/JavaScript", "/Launch", "/EmbeddedFile", "/RichMedia", "/XFA"} {
		if bytes.Contains(content, []byte(unsafe)) {
			os.Remove(path)
			return nil, belloInvalid("PDF 包含不支持的主动内容或内嵌文件")
		}
	}
	if err := syncFeishuCreatedFile(path); err != nil {
		return nil, err
	}
	sha := fmt.Sprintf("%x", hash.Sum(nil))
	targetData, err := common.Marshal(engine.Config.Target)
	if err != nil {
		return nil, err
	}
	mapKey := model.BelloBuddyIdentityHash(fmt.Sprintf("%s:%d:%d:%d", targetData, userID, account.ID, job.ID))
	scopeKey := model.BelloBuddyIdentityHash(mapKey + "\x00" + input.CandidateID)
	now := time.Now().Unix()
	archive := model.BelloBuddyResumeArchive{ArchiveID: archiveID, SHA256: sha, StorageName: storageName, Size: size, CandidateName: input.CandidateName, DownloadedAt: input.DownloadedAt, State: "queued", Stage: "accepted", ReceivedAt: now, UpdatedAt: now}
	err = engine.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := model.GetBelloBuddyAccountForUpdate(tx, account.ID); err != nil {
			return err
		}
		mapping := model.BelloBuddyResumeJobMap{MapKey: mapKey}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mapping).Error; err != nil {
			return err
		}
		if err := tx.Where("map_key = ?", mapKey).First(&mapping).Error; err != nil {
			return err
		}
		scope := model.BelloBuddyResumeScope{ScopeKey: scopeKey, UserID: userID, AccountID: account.ID, JobID: job.ID, CandidateID: input.CandidateID, JobMapID: mapping.ID, Target: string(targetData), CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&scope).Error; err != nil {
			return err
		}
		if err := tx.Where("scope_key = ?", scopeKey).First(&scope).Error; err != nil {
			return err
		}
		archive.ScopeID = scope.ID
		var received model.BelloBuddyResumeUpload
		lookup := tx.Where("user_id = ? AND upload_hash = ?", userID, model.BelloBuddyIdentityHash(input.ClientUploadID)).First(&received).Error
		if lookup == nil {
			if received.ScopeKey != scopeKey || received.SHA256 != sha || received.ClientUploadID != input.ClientUploadID {
				return &BelloBuddyError{409, "UPLOAD_ID_CONFLICT", "此上传编号已用于其他内容或归属"}
			}
			return tx.First(&archive, received.ArchiveID).Error
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&archive).Error; err != nil {
			return err
		}
		if err := tx.Where("scope_id = ? AND sha256 = ?", scope.ID, sha).First(&archive).Error; err != nil {
			return err
		}
		received = model.BelloBuddyResumeUpload{UserID: userID, UploadHash: model.BelloBuddyIdentityHash(input.ClientUploadID), ClientUploadID: input.ClientUploadID, ArchiveID: archive.ID, ScopeKey: scopeKey, SHA256: sha}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&received).Error; err != nil {
			return err
		}
		var check model.BelloBuddyResumeUpload
		if err := tx.Where("user_id = ? AND upload_hash = ?", userID, received.UploadHash).First(&check).Error; err != nil {
			return err
		}
		if check.ScopeKey != scopeKey || check.SHA256 != sha {
			return &BelloBuddyError{409, "UPLOAD_ID_CONFLICT", "此上传编号已用于其他内容或归属"}
		}
		return nil
	})
	if err != nil { // 数据库提交响应可能丢失：保留私有文件，按原编号核对，不伪报接收。
		var rejection *BelloBuddyError
		if errors.As(err, &rejection) && rejection.Code == "UPLOAD_ID_CONFLICT" {
			_ = os.Remove(path)
		}
		return nil, err
	}
	if archive.StorageName != storageName {
		os.Remove(path)
	}
	return &archive, nil
}

func (engine *BelloBuddyResumeEngine) Get(userID int, id string) (*model.BelloBuddyResumeArchive, error) {
	var archive model.BelloBuddyResumeArchive
	if engine.DB.Where("archive_id = ?", id).First(&archive).Error != nil {
		return nil, ErrBelloBuddyNotFound
	}
	var scope model.BelloBuddyResumeScope
	if engine.DB.First(&scope, archive.ScopeID).Error != nil || scope.UserID != userID {
		return nil, ErrBelloBuddyNotFound
	}
	var account model.BelloBuddyAccount
	var job model.BelloBuddyJob
	if engine.DB.First(&account, scope.AccountID).Error != nil || engine.DB.First(&job, scope.JobID).Error != nil {
		return nil, ErrBelloBuddyNotFound
	}
	if _, _, _, err := engine.ownership(userID, account.AccountKey, job.JobID); err != nil {
		return nil, err
	}
	return &archive, nil
}

func (engine *BelloBuddyResumeEngine) GetByUpload(userID int, accountKey, jobID, uploadID string) (*model.BelloBuddyResumeArchive, error) {
	account, job, _, err := engine.ownership(userID, accountKey, jobID)
	if err != nil {
		return nil, err
	}
	var upload model.BelloBuddyResumeUpload
	if engine.DB.Where("user_id = ? AND upload_hash = ?", userID, model.BelloBuddyIdentityHash(uploadID)).First(&upload).Error != nil || upload.ClientUploadID != uploadID {
		return nil, ErrBelloBuddyNotFound
	}
	var archive model.BelloBuddyResumeArchive
	var scope model.BelloBuddyResumeScope
	if engine.DB.First(&archive, upload.ArchiveID).Error != nil || engine.DB.First(&scope, archive.ScopeID).Error != nil || scope.AccountID != account.ID || scope.JobID != job.ID {
		return nil, ErrBelloBuddyNotFound
	}
	return &archive, nil
}

func (engine *BelloBuddyResumeEngine) Reconcile(userID int, id string) (*model.BelloBuddyResumeArchive, error) {
	archive, err := engine.Get(userID, id)
	if err != nil {
		return nil, err
	}
	if archive.State != "blocked" && archive.State != "reconcile" {
		return archive, nil
	}
	if cli, ok := engine.Backend.(*FeishuArchiveCLI); ok {
		cli.InvalidateFields()
	}
	result := engine.DB.Model(archive).Where("id = ? AND state IN ?", archive.ID, []string{"blocked", "reconcile"}).Updates(map[string]any{"state": "queued", "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return nil, result.Error
	}
	return engine.Get(userID, id)
}

func (engine *BelloBuddyResumeEngine) CleanupVerifiedFiles(now int64) error {
	var archives []model.BelloBuddyResumeArchive
	// 未完成替换／外部核对的任务不自动清理唯一副本。
	if err := engine.DB.Where("state IN ? AND completed_at > 0 AND verified_at > 0 AND verified_at <= ? AND purged_at = 0", []string{"archived", "superseded"}, now-7*86400).Limit(100).Find(&archives).Error; err != nil {
		return err
	}
	for _, archive := range archives {
		if filepath.Base(archive.StorageName) != archive.StorageName || archive.StorageName != archive.ArchiveID+".pdf" {
			return &FeishuArchiveFailure{Code: "ARCHIVE_FILE_INVALID"}
		}
		err := os.Remove(filepath.Join(engine.Config.DataDir, "files", archive.StorageName))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := engine.DB.Model(&archive).Where("id = ? AND state IN ?", archive.ID, []string{"archived", "superseded"}).Update("purged_at", now).Error; err != nil {
			return err
		}
	}
	return nil
}

func (engine *BelloBuddyResumeEngine) RunOnce(ctx context.Context) error {
	key := model.BelloBuddyIdentityHash("feishu-base:" + engine.Config.Target.Base)
	lease, ok, err := model.ClaimBelloBuddyResumeLease(engine.DB, key, uuid.NewString(), time.Now().Unix())
	if err != nil || !ok {
		return err
	}
	keepBusy := lease.BusyArchiveID != 0 || lease.BusyJobSyncID != 0
	defer func() {
		if err := model.ReleaseBelloBuddyResumeLease(engine.DB, lease, keepBusy); err != nil {
			common.SysError("resume archive lease release failed")
		}
	}()
	if lease.BusyArchiveID == 0 {
		var queued int64
		if err := engine.DB.Model(&model.BelloBuddyJobCatalog{}).Where("state IN ?", []string{"queued", "processing"}).Count(&queued).Error; err != nil {
			return err
		}
		if lease.BusyJobSyncID != 0 || queued > 0 {
			return engine.runJobCatalog(ctx, lease, &keepBusy)
		}
	}
	var archive model.BelloBuddyResumeArchive
	query := engine.DB.Where("state IN ?", []string{"queued", "processing", "reconcile"})
	if lease.BusyArchiveID != 0 {
		query = query.Where("id = ?", lease.BusyArchiveID)
	}
	if err := query.Order("id").First(&archive).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if lease.BusyArchiveID != archive.ID {
		result := engine.DB.Model(lease).Where("id = ? AND owner = ?", lease.ID, lease.Owner).Update("busy_archive_id", archive.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return resumeUnknown("ARCHIVE_LEASE_LOST")
		}
	}
	lease.BusyArchiveID = archive.ID
	keepBusy = true
	if err := engine.DB.Model(&archive).Updates(map[string]any{"state": "processing", "updated_at": time.Now().Unix()}).Error; err != nil {
		return err
	}
	ctx = context.WithValue(ctx, resumeLeaseContextKey{}, resumeLeaseIdentity{Key: lease.ID, Owner: lease.Owner})
	err = engine.process(ctx, &archive)
	if err != nil {
		if engine.guardLease(ctx, archive.ID) != nil {
			return err
		}
		code := resumeFailureCode(err)
		missingScopes := ""
		var failure *FeishuArchiveFailure
		if errors.As(err, &failure) && len(failure.Scopes) > 0 {
			data, _ := common.Marshal(failure.Scopes)
			missingScopes = string(data)
		}
		if updateErr := engine.DB.Model(&archive).Updates(map[string]any{"state": "blocked", "error_code": code, "missing_scopes": missingScopes, "updated_at": time.Now().Unix()}).Error; updateErr != nil {
			return updateErr
		}
		// 外部调用前的配置／权限检查未产生效果，可以放行其他范围。
		var pending int64
		if engine.DB.Model(&model.BelloBuddyResumeAttempt{}).Where("archive_id = ? AND state IN ?", archive.ID, []string{"started", "unknown"}).Count(&pending).Error == nil && pending == 0 {
			keepBusy = false
		}
		return err
	}
	keepBusy = false
	return nil
}

func (engine *BelloBuddyResumeEngine) StorageStatus() (map[string]any, error) {
	var archives []model.BelloBuddyResumeArchive
	if err := engine.DB.Select("storage_name", "state", "received_at", "size", "purged_at").Find(&archives).Error; err != nil {
		return nil, err
	}
	known := map[string]bool{}
	counts := map[string]int{}
	oldest := int64(0)
	for _, task := range archives {
		known[task.StorageName] = true
		counts[task.State]++
		if task.State != "archived" && task.State != "superseded" && task.State != "abandoned" && (oldest == 0 || task.ReceivedAt < oldest) {
			oldest = task.ReceivedAt
		}
	}
	entries, err := os.ReadDir(filepath.Join(engine.Config.DataDir, "files"))
	if err != nil {
		return nil, err
	}
	used, orphans := int64(0), 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, resumeUnknown("ARCHIVE_FILE_INVALID")
		}
		used += info.Size()
		if !known[entry.Name()] {
			orphans++
		}
	}
	return map[string]any{"used_bytes": used, "max_bytes": engine.Config.MaxStorageBytes, "orphan_files": orphans, "counts": counts, "oldest_pending_at": oldest}, nil
}

type resumeLeaseContextKey struct{}
type resumeLeaseIdentity struct{ Key, Owner string }

func (engine *BelloBuddyResumeEngine) guardLease(ctx context.Context, archiveID int) error {
	identity, ok := ctx.Value(resumeLeaseContextKey{}).(resumeLeaseIdentity)
	if !ok {
		return resumeUnknown("ARCHIVE_LEASE_MISSING")
	}
	var count int64
	if err := engine.DB.Model(&model.BelloBuddyResumeLease{}).Where("id = ? AND owner = ? AND busy_archive_id = ? AND expires_at > ?", identity.Key, identity.Owner, archiveID, time.Now().Unix()).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return resumeUnknown("ARCHIVE_LEASE_LOST")
	}
	return nil
}

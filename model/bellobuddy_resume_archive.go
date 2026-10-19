package model

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 仅归档元信息；无简历正文、聊天、模型结果或客户端指定的服务器路径。
type BelloBuddyResumeScope struct {
	ID          int    `gorm:"primaryKey" json:"-"`
	ScopeKey    string `gorm:"type:varchar(64);not null;uniqueIndex" json:"-"`
	UserID      int    `gorm:"not null;index" json:"-"`
	AccountID   int    `gorm:"not null;index" json:"-"`
	JobID       int    `gorm:"not null;index" json:"-"`
	CandidateID string `gorm:"type:varchar(128);not null" json:"-"`
	RecordID    string `gorm:"type:varchar(128)" json:"-"`
	JobMapID    int    `gorm:"not null" json:"-"`
	Target      string `gorm:"type:text;not null" json:"-"`
	CreatedAt   int64  `json:"-"`
}

type BelloBuddyResumeJobMap struct {
	ID       int    `gorm:"primaryKey"`
	MapKey   string `gorm:"type:varchar(64);not null;uniqueIndex"`
	RecordID string `gorm:"type:varchar(128)"`
	// started 在外部调用前落库。没有可信回执不重新创建职位。
	AttemptID string `gorm:"type:varchar(36)"`
	// 已核验的系统字段，供职位同步与简历归档共同验证同一行。
	ManagedFields string `gorm:"type:text"`
}

type BelloBuddyResumeArchive struct {
	ID            int    `gorm:"primaryKey" json:"-"`
	ArchiveID     string `gorm:"type:varchar(36);not null;uniqueIndex" json:"archive_id"`
	ScopeID       int    `gorm:"not null;uniqueIndex:bello_resume_content,priority:1" json:"-"`
	SHA256        string `gorm:"type:varchar(64);not null;uniqueIndex:bello_resume_content,priority:2" json:"sha256"`
	CandidateName string `gorm:"type:varchar(128)" json:"-"`
	DownloadedAt  int64  `json:"downloaded_at"`
	Size          int64  `json:"size"`
	StorageName   string `gorm:"type:varchar(64);not null" json:"-"`
	State         string `gorm:"type:varchar(24);not null;index" json:"state"`
	Stage         string `gorm:"type:varchar(32);not null" json:"stage"`
	ErrorCode     string `gorm:"type:varchar(64)" json:"code,omitempty"`
	MissingScopes string `gorm:"type:text" json:"-"`
	AbandonedBy   int    `json:"-"`
	FileToken     string `gorm:"type:varchar(256)" json:"-"`
	AttemptID     string `gorm:"type:varchar(36)" json:"-"`
	ReceivedAt    int64  `json:"received_at"`
	VerifiedAt    int64  `json:"verified_at,omitempty"`
	CompletedAt   int64  `json:"completed_at,omitempty"`
	ArchiveTime   int64  `json:"-"`
	PurgedAt      int64  `json:"purged_at,omitempty"`
	UpdatedAt     int64  `json:"updated_at"`
}

// 每个上传编号独立保留接收回执，即使它合并到已有文件任务。
type BelloBuddyResumeUpload struct {
	ID             int    `gorm:"primaryKey"`
	UserID         int    `gorm:"not null;uniqueIndex:bello_resume_upload,priority:1"`
	UploadHash     string `gorm:"type:varchar(64);not null;uniqueIndex:bello_resume_upload,priority:2"`
	ClientUploadID string `gorm:"type:varchar(128);not null"`
	ArchiveID      int    `gorm:"not null;index"`
	ScopeKey       string `gorm:"type:varchar(64);not null"`
	SHA256         string `gorm:"type:varchar(64);not null"`
}

type BelloBuddyResumeAttempt struct {
	ID        string `gorm:"type:varchar(36);primaryKey" json:"-"`
	ArchiveID int    `gorm:"not null;index" json:"-"`
	JobSyncID int    `gorm:"not null;default:0;index" json:"-"`
	Operation string `gorm:"type:varchar(32);not null" json:"operation"`
	State     string `gorm:"type:varchar(24);not null" json:"state"`
	// 只保存结构化成功回执；不存 stderr、凭据或供应商原始错误文本。
	Receipt    string `gorm:"type:text" json:"-"`
	ErrorCode  string `gorm:"type:varchar(64)" json:"code,omitempty"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
}

// BusyArchiveID 不因租约到期清除。接管者只核对此任务，避免未知写入与新写入交叠。
type BelloBuddyResumeLease struct {
	ID            string `gorm:"type:varchar(64);primaryKey"`
	Owner         string `gorm:"type:varchar(36);not null"`
	ExpiresAt     int64  `gorm:"not null"`
	BusyArchiveID int    `gorm:"not null"`
	BusyJobSyncID int    `gorm:"not null;default:0"`
}

// 一行对应原有职位映射。新观察只更新期望值；在途外部尝试单独保留，不能被覆盖。
type BelloBuddyJobCatalog struct {
	ID            int    `gorm:"primaryKey" json:"-"`
	JobMapID      int    `gorm:"not null;uniqueIndex" json:"-"`
	UserID        int    `gorm:"not null;index" json:"-"`
	AccountID     int    `gorm:"not null;index" json:"-"`
	JobID         int    `gorm:"not null" json:"-"`
	PlatformJobID string `gorm:"type:varchar(128);not null" json:"job_id"`
	Target        string `gorm:"type:text;not null" json:"-"`
	Desired       string `gorm:"type:text;not null" json:"-"`
	ObservedAt    int64  `json:"observed_at"`
	Revision      int64  `gorm:"not null" json:"revision"`
	State         string `gorm:"type:varchar(24);not null;index" json:"state"`
	ErrorCode     string `gorm:"type:varchar(64)" json:"code,omitempty"`
	Warning       string `gorm:"type:varchar(64)" json:"warning,omitempty"`
	AttemptID     string `gorm:"type:varchar(36)" json:"-"`
	AttemptFields string `gorm:"type:text" json:"-"`
	SyncedAt      int64  `json:"synced_at,omitempty"`
}

// 接收响应丢失后按同一客户端编号查询；同编号不同内容拒绝。
type BelloBuddyJobSyncBatch struct {
	ID           int    `gorm:"primaryKey" json:"-"`
	SyncKey      string `gorm:"type:varchar(64);not null;uniqueIndex" json:"-"`
	ClientSyncID string `gorm:"type:varchar(128);not null" json:"client_sync_id"`
	UserID       int    `gorm:"not null;index" json:"-"`
	AccountID    int    `gorm:"not null" json:"-"`
	PayloadHash  string `gorm:"type:varchar(64);not null" json:"-"`
	CatalogIDs   string `gorm:"type:text;not null" json:"-"`
	ReceivedAt   int64  `json:"received_at"`
}

func BelloBuddyResumeModels() []any {
	return []any{&BelloBuddyResumeScope{}, &BelloBuddyResumeJobMap{}, &BelloBuddyResumeArchive{}, &BelloBuddyResumeUpload{}, &BelloBuddyResumeAttempt{}, &BelloBuddyResumeLease{}, &BelloBuddyJobCatalog{}, &BelloBuddyJobSyncBatch{}}
}

func LockBelloBuddyResumeScope(tx *gorm.DB, id int) (*BelloBuddyResumeScope, error) {
	var scope BelloBuddyResumeScope
	err := lockForUpdate(tx).First(&scope, id).Error
	return &scope, err
}

func ClaimBelloBuddyResumeLease(db *gorm.DB, key, owner string, now int64) (*BelloBuddyResumeLease, bool, error) {
	lease := BelloBuddyResumeLease{ID: key}
	err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&lease).Error
	if err != nil {
		return nil, false, err
	}
	// 显式主键条件，不依赖 INSERT ... ON CONFLICT 的结构体回填。
	result := db.Model(&BelloBuddyResumeLease{}).Where("id = ? AND expires_at <= ?", key, now).Updates(map[string]any{"owner": owner, "expires_at": now + 600})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, false, nil
	}
	lease = BelloBuddyResumeLease{}
	if err := db.First(&lease, "id = ? AND owner = ?", key, owner).Error; err != nil {
		return nil, false, err
	}
	return &lease, true, nil
}

func ReleaseBelloBuddyResumeLease(db *gorm.DB, lease *BelloBuddyResumeLease, keepBusy bool) error {
	values := map[string]any{"owner": "", "expires_at": int64(0)}
	if !keepBusy {
		values["busy_archive_id"] = 0
		values["busy_job_sync_id"] = 0
	}
	result := db.Model(lease).Where("id = ? AND owner = ?", lease.ID, lease.Owner).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("resume lease lost")
	}
	return nil
}

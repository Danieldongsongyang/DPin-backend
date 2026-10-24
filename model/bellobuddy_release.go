package model

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BelloBuddyReleaseProduct  = "bellobuddy"
	BelloBuddyReleaseStable   = "stable"
	BelloBuddyReleasePlatform = "win32"
	BelloBuddyReleaseArch     = "x64"

	BelloBuddyReleaseDraft      = "draft"
	BelloBuddyReleaseValidating = "validating"
	BelloBuddyReleaseReady      = "ready"
	BelloBuddyReleasePublished  = "published"
	BelloBuddyReleaseWithdrawn  = "withdrawn"

	// TEXT 在 MySQL 中最多 64 KiB；预留余量并限制单版本历史增长。
	BelloBuddyReleaseAcceptanceHistoryMaxBytes = 48 * 1024
)

var (
	ErrBelloBuddyReleaseConflict    = errors.New("发布数据已发生变化，请刷新后重试")
	ErrBelloBuddyReleaseState       = errors.New("当前版本状态不允许此操作")
	ErrBelloBuddyReleaseIdempotency = errors.New("相同幂等键已用于其他发布请求")
	ErrBelloBuddyReleaseInput       = errors.New("发布操作参数无效")
	ErrBelloBuddyReleaseVersion     = errors.New("正式渠道只接受高于历史已发布版本的稳定版本")
)

// 制品在 ready 后冻结；是否当前推荐由独立渠道行决定。
type BelloBuddyRelease struct {
	ID                   int    `json:"id" gorm:"primaryKey"`
	Product              string `json:"product" gorm:"type:varchar(32);not null;uniqueIndex:bello_release_version,priority:1"`
	Version              string `json:"version" gorm:"type:varchar(128);not null"`
	VersionHash          string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:bello_release_version,priority:2"`
	BuildID              string `json:"build_id" gorm:"type:varchar(128);not null"`
	Title                string `json:"title" gorm:"type:varchar(256);not null"`
	Notes                string `json:"notes" gorm:"type:text;not null"`
	MinimumSystemVersion string `json:"minimum_system_version" gorm:"type:varchar(64);not null"`
	Platform             string `json:"platform" gorm:"type:varchar(16);not null"`
	Arch                 string `json:"arch" gorm:"type:varchar(16);not null"`
	State                string `json:"state" gorm:"type:varchar(16);not null;index"`
	Disabled             bool   `json:"disabled" gorm:"not null"`
	DisabledReason       string `json:"disabled_reason" gorm:"type:varchar(512);not null"`
	ValidationReport     string `json:"-" gorm:"type:text;not null"`
	AcceptanceReport     string `json:"-" gorm:"type:text;not null"`
	AcceptanceHistory    string `json:"-" gorm:"type:text;not null"`
	Revision             int64  `json:"revision" gorm:"not null"`
	CreatedBy            int    `json:"created_by" gorm:"not null"`
	CreatedAt            int64  `json:"created_at" gorm:"not null"`
	UpdatedAt            int64  `json:"updated_at" gorm:"not null"`
	PublishedAt          int64  `json:"published_at" gorm:"not null"`
}

type BelloBuddyReleaseArtifact struct {
	ID         int    `json:"id" gorm:"primaryKey"`
	ReleaseID  int    `json:"release_id" gorm:"not null;uniqueIndex:bello_release_artifact,priority:1"`
	Kind       string `json:"kind" gorm:"type:varchar(16);not null;uniqueIndex:bello_release_artifact,priority:2"`
	Filename   string `json:"filename" gorm:"type:varchar(256);not null"`
	StorageKey string `json:"-" gorm:"type:varchar(512);not null"`
	Size       int64  `json:"size" gorm:"not null"`
	SHA256     string `json:"sha256" gorm:"type:varchar(64);not null"`
	SHA512     string `json:"sha512" gorm:"type:varchar(128);not null"`
	CreatedAt  int64  `json:"created_at" gorm:"not null"`
}

type BelloBuddyReleaseUpload struct {
	ID            int    `json:"id" gorm:"primaryKey"`
	UploadID      string `json:"upload_id" gorm:"type:varchar(36);not null;uniqueIndex"`
	ReleaseID     int    `json:"release_id" gorm:"not null;index"`
	Kind          string `json:"kind" gorm:"type:varchar(16);not null"`
	Filename      string `json:"filename" gorm:"type:varchar(256);not null"`
	State         string `json:"state" gorm:"type:varchar(16);not null"`
	BytesReceived int64  `json:"bytes_received" gorm:"not null"`
	ErrorCode     string `json:"code" gorm:"type:varchar(64);not null"`
	CreatedBy     int    `json:"created_by" gorm:"not null"`
	CreatedAt     int64  `json:"created_at" gorm:"not null"`
	UpdatedAt     int64  `json:"updated_at" gorm:"not null"`
}

type BelloBuddyReleaseChannel struct {
	ID           int    `json:"id" gorm:"primaryKey"`
	Product      string `json:"product" gorm:"type:varchar(32);not null;uniqueIndex:bello_release_channel,priority:1"`
	Channel      string `json:"channel" gorm:"type:varchar(16);not null;uniqueIndex:bello_release_channel,priority:2"`
	Platform     string `json:"platform" gorm:"type:varchar(16);not null;uniqueIndex:bello_release_channel,priority:3"`
	Arch         string `json:"arch" gorm:"type:varchar(16);not null;uniqueIndex:bello_release_channel,priority:4"`
	ReleaseID    int    `json:"release_id" gorm:"not null"`
	Revision     int64  `json:"revision" gorm:"not null"`
	VersionFloor string `json:"version_floor" gorm:"type:varchar(128);not null"`
	UpdatedAt    int64  `json:"updated_at" gorm:"not null"`
}

// 幂等记录和渠道目标在同一主库事务写入，不依赖单独日志库是否可用。
type BelloBuddyReleaseOperation struct {
	ID                      int    `json:"id" gorm:"primaryKey"`
	IdempotencyKey          string `json:"idempotency_key" gorm:"type:varchar(128);not null"`
	IdempotencyHash         string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	RequestFingerprint      string `json:"-" gorm:"type:varchar(64);not null"`
	ActorID                 int    `json:"actor_id" gorm:"not null"`
	Action                  string `json:"action" gorm:"type:varchar(16);not null"`
	ReleaseID               int    `json:"release_id" gorm:"not null;index"`
	FromReleaseID           int    `json:"from_release_id" gorm:"not null"`
	ToReleaseID             int    `json:"to_release_id" gorm:"not null"`
	PreviousChannelRevision int64  `json:"previous_channel_revision" gorm:"not null"`
	ChannelRevision         int64  `json:"channel_revision" gorm:"not null"`
	ReleaseRevision         int64  `json:"release_revision" gorm:"not null"`
	Reason                  string `json:"reason" gorm:"type:varchar(512);not null"`
	CreatedAt               int64  `json:"created_at" gorm:"not null"`
}

type BelloBuddyReleaseOperationInput struct {
	ActorID                 int
	ReleaseID               int
	ExpectedReleaseRevision int64
	ExpectedChannelRevision int64
	Action                  string
	Reason                  string
	IdempotencyKey          string
	RequestFingerprint      string
}

type BelloBuddyReleaseAcceptanceSnapshot struct {
	Revision   int64             `json:"revision"`
	RecordedAt int64             `json:"recorded_at"`
	Report     common.RawMessage `json:"report"`
}

func BelloBuddyReleaseModels() []any {
	return []any{&BelloBuddyRelease{}, &BelloBuddyReleaseArtifact{}, &BelloBuddyReleaseUpload{}, &BelloBuddyReleaseChannel{}, &BelloBuddyReleaseOperation{}}
}

func GetBelloBuddyRelease(db *gorm.DB, id int) (*BelloBuddyRelease, error) {
	var release BelloBuddyRelease
	err := db.First(&release, id).Error
	return &release, err
}

func CreateBelloBuddyRelease(db *gorm.DB, release *BelloBuddyRelease) error {
	if release == nil || release.CreatedBy <= 0 || release.Version == "" || len(release.Version) > 128 || len(release.BuildID) > 128 || len(release.Title) > 256 {
		return ErrBelloBuddyReleaseInput
	}
	now := common.GetTimestamp()
	release.ID = 0
	release.Product = BelloBuddyReleaseProduct
	release.VersionHash = BelloBuddyIdentityHash(release.Version)
	release.Platform = BelloBuddyReleasePlatform
	release.Arch = BelloBuddyReleaseArch
	release.State = BelloBuddyReleaseDraft
	release.Disabled = false
	release.DisabledReason = ""
	release.ValidationReport = ""
	release.AcceptanceReport = ""
	release.AcceptanceHistory = ""
	release.Revision = 1
	release.CreatedAt = now
	release.UpdatedAt = now
	release.PublishedAt = 0
	return db.Create(release).Error
}

// 回调必须仅使用传入的 tx 写元数据；大文件先写暂存，再在此锁内提交索引。
// 回调可调整状态、报告等字段，但不可修改版本身份及所有权。
func WithBelloBuddyRelease(db *gorm.DB, id int, expectedRevision int64, allowedStates []string, fn func(*gorm.DB, *BelloBuddyRelease) error) (*BelloBuddyRelease, error) {
	if id <= 0 || expectedRevision <= 0 || len(allowedStates) == 0 || fn == nil {
		return nil, ErrBelloBuddyReleaseInput
	}
	var updated BelloBuddyRelease
	err := db.Transaction(func(tx *gorm.DB) error {
		var release BelloBuddyRelease
		if err := lockForUpdate(tx).First(&release, id).Error; err != nil {
			return err
		}
		if release.Revision != expectedRevision {
			return ErrBelloBuddyReleaseConflict
		}
		if release.Disabled || !slices.Contains(allowedStates, release.State) {
			return ErrBelloBuddyReleaseState
		}
		original := release
		if err := fn(tx, &release); err != nil {
			return err
		}
		if release.ID != original.ID || release.Product != original.Product || release.Version != original.Version || release.VersionHash != original.VersionHash || release.Platform != original.Platform || release.Arch != original.Arch || release.CreatedBy != original.CreatedBy || release.CreatedAt != original.CreatedAt || release.PublishedAt != original.PublishedAt || release.Disabled != original.Disabled || release.DisabledReason != original.DisabledReason || release.AcceptanceHistory != original.AcceptanceHistory {
			return ErrBelloBuddyReleaseInput
		}
		if err := validateBelloBuddyReleaseMutation(&original, &release); err != nil {
			return err
		}
		release.Revision = expectedRevision + 1
		release.UpdatedAt = common.GetTimestamp()
		if release.AcceptanceReport != original.AcceptanceReport {
			if err := appendBelloBuddyReleaseAcceptance(&original, &release); err != nil {
				return err
			}
		}
		result := tx.Model(&BelloBuddyRelease{}).Where("id = ? AND revision = ?", id, expectedRevision).Select("build_id", "title", "notes", "minimum_system_version", "state", "validation_report", "acceptance_report", "acceptance_history", "revision", "updated_at").Updates(&release)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBelloBuddyReleaseConflict
		}
		updated = release
		return nil
	})
	return &updated, belloBuddyReleaseTransactionError(err)
}

// 当前报告用于发布门禁，历史只追加，禁止回调覆盖或清空旧证据。
func appendBelloBuddyReleaseAcceptance(before, after *BelloBuddyRelease) error {
	if len(before.AcceptanceHistory) > BelloBuddyReleaseAcceptanceHistoryMaxBytes || len(after.AcceptanceReport) > BelloBuddyReleaseAcceptanceHistoryMaxBytes {
		return ErrBelloBuddyReleaseInput
	}
	history := []BelloBuddyReleaseAcceptanceSnapshot{}
	if before.AcceptanceHistory != "" {
		if err := common.UnmarshalJsonStr(before.AcceptanceHistory, &history); err != nil {
			return ErrBelloBuddyReleaseInput
		}
	} else if before.AcceptanceReport != "" {
		// 兼容已有报告但尚无历史字段的代表性升级场景。
		history = append(history, BelloBuddyReleaseAcceptanceSnapshot{Revision: before.Revision, RecordedAt: before.UpdatedAt, Report: common.RawMessage(before.AcceptanceReport)})
	}
	report := after.AcceptanceReport
	if report == "" {
		report = "null"
	}
	history = append(history, BelloBuddyReleaseAcceptanceSnapshot{Revision: after.Revision, RecordedAt: after.UpdatedAt, Report: common.RawMessage(report)})
	data, err := common.Marshal(history)
	if err != nil || len(data) > BelloBuddyReleaseAcceptanceHistoryMaxBytes {
		return ErrBelloBuddyReleaseInput
	}
	after.AcceptanceHistory = string(data)
	return nil
}

// 已冻结制品的身份、说明和校验结论不可通过通用修订入口更改。
func validateBelloBuddyReleaseMutation(before, after *BelloBuddyRelease) error {
	if len(after.BuildID) > 128 || len(after.Title) > 256 || len(after.MinimumSystemVersion) > 64 {
		return ErrBelloBuddyReleaseInput
	}
	switch before.State {
	case BelloBuddyReleaseDraft:
		if after.State != BelloBuddyReleaseDraft && after.State != BelloBuddyReleaseValidating {
			return ErrBelloBuddyReleaseState
		}
	case BelloBuddyReleaseValidating:
		if after.State != BelloBuddyReleaseDraft && after.State != BelloBuddyReleaseReady {
			return ErrBelloBuddyReleaseState
		}
	case BelloBuddyReleaseReady, BelloBuddyReleasePublished, BelloBuddyReleaseWithdrawn:
		if after.State != before.State || after.BuildID != before.BuildID || after.Title != before.Title || after.Notes != before.Notes || after.MinimumSystemVersion != before.MinimumSystemVersion || after.ValidationReport != before.ValidationReport {
			return ErrBelloBuddyReleaseState
		}
	default:
		return ErrBelloBuddyReleaseState
	}
	return nil
}

func GetBelloBuddyReleaseChannel(db *gorm.DB) (*BelloBuddyReleaseChannel, error) {
	channel := BelloBuddyReleaseChannel{Product: BelloBuddyReleaseProduct, Channel: BelloBuddyReleaseStable, Platform: BelloBuddyReleasePlatform, Arch: BelloBuddyReleaseArch}
	err := db.Where("product = ? AND channel = ? AND platform = ? AND arch = ?", channel.Product, channel.Channel, channel.Platform, channel.Arch).First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &channel, nil
	}
	return &channel, err
}

// validate 在同一事务中核对冻结制品和可信验收证据。发布必须提供此门禁。
func ApplyBelloBuddyReleaseOperation(db *gorm.DB, input BelloBuddyReleaseOperationInput, validate func(*gorm.DB, *BelloBuddyRelease) error) (*BelloBuddyReleaseOperation, error) {
	if input.ActorID <= 0 || input.ReleaseID <= 0 || input.ExpectedReleaseRevision <= 0 || input.ExpectedChannelRevision < 0 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(input.RequestFingerprint) != 64 || len(input.Reason) > 512 || !slices.Contains([]string{"publish", "withdraw", "disable"}, input.Action) {
		return nil, ErrBelloBuddyReleaseInput
	}
	var operation BelloBuddyReleaseOperation
	err := db.Transaction(func(tx *gorm.DB) error {
		channel := BelloBuddyReleaseChannel{Product: BelloBuddyReleaseProduct, Channel: BelloBuddyReleaseStable, Platform: BelloBuddyReleasePlatform, Arch: BelloBuddyReleaseArch}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&channel).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("product = ? AND channel = ? AND platform = ? AND arch = ?", BelloBuddyReleaseProduct, BelloBuddyReleaseStable, BelloBuddyReleasePlatform, BelloBuddyReleaseArch).First(&channel).Error; err != nil {
			return err
		}
		keyHash := BelloBuddyIdentityHash(input.IdempotencyKey)
		err := tx.Where("idempotency_hash = ?", keyHash).First(&operation).Error
		if err == nil {
			if operation.IdempotencyKey != input.IdempotencyKey || operation.RequestFingerprint != input.RequestFingerprint || operation.ActorID != input.ActorID || operation.Action != input.Action || operation.ReleaseID != input.ReleaseID || operation.Reason != input.Reason || operation.PreviousChannelRevision != input.ExpectedChannelRevision || operation.ReleaseRevision != input.ExpectedReleaseRevision+1 {
				return ErrBelloBuddyReleaseIdempotency
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if channel.Revision != input.ExpectedChannelRevision {
			return ErrBelloBuddyReleaseConflict
		}
		var release BelloBuddyRelease
		if err := lockForUpdate(tx).First(&release, input.ReleaseID).Error; err != nil {
			return err
		}
		if release.Revision != input.ExpectedReleaseRevision {
			return ErrBelloBuddyReleaseConflict
		}
		if release.Product != BelloBuddyReleaseProduct || release.Platform != BelloBuddyReleasePlatform || release.Arch != BelloBuddyReleaseArch {
			return ErrBelloBuddyReleaseInput
		}
		fromID := channel.ReleaseID
		now := common.GetTimestamp()
		switch input.Action {
		case "publish":
			if release.Disabled || (release.State != BelloBuddyReleaseReady && release.State != BelloBuddyReleasePublished) || validate == nil {
				return ErrBelloBuddyReleaseState
			}
			if err := validateBelloBuddyStableVersion(release.Version, channel.VersionFloor); err != nil {
				return err
			}
			if err := validate(tx, &release); err != nil {
				return err
			}
			release.State = BelloBuddyReleasePublished
			if release.PublishedAt == 0 {
				release.PublishedAt = now
			}
			channel.ReleaseID = release.ID
			channel.VersionFloor = release.Version
		case "withdraw":
			if release.Disabled || release.State != BelloBuddyReleasePublished {
				return ErrBelloBuddyReleaseState
			}
			release.State = BelloBuddyReleaseWithdrawn
			if channel.ReleaseID == release.ID {
				channel.ReleaseID = 0
			}
		case "disable":
			if release.Disabled || strings.TrimSpace(input.Reason) == "" || !slices.Contains([]string{BelloBuddyReleaseReady, BelloBuddyReleasePublished, BelloBuddyReleaseWithdrawn}, release.State) {
				return ErrBelloBuddyReleaseState
			}
			release.Disabled = true
			release.DisabledReason = input.Reason
			if channel.ReleaseID == release.ID {
				channel.ReleaseID = 0
			}
		}
		release.Revision++
		release.UpdatedAt = now
		result := tx.Model(&BelloBuddyRelease{}).Where("id = ? AND revision = ?", release.ID, input.ExpectedReleaseRevision).Select("state", "disabled", "disabled_reason", "published_at", "revision", "updated_at").Updates(&release)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBelloBuddyReleaseConflict
		}
		channel.Revision++
		channel.UpdatedAt = now
		result = tx.Model(&BelloBuddyReleaseChannel{}).Where("id = ? AND revision = ?", channel.ID, input.ExpectedChannelRevision).Select("release_id", "revision", "version_floor", "updated_at").Updates(&channel)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBelloBuddyReleaseConflict
		}
		operation = BelloBuddyReleaseOperation{IdempotencyKey: input.IdempotencyKey, IdempotencyHash: keyHash, RequestFingerprint: input.RequestFingerprint, ActorID: input.ActorID, Action: input.Action, ReleaseID: release.ID, FromReleaseID: fromID, ToReleaseID: channel.ReleaseID, PreviousChannelRevision: input.ExpectedChannelRevision, ChannelRevision: channel.Revision, ReleaseRevision: release.Revision, Reason: input.Reason, CreatedAt: now}
		return tx.Create(&operation).Error
	})
	return &operation, belloBuddyReleaseTransactionError(err)
}

// 竞争导致事务未提交时返回明确冲突，不重放可能已消耗的上传或发布操作。
func belloBuddyReleaseTransactionError(err error) error {
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) && (sqliteError.Code()&0xff == 5 || sqliteError.Code()&0xff == 6) {
		return ErrBelloBuddyReleaseConflict
	}
	var postgresError interface{ SQLState() string }
	if errors.As(err, &postgresError) && (postgresError.SQLState() == "40001" || postgresError.SQLState() == "40P01") {
		return ErrBelloBuddyReleaseConflict
	}
	var mysqlError *mysqlDriver.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1205 || mysqlError.Number == 1213) {
		return ErrBelloBuddyReleaseConflict
	}
	return err
}

var belloStableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// 仅比较稳定 SemVer 的数字部分；+build 不改变版本优先级。
func validateBelloBuddyStableVersion(version, floor string) error {
	match := belloStableVersion.FindStringSubmatch(version)
	if match == nil {
		return ErrBelloBuddyReleaseVersion
	}
	var current [3]uint64
	for i := range current {
		value, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return ErrBelloBuddyReleaseVersion
		}
		current[i] = value
	}
	if floor == "" {
		return nil
	}
	previous := belloStableVersion.FindStringSubmatch(floor)
	if previous == nil {
		return fmt.Errorf("渠道历史版本记录无效: %w", ErrBelloBuddyReleaseVersion)
	}
	for i := range current {
		value, err := strconv.ParseUint(previous[i+1], 10, 64)
		if err != nil {
			return ErrBelloBuddyReleaseVersion
		}
		if current[i] > value {
			return nil
		}
		if current[i] < value {
			return ErrBelloBuddyReleaseVersion
		}
	}
	return ErrBelloBuddyReleaseVersion
}

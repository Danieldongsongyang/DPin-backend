package model

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 正文保持原始 JSON，权限与内容版本由独立字段管理。
type BelloBuddyPlan struct {
	ID            int    `json:"id" gorm:"primaryKey"`
	Kind          string `json:"kind" gorm:"type:varchar(32);not null;index"`
	Name          string `json:"name" gorm:"type:varchar(128);not null"`
	Visibility    string `json:"visibility" gorm:"type:varchar(16);not null;index"`
	OwnerID       int    `json:"owner_id" gorm:"not null;index"`
	Enabled       bool   `json:"enabled" gorm:"not null"`
	Content       string `json:"-" gorm:"type:text;not null"`
	SchemaVersion int    `json:"schema_version" gorm:"not null"`
	Version       int64  `json:"version" gorm:"not null"`
	FilterPlanID  int    `json:"filter_plan_id,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// 首次导入来源与当前方案分开保存，重复导入不覆盖管理员之后的编辑。
type BelloBuddyPlanSource struct {
	ID          int    `json:"-" gorm:"primaryKey"`
	SourceHash  string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	Namespace   string `json:"namespace" gorm:"type:varchar(128);not null"`
	Kind        string `json:"kind" gorm:"type:varchar(32);not null"`
	SourceID    string `json:"source_id" gorm:"type:varchar(128);not null"`
	ContentHash string `json:"-" gorm:"type:varchar(64);not null"`
	PlanID      int    `json:"plan_id" gorm:"not null;index"`
	CreatedAt   int64  `json:"created_at"`
}

func ReserveBelloBuddyPlanSource(tx *gorm.DB, source *BelloBuddyPlanSource) (bool, error) {
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(source)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		return true, nil
	}
	var stored BelloBuddyPlanSource
	err := lockForUpdate(tx).Where("source_hash = ?", source.SourceHash).First(&stored).Error
	if err == nil {
		*source = stored
	}
	return false, err
}

type BelloBuddyAccount struct {
	ID          int    `json:"id" gorm:"primaryKey"`
	UserID      int    `json:"-" gorm:"not null;uniqueIndex:bello_account_identity,priority:1"`
	AccountHash string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:bello_account_identity,priority:2"`
	AccountKey  string `json:"account_key" gorm:"type:varchar(128);not null"`
	// 招聘平台的稳定用户编号；AccountKey 仍是桌面账号槽位，重建槽位不能改变岗位归属。
	PlatformAccountID string `json:"platform_account_id,omitempty" gorm:"type:varchar(128);index"`
	Platform          string `json:"platform" gorm:"type:varchar(16);not null"`
	DisplayName       string `json:"display_name" gorm:"type:varchar(128)"`
	Version           int64  `json:"version" gorm:"not null"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

type BelloBuddyJob struct {
	ID                int    `json:"-" gorm:"primaryKey"`
	AccountID         int    `json:"-" gorm:"not null;uniqueIndex:bello_job_identity,priority:1"`
	JobHash           string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:bello_job_identity,priority:2"`
	JobID             string `json:"job_id" gorm:"type:varchar(128);not null"`
	Name              string `json:"name" gorm:"type:varchar(128)"`
	City              string `json:"city" gorm:"type:varchar(128)"`
	Online            bool   `json:"online" gorm:"not null"`
	FilterMode        string `json:"filter_mode" gorm:"type:varchar(16)"`
	FilterPlanID      int    `json:"filter_plan_id,omitempty"`
	CommunicationMode string `json:"communication_mode" gorm:"type:varchar(16)"`
	CommunicationID   int    `json:"communication_plan_id,omitempty"`
	GreetingLimit     int    `json:"greeting_limit"`
	BindingID         int    `json:"-" gorm:"index"`
	Version           int64  `json:"version" gorm:"not null"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

// 共享键由管理员确认，职位名称和城市只用于辅助定位，不能自动建立授权关联。
type BelloBuddyJobBinding struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	SharedKey string `json:"shared_key" gorm:"type:varchar(128);not null"`
	KeyHash   string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	Name      string `json:"name" gorm:"type:varchar(128);not null"`
	ChannelID int    `json:"channel_id" gorm:"not null"`
	Model     string `json:"model" gorm:"type:varchar(128)"`
	Enabled   bool   `json:"enabled" gorm:"not null"`
	Version   int64  `json:"version" gorm:"not null"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func GetBelloBuddyBindingForUpdate(tx *gorm.DB, id int) (*BelloBuddyJobBinding, error) {
	var binding BelloBuddyJobBinding
	err := lockForUpdate(tx).First(&binding, id).Error
	return &binding, err
}

func GetBelloBuddyAccountForUpdate(tx *gorm.DB, id int) (*BelloBuddyAccount, error) {
	var account BelloBuddyAccount
	err := lockForUpdate(tx).First(&account, id).Error
	return &account, err
}

// 哈希参与唯一索引，避免 MySQL 默认大小写不敏感排序规则合并不同的平台 ID。
func BelloBuddyIdentityHash(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func BelloBuddyVisiblePlans(db *gorm.DB, userID int) *gorm.DB {
	return db.Model(&BelloBuddyPlan{}).Where("(visibility = ? AND enabled = ?) OR (visibility = ? AND owner_id = ?)", "public", true, "private", userID)
}

// 所有职位写入及启动读取使用同一账号锁，保证批量配置和完整快照的一致性。
func WithBelloBuddyAccount(userID int, accountKey string, register bool, fn func(*gorm.DB, *BelloBuddyAccount) error) error {
	platform := ""
	if register {
		platform = "boss"
	}
	return WithBelloBuddyAccountPlatform(userID, accountKey, platform, register, fn)
}

var ErrBelloBuddyAccountPlatformConflict = errors.New("招聘账号的已登记渠道与本次渠道不一致")

// 平台在首次登记时固定；读取未指定平台时沿用已登记归属，不覆盖已有账号。
func WithBelloBuddyAccountPlatform(userID int, accountKey, platform string, register bool, fn func(*gorm.DB, *BelloBuddyAccount) error) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if register {
			now := common.GetTimestamp()
			account := BelloBuddyAccount{UserID: userID, AccountKey: accountKey, AccountHash: BelloBuddyIdentityHash(accountKey), Platform: platform, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
				return err
			}
		}
		var account BelloBuddyAccount
		if err := lockForUpdate(tx).Where("user_id = ? AND account_hash = ?", userID, BelloBuddyIdentityHash(accountKey)).First(&account).Error; err != nil {
			return err
		}
		if platform != "" && account.Platform != platform {
			return ErrBelloBuddyAccountPlatformConflict
		}
		return fn(tx, &account)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
}

func AdvanceBelloBuddyAccount(tx *gorm.DB, account *BelloBuddyAccount) error {
	now := common.GetTimestamp()
	result := tx.Model(&BelloBuddyAccount{}).Where("id = ? AND version = ?", account.ID, account.Version).Updates(map[string]any{
		"version": account.Version + 1, "updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrInvalidTransaction
	}
	account.Version++
	account.UpdatedAt = now
	return nil
}

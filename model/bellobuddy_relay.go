package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const TokenManagedByBelloBuddy = "bellobuddy"

// 关联保留在 Token 删除之后，使后续登录不能静默创建替代凭据。
type BelloBuddyRelayIdentity struct {
	UserID    int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	TokenID   int   `json:"-" gorm:"not null;uniqueIndex"`
	CreatedAt int64 `json:"-"`
}

func GetOrCreateBelloBuddyRelayToken(userID int) (*Token, error) {
	var token Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "status").First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrTokenInvalid
		}
		var identity BelloBuddyRelayIdentity
		err := tx.First(&identity, "user_id = ?", userID).Error
		if err == nil {
			if err := tx.First(&token, "id = ? AND user_id = ?", identity.TokenID, userID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrTokenInvalid
				}
				return err
			}
			if token.ManagedBy != TokenManagedByBelloBuddy {
				return ErrTokenInvalid
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		token = Token{
			UserId: userID, Key: key, Status: common.TokenStatusEnabled, Name: "BelloBuddy",
			ManagedBy: TokenManagedByBelloBuddy, CreatedTime: now, AccessedTime: now,
			ExpiredTime: -1, UnlimitedQuota: true,
		}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		return tx.Create(&BelloBuddyRelayIdentity{UserID: userID, TokenID: token.Id, CreatedAt: now}).Error
	})
	if err != nil {
		return nil, err
	}
	return &token, nil
}

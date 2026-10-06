package service

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BelloBuddyBindingInput struct {
	SharedKey       string `json:"shared_key"`
	Name            string `json:"name"`
	ChannelID       int    `json:"channel_id"`
	Model           string `json:"model"`
	Enabled         *bool  `json:"enabled"`
	ExpectedVersion int64  `json:"expected_version"`
}

type BelloBuddyJobMappingInput struct {
	JobConfigID     int   `json:"job_config_id"`
	ExpectedVersion int64 `json:"expected_version"`
}

type BelloBuddyBindingJobsInput struct {
	ExpectedVersion int64                       `json:"expected_version"`
	Jobs            []BelloBuddyJobMappingInput `json:"jobs"`
}

type BelloBuddyJobCandidate struct {
	JobConfigID int    `json:"job_config_id"`
	AccountID   int    `json:"account_id"`
	UserID      int    `json:"user_id"`
	AccountName string `json:"account_name"`
	JobID       string `json:"job_id"`
	Name        string `json:"name"`
	City        string `json:"city"`
	Online      bool   `json:"online"`
	BindingID   int    `json:"binding_id"`
	Version     int64  `json:"version"`
}

func SaveBelloBuddyJobBinding(bindingID int, input BelloBuddyBindingInput) (*model.BelloBuddyJobBinding, error) {
	if err := ValidateBelloBuddyIdentifier(input.SharedKey); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Name) == "" || utf8.RuneCountInString(input.Name) > 128 || utf8.RuneCountInString(input.Model) > 128 || input.Enabled == nil || input.ChannelID < 0 {
		return nil, belloInvalid("岗位绑定名称、渠道或启用状态无效")
	}
	var binding model.BelloBuddyJobBinding
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if *input.Enabled && (input.ChannelID == 0 || strings.TrimSpace(input.Model) == "") {
			return belloInvalid("启用机器人绑定必须指定渠道和逻辑模型")
		}
		if input.ChannelID > 0 {
			var channel model.Channel
			if err := tx.Select("id", "type", "status", "models").First(&channel, input.ChannelID).Error; err != nil {
				return err
			}
			if channel.Type != constant.ChannelTypeMaxKB || !slices.Contains(channel.GetModels(), input.Model) || (*input.Enabled && channel.Status != common.ChannelStatusEnabled) {
				return belloInvalid("绑定必须使用提供指定模型的 MaxKB 渠道，启用时渠道必须可用")
			}
		}
		now := common.GetTimestamp()
		if bindingID == 0 {
			if input.ExpectedVersion != 0 {
				return belloInvalid("新机器人绑定不能提供旧版本")
			}
			binding = model.BelloBuddyJobBinding{SharedKey: input.SharedKey, KeyHash: model.BelloBuddyIdentityHash(input.SharedKey), Name: input.Name, ChannelID: input.ChannelID, Model: input.Model, Enabled: *input.Enabled, Version: 1, CreatedAt: now, UpdatedAt: now}
			created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected != 1 {
				return ErrBelloBuddyConflict
			}
			return nil
		}
		stored, err := model.GetBelloBuddyBindingForUpdate(tx, bindingID)
		if err != nil {
			return err
		}
		binding = *stored
		if input.SharedKey != binding.SharedKey || input.ExpectedVersion <= 0 || input.ExpectedVersion != binding.Version {
			return ErrBelloBuddyConflict
		}
		updated := tx.Model(&model.BelloBuddyJobBinding{}).Where("id = ? AND version = ?", bindingID, binding.Version).Updates(map[string]any{
			"name": input.Name, "channel_id": input.ChannelID, "model": input.Model, "enabled": *input.Enabled, "version": binding.Version + 1, "updated_at": now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrBelloBuddyConflict
		}
		return tx.First(&binding, bindingID).Error
	})
	return &binding, err
}

// 显式确认的职位集合可来自不同 DPin 用户；不凭展示名称自动绑定其他职位。
func MapBelloBuddyJobsToBinding(bindingID int, input BelloBuddyBindingJobsInput) error {
	if len(input.Jobs) == 0 || len(input.Jobs) > BelloBuddyMaxJobs || input.ExpectedVersion <= 0 {
		return belloInvalid("岗位映射必须包含明确的职位集合和绑定版本")
	}
	ids := make([]int, 0, len(input.Jobs))
	expected := make(map[int]int64, len(input.Jobs))
	for _, job := range input.Jobs {
		if job.JobConfigID <= 0 || job.ExpectedVersion <= 0 || expected[job.JobConfigID] != 0 {
			return belloInvalid("职位映射 ID、版本或重复项无效")
		}
		ids = append(ids, job.JobConfigID)
		expected[job.JobConfigID] = job.ExpectedVersion
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		binding, err := model.GetBelloBuddyBindingForUpdate(tx, bindingID)
		if err != nil {
			return err
		}
		if binding.Version != input.ExpectedVersion {
			return ErrBelloBuddyConflict
		}
		var jobs []model.BelloBuddyJob
		if err := tx.Where("id IN ?", ids).Order("account_id, id").Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) != len(ids) {
			return ErrBelloBuddyNotFound
		}
		// 每个账号的版本一起推进；CAS 与普通用户的批量选择采用相同版本约束。
		accounts := make(map[int]bool)
		for _, job := range jobs {
			if job.Version != expected[job.ID] {
				return ErrBelloBuddyConflict
			}
			if !accounts[job.AccountID] {
				account, err := model.GetBelloBuddyAccountForUpdate(tx, job.AccountID)
				if err != nil {
					return err
				}
				if err := AdvanceBelloBuddyAccount(tx, account); err != nil {
					return err
				}
				accounts[job.AccountID] = true
			}
			result := tx.Model(&model.BelloBuddyJob{}).Where("id = ? AND version = ?", job.ID, job.Version).Updates(map[string]any{"binding_id": bindingID, "version": job.Version + 1, "updated_at": common.GetTimestamp()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrBelloBuddyConflict
			}
		}
		return nil
	})
}

func GetBelloBuddyBotStatus(tx *gorm.DB, job model.BelloBuddyJob) (string, int64, error) {
	if job.BindingID == 0 {
		return "BOT_NOT_CONFIGURED", 0, nil
	}
	var binding model.BelloBuddyJobBinding
	err := tx.First(&binding, job.BindingID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "BOT_NOT_CONFIGURED", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if !binding.Enabled || binding.ChannelID == 0 || binding.Model == "" {
		return "BOT_NOT_CONFIGURED", binding.Version, nil
	}
	var channel model.Channel
	err = tx.Select("id", "type", "status", "models").First(&channel, binding.ChannelID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "BOT_UNAVAILABLE", binding.Version, nil
	}
	if err != nil {
		return "", 0, err
	}
	if channel.Type != constant.ChannelTypeMaxKB || channel.Status != common.ChannelStatusEnabled || !slices.Contains(channel.GetModels(), binding.Model) {
		return "BOT_UNAVAILABLE", binding.Version, nil
	}
	return "BOT_CONFIGURED", binding.Version, nil
}

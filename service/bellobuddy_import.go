package service

import (
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type BelloBuddyPlanImportItem struct {
	BelloBuddyPlanInput
	SourceID       string `json:"source_id"`
	FilterSourceID string `json:"filter_source_id"`
}

type BelloBuddyPlanImportInput struct {
	Namespace string                     `json:"namespace"`
	Plans     []BelloBuddyPlanImportItem `json:"plans"`
}

type BelloBuddyPlanImportResult struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	SourceID  string `json:"source_id"`
	PlanID    int    `json:"plan_id"`
	Version   int64  `json:"version"`
	Created   bool   `json:"created"`
}

var ErrBelloBuddyImportConflict = &BelloBuddyError{http.StatusConflict, "IMPORT_SOURCE_CONFLICT", "导入来源已存在但内容不同，请使用方案编辑接口处理变更"}

func ImportBelloBuddyPlans(input BelloBuddyPlanImportInput) ([]BelloBuddyPlanImportResult, error) {
	if err := ValidateBelloBuddyIdentifier(input.Namespace); err != nil {
		return nil, err
	}
	if len(input.Plans) == 0 || len(input.Plans) > 100 {
		return nil, belloInvalid("导入必须包含 1 至 100 个已登记方案")
	}
	items := slices.Clone(input.Plans)
	// 先建立筛选方案映射，再解析沟通正文中的旧筛选引用。
	slices.SortStableFunc(items, func(a, b BelloBuddyPlanImportItem) int {
		if a.Kind == b.Kind {
			return 0
		}
		if a.Kind == "filter" {
			return -1
		}
		return 1
	})
	seen := make(map[string]bool)
	for _, item := range items {
		if err := ValidateBelloBuddyIdentifier(item.SourceID); err != nil {
			return nil, err
		}
		if !slices.Contains([]string{"filter", "communication"}, item.Kind) || item.ExpectedVersion != 0 || item.FilterPlanID != 0 {
			return nil, belloInvalid("导入类型无效，关联必须使用旧来源 ID 而非服务端 ID")
		}
		key := item.Kind + "\x00" + item.SourceID
		if seen[key] {
			return nil, belloInvalid("重复的方案导入来源")
		}
		seen[key] = true
		var legacy struct {
			ID        string `json:"id"`
			FilterDef *struct {
				ID string `json:"id"`
			} `json:"filter_def"`
		}
		if err := common.Unmarshal(item.Content, &legacy); err != nil || legacy.ID != item.SourceID {
			return nil, belloInvalid("完整正文 ID 与导入来源不一致")
		}
		if item.Kind == "filter" && item.FilterSourceID != "" {
			return nil, belloInvalid("筛选方案不能携带筛选来源关联")
		}
		if item.Kind == "communication" {
			legacyFilterID := ""
			if legacy.FilterDef != nil {
				legacyFilterID = legacy.FilterDef.ID
			}
			if item.FilterSourceID != legacyFilterID {
				return nil, belloInvalid("沟通方案的旧筛选引用与导入映射不一致")
			}
			if item.FilterSourceID != "" {
				if err := ValidateBelloBuddyIdentifier(item.FilterSourceID); err != nil {
					return nil, err
				}
			}
		}
	}
	results := make([]BelloBuddyPlanImportResult, 0, len(items))
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			// 摘要使用旧来源引用，避免当前方案的编辑、禁用改变重复导入的判定。
			canonical, err := common.Marshal(item)
			if err != nil {
				return err
			}
			source := model.BelloBuddyPlanSource{
				Namespace: input.Namespace, Kind: item.Kind, SourceID: item.SourceID,
				SourceHash:  model.BelloBuddyIdentityHash(input.Namespace + "\x00" + item.Kind + "\x00" + item.SourceID),
				ContentHash: model.BelloBuddyIdentityHash(string(canonical)), CreatedAt: common.GetTimestamp(),
			}
			created, err := model.ReserveBelloBuddyPlanSource(tx, &source)
			if err != nil {
				return err
			}
			var plan model.BelloBuddyPlan
			if created {
				if item.FilterSourceID != "" {
					var filterSource model.BelloBuddyPlanSource
					hash := model.BelloBuddyIdentityHash(input.Namespace + "\x00filter\x00" + item.FilterSourceID)
					if err := tx.Where("source_hash = ?", hash).First(&filterSource).Error; err != nil {
						return err
					}
					var filter model.BelloBuddyPlan
					if err := tx.Where("id = ? AND kind = ? AND visibility = ? AND enabled = ?", filterSource.PlanID, "filter", "public", true).First(&filter).Error; err != nil {
						return err
					}
					item.FilterPlanID = filter.ID
				}
				if err := ValidateBelloBuddyPlan(item.BelloBuddyPlanInput); err != nil {
					return err
				}
				now := common.GetTimestamp()
				plan = model.BelloBuddyPlan{Kind: item.Kind, Name: item.Name, Visibility: "public", OwnerID: 0, Enabled: *item.Enabled, Content: string(item.Content), SchemaVersion: item.SchemaVersion, Version: 1, FilterPlanID: item.FilterPlanID, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&plan).Error; err != nil {
					return err
				}
				if err := tx.Model(&source).Update("plan_id", plan.ID).Error; err != nil {
					return err
				}
			} else {
				if source.ContentHash != model.BelloBuddyIdentityHash(string(canonical)) {
					return ErrBelloBuddyImportConflict
				}
				if err := tx.Where("id = ? AND kind = ? AND visibility = ?", source.PlanID, item.Kind, "public").First(&plan).Error; err != nil {
					return err
				}
			}
			results = append(results, BelloBuddyPlanImportResult{Namespace: input.Namespace, Kind: item.Kind, SourceID: item.SourceID, PlanID: plan.ID, Version: plan.Version, Created: created})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

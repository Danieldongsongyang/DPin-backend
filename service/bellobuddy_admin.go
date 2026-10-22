package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// 管理员读取投影不改变方案归属，用户名只用于展示。
type BelloBuddyAdminPlan struct {
	model.BelloBuddyPlan
	OwnerUsername string `json:"owner_username"`
}

type BelloBuddyAdminPlanDetail struct {
	BelloBuddyAdminPlan
	Content     common.RawMessage `json:"content"`
	ContentJSON string            `json:"content_json"`
}

func BelloBuddyAdminPlans(db *gorm.DB, all bool) *gorm.DB {
	query := db.Model(&model.BelloBuddyPlan{})
	if !all {
		query = query.Where("bello_buddy_plans.visibility = ?", "public")
	}
	return query
}

func BelloBuddyAdminPlanOwners(query *gorm.DB) *gorm.DB {
	return query.Joins("LEFT JOIN users AS bello_plan_owner ON bello_plan_owner.id = bello_buddy_plans.owner_id").
		Select("bello_buddy_plans.*, COALESCE(bello_plan_owner.username, '') AS owner_username")
}

func GetBelloBuddyAdminPlan(db *gorm.DB, id int, all bool) (*BelloBuddyAdminPlanDetail, error) {
	var plan BelloBuddyAdminPlan
	err := BelloBuddyAdminPlanOwners(BelloBuddyAdminPlans(db, all)).Where("bello_buddy_plans.id = ?", id).First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBelloBuddyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &BelloBuddyAdminPlanDetail{BelloBuddyAdminPlan: plan, Content: common.RawMessage(plan.BelloBuddyPlan.Content), ContentJSON: plan.BelloBuddyPlan.Content}, nil
}

// 关联权限按被编辑方案的归属判断，不借用管理员可见的其他用户私人方案。
func BelloBuddyPlanFilterScope(query *gorm.DB, target *model.BelloBuddyPlan) *gorm.DB {
	query = query.Where("bello_buddy_plans.kind = ?", "filter")
	if target.Visibility == "public" {
		return query.Where("bello_buddy_plans.visibility = ?", "public")
	}
	return query.Where("bello_buddy_plans.visibility = ? OR (bello_buddy_plans.visibility = ? AND bello_buddy_plans.owner_id = ?)", "public", "private", target.OwnerID)
}

func SaveBelloBuddyAdminPlan(userID, planID int, all bool, input BelloBuddyPlanInput) (*BelloBuddyPlanDetail, error) {
	return saveBelloBuddyPlan(userID, planID, true, all, input)
}

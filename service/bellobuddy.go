package service

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	BelloBuddySchemaVersion = 1
	BelloBuddyMaxBodyBytes  = 2 << 20
	// 为 MySQL TEXT 的 65535 字节上限保留空间，三种数据库采用一致边界。
	BelloBuddyMaxPlanBytes = 60 << 10
	BelloBuddyMaxJobs      = 200
)

type BelloBuddyError struct {
	Status  int
	Code    string
	Message string
}

func (e *BelloBuddyError) Error() string { return e.Message }

var (
	ErrBelloBuddyNotFound = &BelloBuddyError{http.StatusNotFound, "RESOURCE_NOT_FOUND", "方案、账号或职位不存在或不可访问"}
	ErrBelloBuddyConflict = &BelloBuddyError{http.StatusConflict, "VERSION_CONFLICT", "配置已更新，请重新读取后保存"}
	ErrBelloBuddyConfig   = &BelloBuddyError{http.StatusConflict, "CONFIG_INVALID", "职位配置未完成或关联方案无效"}
)

type BelloBuddyPlanInput struct {
	Kind            string            `json:"kind"`
	Name            string            `json:"name"`
	Enabled         *bool             `json:"enabled"`
	SchemaVersion   int               `json:"schema_version"`
	ExpectedVersion int64             `json:"expected_version"`
	FilterPlanID    int               `json:"filter_plan_id"`
	Content         common.RawMessage `json:"content"`
}

type BelloBuddyPlanDetail struct {
	model.BelloBuddyPlan
	Content common.RawMessage `json:"content"`
}

type BelloBuddyJobInput struct {
	JobID  string `json:"job_id"`
	Name   string `json:"name"`
	City   string `json:"city"`
	Online *bool  `json:"online"`
}

type BelloBuddySyncInput struct {
	DisplayName string               `json:"display_name"`
	Jobs        []BelloBuddyJobInput `json:"jobs"`
}

type BelloBuddyJobConfigInput struct {
	JobID             string `json:"job_id"`
	ExpectedVersion   int64  `json:"expected_version"`
	FilterMode        string `json:"filter_mode"`
	FilterPlanID      int    `json:"filter_plan_id"`
	CommunicationMode string `json:"communication_mode"`
	CommunicationID   int    `json:"communication_plan_id"`
	GreetingLimit     *int   `json:"greeting_limit"`
}

type BelloBuddyJobsConfig struct {
	Account model.BelloBuddyAccount `json:"account"`
	Jobs    []model.BelloBuddyJob   `json:"jobs"`
}

type BelloBuddyRuntimeInput struct {
	JobIDs        []string `json:"job_ids"`
	RunnerVersion string   `json:"runner_version"`
	SchemaVersion int      `json:"schema_version"`
}

type BelloBuddyRuntimeJob struct {
	model.BelloBuddyJob
	BotStatus         string `json:"bot_status"`
	BotBindingVersion int64  `json:"bot_binding_version"`
}

type BelloBuddyRuntimeConfig struct {
	Account       model.BelloBuddyAccount `json:"account"`
	SchemaVersion int                     `json:"schema_version"`
	FetchedAt     int64                   `json:"fetched_at"`
	Jobs          []BelloBuddyRuntimeJob  `json:"jobs"`
	Plans         []BelloBuddyPlanDetail  `json:"plans"`
	// 按服务端 ID 记录旧正文 ID，复制方案保留旧 ID 时也不会合并或串用。
	PlanAliases map[int]string `json:"plan_aliases"`
}

func belloInvalid(message string) error {
	return &BelloBuddyError{http.StatusBadRequest, "INVALID_REQUEST", message}
}

func ValidateBelloBuddyIdentifier(value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return belloInvalid("账号和职位必须使用有效的稳定 ID")
	}
	return nil
}

// 已知字段按真实对象校验类型；不重新编码正文，保留未知字段、大整数和缺失状态。
func ValidateBelloBuddyPlan(input BelloBuddyPlanInput) error {
	if !slices.Contains([]string{"filter", "communication"}, input.Kind) || input.SchemaVersion != BelloBuddySchemaVersion {
		return belloInvalid("不支持的方案类型或 Schema 版本")
	}
	if strings.TrimSpace(input.Name) == "" || utf8.RuneCountInString(input.Name) > 128 || input.Enabled == nil || input.FilterPlanID < 0 {
		return belloInvalid("方案名称、启用状态或筛选关联无效")
	}
	if len(input.Content) == 0 || len(input.Content) > BelloBuddyMaxPlanBytes || common.GetJsonType(input.Content) != "object" {
		return belloInvalid("方案必须包含完整 JSON 对象")
	}
	var fields map[string]common.RawMessage
	if err := common.Unmarshal(input.Content, &fields); err != nil || fields == nil {
		return belloInvalid("方案 JSON 无效")
	}
	nameKey := "name"
	marker := "FilterDef"
	if input.Kind == "communication" {
		nameKey, marker = "scheme_name", "BeiliaoConfig"
	}
	var name string
	var markers []string
	if common.Unmarshal(fields[nameKey], &name) != nil || name != input.Name || common.Unmarshal(fields["schema_defs"], &markers) != nil || !slices.Contains(markers, marker) {
		return belloInvalid("方案名称或类型声明与完整正文不一致")
	}
	for _, key := range []string{"api_key", "access_token", "refresh_token", "authorization"} {
		if _, ok := fields[key]; ok {
			return belloInvalid("方案不得保存认证凭据")
		}
	}
	booleanFields := []string{"disabled", "builtin"}
	if input.Kind == "filter" {
		booleanFields = append(booleanFields, "enable_quick_screening", "enable_ai_screening")
	} else {
		booleanFields = append(booleanFields, "skip_delivered_toggle", "conditions_as_match", "direct_inquiry_as_match", "auto_greeting_enabled", "auto_initiate_after_response", "standard_response_enabled", "request_phone_exchange", "request_attachment_resume", "request_wechat_exchange", "exchange_contact_after_screening_pass", "skip_inquiry_on_abandoned_intent", "mark_unprocessable_sessions", "follow_up_message_enabled")
	}
	for _, key := range booleanFields {
		if value, ok := fields[key]; ok && common.GetJsonType(value) != "boolean" {
			return belloInvalid("方案布尔字段类型错误：" + key)
		}
	}
	if raw, ok := fields["disabled"]; ok {
		var disabled bool
		if common.Unmarshal(raw, &disabled) != nil || disabled == *input.Enabled {
			return belloInvalid("方案启用状态与正文 disabled 不一致")
		}
	}
	if err := validateBelloBuddyContentFields(input.Kind, fields); err != nil {
		return err
	}
	if input.Kind == "filter" {
		if input.FilterPlanID != 0 {
			return belloInvalid("筛选方案不能引用其他筛选方案")
		}
		for _, key := range []string{"any_fails", "any_passes"} {
			if common.GetJsonType(fields[key]) != "array" {
				return belloInvalid("筛选方案缺少完整规则数组：" + key)
			}
		}
		return nil
	}
	var screening bool
	_ = common.Unmarshal(fields["conditions_as_match"], &screening)
	if screening && input.FilterPlanID <= 0 {
		return belloInvalid("启用沟通筛选时必须提供服务端筛选方案关联")
	}
	return nil
}

func validateBelloBuddyContentFields(kind string, fields map[string]common.RawMessage) error {
	types := map[string][]string{
		"string": {"id", "scope", "created_at", "updated_at", "visibility"},
		"array":  {"schema_defs", "scope_value"},
		"object": {"user", "owned_company", "updated_by", "snapshot"},
		"number": {"version"},
	}
	if kind == "filter" {
		types["string"] = append(types["string"], "name", "ai_screening_mode", "ai_screening_guidance", "ai_screening_summary_length", "recruitment_type", "source")
		types["array"] = append(types["array"], "any_fails", "any_passes")
		types["object"] = append(types["object"], "ai_screening_criteria")
	} else {
		types["string"] = append(types["string"], "scheme_name", "position_background_info", "non_match_action", "auto_receive_delivery", "screening_order", "ai_communication_style", "rejection_response_strategy", "new_message_response_strategy", "request_resume_phrase")
		types["number"] = append(types["number"], "candidate_unresponsive_timeout", "skip_delivered_timeout", "max_session_rounds", "follow_up_duration_days", "daily_follow_up_count")
		types["array"] = append(types["array"], "rejection_keywords")
		types["object"] = append(types["object"], "filter_def", "standard_response_settings", "wechat_exchange_reply_phrase", "additional_candidate_info_needed", "info_screening_fail_action", "list_addition_restrictions", "rejection_phrase", "end_session_guidance_phrase", "follow_up_templates")
	}
	for fieldType, keys := range types {
		for _, key := range keys {
			value, exists := fields[key]
			if !exists {
				continue
			}
			if common.GetJsonType(value) != fieldType {
				return belloInvalid("方案字段类型错误：" + key)
			}
			if fieldType == "number" {
				var number int64
				if common.Unmarshal(value, &number) != nil || number < 0 {
					return belloInvalid("方案数量必须为非负整数：" + key)
				}
			}
		}
	}
	if kind != "filter" {
		return nil
	}
	for _, key := range []string{"any_fails", "any_passes"} {
		var rules []struct {
			Conditions  []map[string]common.RawMessage `json:"conditions"`
			Description *string                        `json:"desc"`
		}
		if common.Unmarshal(fields[key], &rules) != nil {
			return belloInvalid("筛选规则结构错误：" + key)
		}
		for _, rule := range rules {
			if len(rule.Conditions) == 0 {
				return belloInvalid("筛选规则必须包含条件")
			}
			for _, condition := range rule.Conditions {
				if len(condition) == 0 {
					return belloInvalid("筛选条件不能为空")
				}
				if raw, ok := condition["ages"]; ok {
					var ranges []struct {
						Min          *int  `json:"min"`
						Max          *int  `json:"max"`
						ExclusiveMin *bool `json:"exclusive_min"`
						ExclusiveMax *bool `json:"exclusive_max"`
					}
					if common.Unmarshal(raw, &ranges) != nil || len(ranges) == 0 {
						return belloInvalid("年龄条件结构错误")
					}
					for _, interval := range ranges {
						if (interval.Min == nil && interval.Max == nil) || (interval.Min != nil && *interval.Min < 0) || (interval.Max != nil && *interval.Max < 0) || (interval.Min != nil && interval.Max != nil && *interval.Min > *interval.Max) {
							return belloInvalid("年龄边界无效")
						}
					}
				}
				if raw, ok := condition["edu_nature"]; ok {
					var values []string
					if common.Unmarshal(raw, &values) != nil || len(values) == 0 {
						return belloInvalid("教育性质条件无效")
					}
				}
				if raw, ok := condition["edu_nature_must_all_search_by_relation"]; ok && common.GetJsonType(raw) != "boolean" {
					return belloInvalid("教育性质关系标志必须为布尔值")
				}
			}
		}
	}
	return nil
}

func GetBelloBuddyPlan(db *gorm.DB, userID, planID int, kind string, enabled bool) (*model.BelloBuddyPlan, error) {
	var plan model.BelloBuddyPlan
	query := model.BelloBuddyVisiblePlans(db, userID).Where("id = ?", planID)
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if enabled {
		query = query.Where("enabled = ?", true)
	}
	if err := query.First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBelloBuddyNotFound
		}
		return nil, err
	}
	return &plan, nil
}

func SaveBelloBuddyPlan(userID, planID int, public bool, input BelloBuddyPlanInput) (*BelloBuddyPlanDetail, error) {
	if err := ValidateBelloBuddyPlan(input); err != nil {
		return nil, err
	}
	var plan model.BelloBuddyPlan
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		visibility, ownerID := "private", userID
		if public {
			visibility, ownerID = "public", 0
		}
		if input.FilterPlanID != 0 {
			filter, err := GetBelloBuddyPlan(tx, userID, input.FilterPlanID, "filter", true)
			if err != nil {
				return err
			}
			if public && filter.Visibility != "public" {
				return belloInvalid("公用沟通方案只能关联公用筛选方案")
			}
		}
		now := common.GetTimestamp()
		if planID == 0 {
			if input.ExpectedVersion != 0 {
				return belloInvalid("新方案不能提供旧版本")
			}
			plan = model.BelloBuddyPlan{Kind: input.Kind, Name: input.Name, Visibility: visibility, OwnerID: ownerID, Enabled: *input.Enabled, Content: string(input.Content), SchemaVersion: input.SchemaVersion, Version: 1, FilterPlanID: input.FilterPlanID, CreatedAt: now, UpdatedAt: now}
			return tx.Create(&plan).Error
		}
		if err := tx.Where("id = ? AND visibility = ? AND owner_id = ?", planID, visibility, ownerID).First(&plan).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBelloBuddyNotFound
			}
			return err
		}
		if input.Kind != plan.Kind || input.ExpectedVersion <= 0 || input.ExpectedVersion != plan.Version {
			return ErrBelloBuddyConflict
		}
		result := tx.Model(&plan).Where("version = ?", input.ExpectedVersion).Updates(map[string]any{
			"name": input.Name, "enabled": *input.Enabled, "content": string(input.Content), "schema_version": input.SchemaVersion,
			"version": input.ExpectedVersion + 1, "filter_plan_id": input.FilterPlanID, "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBelloBuddyConflict
		}
		return tx.First(&plan, planID).Error
	})
	if err != nil {
		return nil, err
	}
	return &BelloBuddyPlanDetail{BelloBuddyPlan: plan, Content: common.RawMessage(plan.Content)}, nil
}

func SyncBelloBuddyJobs(userID int, accountKey string, input BelloBuddySyncInput) (*BelloBuddyJobsConfig, error) {
	if err := ValidateBelloBuddyIdentifier(accountKey); err != nil {
		return nil, err
	}
	if len(input.Jobs) == 0 || len(input.Jobs) > BelloBuddyMaxJobs || utf8.RuneCountInString(input.DisplayName) > 128 {
		return nil, belloInvalid("同步职位数量或账号名称无效")
	}
	seen := make(map[string]bool)
	for _, job := range input.Jobs {
		if err := ValidateBelloBuddyIdentifier(job.JobID); err != nil {
			return nil, err
		}
		if job.Online == nil || seen[job.JobID] || utf8.RuneCountInString(job.Name) > 128 || utf8.RuneCountInString(job.City) > 128 {
			return nil, belloInvalid("职位状态、展示信息或重复 ID 无效")
		}
		seen[job.JobID] = true
	}
	now := common.GetTimestamp()
	result := &BelloBuddyJobsConfig{}
	err := model.WithBelloBuddyAccount(userID, accountKey, true, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		if err := AdvanceBelloBuddyAccount(tx, account); err != nil {
			return err
		}
		if err := tx.Model(account).Update("display_name", input.DisplayName).Error; err != nil {
			return err
		}
		account.DisplayName = input.DisplayName
		for _, inputJob := range input.Jobs {
			var job model.BelloBuddyJob
			err := tx.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(inputJob.JobID)).First(&job).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				job = model.BelloBuddyJob{AccountID: account.ID, JobHash: model.BelloBuddyIdentityHash(inputJob.JobID), JobID: inputJob.JobID, Name: inputJob.Name, City: inputJob.City, Online: *inputJob.Online, Version: 1, CreatedAt: now, UpdatedAt: now}
				if !job.Online {
					job.FilterMode = "skip_all"
				}
				if err := tx.Create(&job).Error; err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			updates := map[string]any{"name": inputJob.Name, "city": inputJob.City, "online": *inputJob.Online, "version": job.Version + 1, "updated_at": now}
			if !*inputJob.Online {
				updates["filter_mode"], updates["filter_plan_id"], updates["greeting_limit"] = "skip_all", 0, 0
			}
			if err := tx.Model(&job).Updates(updates).Error; err != nil {
				return err
			}
		}
		result.Account = *account
		return tx.Where("account_id = ?", account.ID).Order("id").Find(&result.Jobs).Error
	})
	return result, err
}

func AdvanceBelloBuddyAccount(tx *gorm.DB, account *model.BelloBuddyAccount) error {
	if err := model.AdvanceBelloBuddyAccount(tx, account); err != nil {
		if errors.Is(err, gorm.ErrInvalidTransaction) {
			return ErrBelloBuddyConflict
		}
		return err
	}
	return nil
}

func GetBelloBuddyJobs(userID int, accountKey string) (*BelloBuddyJobsConfig, error) {
	result := &BelloBuddyJobsConfig{}
	err := model.WithBelloBuddyAccount(userID, accountKey, false, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		result.Account = *account
		return tx.Where("account_id = ?", account.ID).Order("id").Find(&result.Jobs).Error
	})
	return result, err
}

func validateBelloBuddyJobSelection(tx *gorm.DB, userID int, input BelloBuddyJobConfigInput) error {
	if !slices.Contains([]string{"plan", "pass_all", "skip_all"}, input.FilterMode) || !slices.Contains([]string{"plan", "jump"}, input.CommunicationMode) || input.GreetingLimit == nil || *input.GreetingLimit < 0 || input.ExpectedVersion <= 0 {
		return belloInvalid("选择模式、版本或招呼限额无效")
	}
	for _, selection := range []struct {
		mode string
		id   int
		kind string
	}{{input.FilterMode, input.FilterPlanID, "filter"}, {input.CommunicationMode, input.CommunicationID, "communication"}} {
		if selection.mode == "plan" {
			if selection.id <= 0 {
				return belloInvalid("真实方案模式必须提供服务端 ID")
			}
			if _, err := GetBelloBuddyPlan(tx, userID, selection.id, selection.kind, true); err != nil {
				return err
			}
		} else if selection.id != 0 {
			return belloInvalid("特殊选择模式不能携带真实方案 ID")
		}
	}
	return nil
}

func SaveBelloBuddyJobConfigs(userID int, accountKey string, inputs []BelloBuddyJobConfigInput) (*BelloBuddyJobsConfig, error) {
	if len(inputs) == 0 || len(inputs) > BelloBuddyMaxJobs {
		return nil, belloInvalid("必须提供明确的职位集合")
	}
	result := &BelloBuddyJobsConfig{}
	err := model.WithBelloBuddyAccount(userID, accountKey, false, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		if err := AdvanceBelloBuddyAccount(tx, account); err != nil {
			return err
		}
		seen := make(map[string]bool)
		for _, input := range inputs {
			if err := ValidateBelloBuddyIdentifier(input.JobID); err != nil {
				return err
			}
			if seen[input.JobID] {
				return belloInvalid("职位集合不能包含重复 ID")
			}
			seen[input.JobID] = true
			if err := validateBelloBuddyJobSelection(tx, userID, input); err != nil {
				return err
			}
			var job model.BelloBuddyJob
			if err := tx.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(input.JobID)).First(&job).Error; err != nil {
				return err
			}
			if job.Version != input.ExpectedVersion {
				return ErrBelloBuddyConflict
			}
			if !job.Online && input.FilterMode != "skip_all" {
				return belloInvalid("下线职位必须跳过打招呼")
			}
			limit := *input.GreetingLimit
			if input.FilterMode == "skip_all" {
				limit = 0
			}
			updated := tx.Model(&job).Where("version = ?", input.ExpectedVersion).Updates(map[string]any{
				"filter_mode": input.FilterMode, "filter_plan_id": input.FilterPlanID, "communication_mode": input.CommunicationMode,
				"communication_id": input.CommunicationID, "greeting_limit": limit, "version": job.Version + 1, "updated_at": common.GetTimestamp(),
			})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrBelloBuddyConflict
			}
		}
		result.Account = *account
		return tx.Where("account_id = ?", account.ID).Order("id").Find(&result.Jobs).Error
	})
	return result, err
}

func GetBelloBuddyRuntimeConfig(userID int, accountKey string, input BelloBuddyRuntimeInput) (*BelloBuddyRuntimeConfig, error) {
	if len(input.JobIDs) == 0 || len(input.JobIDs) > BelloBuddyMaxJobs || input.SchemaVersion != BelloBuddySchemaVersion || strings.TrimSpace(input.RunnerVersion) == "" || len(input.RunnerVersion) > 128 {
		return nil, belloInvalid("运行器版本、Schema 或职位集合无效")
	}
	result := &BelloBuddyRuntimeConfig{SchemaVersion: BelloBuddySchemaVersion, FetchedAt: common.GetTimestamp(), Jobs: []BelloBuddyRuntimeJob{}, Plans: []BelloBuddyPlanDetail{}, PlanAliases: map[int]string{}}
	err := model.WithBelloBuddyAccount(userID, accountKey, false, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		result.Account = *account
		seenJobs := make(map[string]bool)
		seenPlans := make(map[int]bool)
		for _, jobID := range input.JobIDs {
			if err := ValidateBelloBuddyIdentifier(jobID); err != nil {
				return err
			}
			if seenJobs[jobID] {
				return belloInvalid("职位集合不能包含重复 ID")
			}
			seenJobs[jobID] = true
			var job model.BelloBuddyJob
			if err := tx.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(jobID)).First(&job).Error; err != nil {
				return err
			}
			selection := BelloBuddyJobConfigInput{FilterMode: job.FilterMode, FilterPlanID: job.FilterPlanID, CommunicationMode: job.CommunicationMode, CommunicationID: job.CommunicationID, ExpectedVersion: job.Version, GreetingLimit: &job.GreetingLimit}
			if err := validateBelloBuddyJobSelection(tx, userID, selection); err != nil {
				return ErrBelloBuddyConfig
			}
			if (!job.Online || job.FilterMode == "skip_all") && job.GreetingLimit != 0 {
				return ErrBelloBuddyConfig
			}
			ids := []int{job.FilterPlanID, job.CommunicationID}
			for i := 0; i < len(ids); i++ {
				id := ids[i]
				if id == 0 || seenPlans[id] {
					continue
				}
				plan, err := GetBelloBuddyPlan(tx, userID, id, "", true)
				if err != nil {
					return ErrBelloBuddyConfig
				}
				planInput := BelloBuddyPlanInput{Kind: plan.Kind, Name: plan.Name, Enabled: &plan.Enabled, SchemaVersion: plan.SchemaVersion, FilterPlanID: plan.FilterPlanID, Content: common.RawMessage(plan.Content)}
				if err := ValidateBelloBuddyPlan(planInput); err != nil {
					return ErrBelloBuddyConfig
				}
				seenPlans[id] = true
				result.Plans = append(result.Plans, BelloBuddyPlanDetail{BelloBuddyPlan: *plan, Content: common.RawMessage(plan.Content)})
				var legacy struct {
					ID string `json:"id"`
				}
				if err := common.UnmarshalJsonStr(plan.Content, &legacy); err != nil {
					return ErrBelloBuddyConfig
				}
				if legacy.ID != "" {
					result.PlanAliases[id] = legacy.ID
				}
				if plan.FilterPlanID != 0 {
					if _, err := GetBelloBuddyPlan(tx, userID, plan.FilterPlanID, "filter", true); err != nil {
						return ErrBelloBuddyConfig
					}
					ids = append(ids, plan.FilterPlanID)
				}
			}
			status, bindingVersion, err := GetBelloBuddyBotStatus(tx, job)
			if err != nil {
				return err
			}
			// 未绑定或渠道不可用不阻止本地筛选；AI 入口仍需实时校验权限和额度。
			result.Jobs = append(result.Jobs, BelloBuddyRuntimeJob{BelloBuddyJob: job, BotStatus: status, BotBindingVersion: bindingVersion})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func BelloBuddyErrorResponse(err error) *BelloBuddyError {
	var businessError *BelloBuddyError
	if errors.As(err, &businessError) {
		return businessError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrBelloBuddyNotFound
	}
	common.SysError(fmt.Sprintf("BelloBuddy 配置操作失败：%T", err))
	return &BelloBuddyError{http.StatusInternalServerError, "INTERNAL_ERROR", "配置服务暂时不可用"}
}

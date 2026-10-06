package controller

import (
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func belloReply(c *gin.Context, data any, err error) {
	if err != nil {
		failure := service.BelloBuddyErrorResponse(err)
		c.JSON(failure.Status, gin.H{"success": false, "code": failure.Code, "message": failure.Message, "source": "dpin"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// 可用性表示当前用户组存在启用的协议资源，不代表上游健康或当前职位已绑定。
// 每次 AI 调用仍要执行会话、方案、岗位、内部凭据及额度校验。
func GetBelloBuddyCapabilities(c *gin.Context) {
	var resources []struct {
		Model string
		Type  int
	}
	err := model.DB.Table("abilities").Select("DISTINCT abilities.model, channels.type").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where(map[string]any{"abilities.group": c.GetString("group"), "abilities.enabled": true, "channels.status": common.ChannelStatusEnabled}).
		Where("channels.type IN ?", []int{constant.ChannelTypeTypeSafe, constant.ChannelTypeMaxKB}).Order("abilities.model").Scan(&resources).Error
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	screenModels, replyModels := []string{}, []string{}
	for _, resource := range resources {
		switch resource.Type {
		case constant.ChannelTypeTypeSafe:
			if resource.Model == service.BelloBuddyScreenModel {
				screenModels = append(screenModels, resource.Model)
			}
		case constant.ChannelTypeMaxKB:
			replyModels = append(replyModels, resource.Model)
		}
	}
	screenCode, replyCode := "", ""
	if len(screenModels) == 0 {
		screenCode = "AI_RESOURCE_UNAVAILABLE"
	}
	if len(replyModels) == 0 {
		replyCode = "AI_RESOURCE_UNAVAILABLE"
	}
	belloReply(c, gin.H{
		"schema_version": service.BelloBuddySchemaVersion,
		"plan_kinds":     []string{"filter", "communication"},
		"selection_modes": gin.H{
			"filter":        []string{"plan", "pass_all", "skip_all"},
			"communication": []string{"plan", "jump"},
		},
		"limits": gin.H{
			"max_body_bytes": service.BelloBuddyMaxBodyBytes,
			"max_plan_bytes": service.BelloBuddyMaxPlanBytes,
			"max_jobs":       service.BelloBuddyMaxJobs,
		},
		"features": gin.H{"plan_management": true, "job_configuration": true, "runtime_config": true},
		"ai_screen": gin.H{
			"available": len(screenModels) > 0, "protocol": "typesafe_systemone", "models": screenModels, "code": screenCode,
		},
		"ai_reply": gin.H{
			"available": len(replyModels) > 0, "protocol": "maxkb_application", "models": replyModels, "code": replyCode,
		},
	}, nil)
}

func belloReadBody(c *gin.Context, value any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, service.BelloBuddyMaxBodyBytes))
	if err == nil {
		err = common.Unmarshal(body, value)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "INVALID_REQUEST", "message": "请求 JSON 无效或超过 2 MB", "source": "dpin"})
		return false
	}
	return true
}

func belloPlanID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		belloReply(c, nil, service.ErrBelloBuddyNotFound)
		return 0, false
	}
	return id, true
}

func ListBelloBuddyPlans(c *gin.Context) {
	kind := c.Query("kind")
	if kind != "" && kind != "filter" && kind != "communication" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "INVALID_REQUEST", "message": "未知的方案类型"})
		return
	}
	page, pageSize, ok := belloPage(c)
	if !ok {
		return
	}
	query := model.BelloBuddyVisiblePlans(model.DB, c.GetInt("id"))
	if c.GetBool("bello_public_admin") {
		query = model.DB.Model(&model.BelloBuddyPlan{}).Where("visibility = ?", "public")
	}
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if name := c.Query("name"); name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		belloReply(c, nil, err)
		return
	}
	plans := []model.BelloBuddyPlan{}
	err := query.Omit("content").Order("id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&plans).Error
	belloReply(c, gin.H{"items": plans, "total": count, "page": page, "page_size": pageSize}, err)
}

func belloPage(c *gin.Context) (int, int, bool) {
	page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, sizeErr := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if pageErr != nil || sizeErr != nil || page <= 0 || page > 100000 || pageSize <= 0 || pageSize > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "INVALID_REQUEST", "message": "分页参数无效", "source": "dpin"})
		return 0, 0, false
	}
	return page, pageSize, true
}

func ListBelloBuddyJobBindings(c *gin.Context) {
	page, size, ok := belloPage(c)
	if !ok {
		return
	}
	query := model.DB.Model(&model.BelloBuddyJobBinding{})
	if name := c.Query("name"); name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		belloReply(c, nil, err)
		return
	}
	items := []model.BelloBuddyJobBinding{}
	err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error
	belloReply(c, gin.H{"items": items, "total": count, "page": page, "page_size": size}, err)
}

func ListBelloBuddyJobCandidates(c *gin.Context) {
	page, size, ok := belloPage(c)
	if !ok {
		return
	}
	query := model.DB.Table("bello_buddy_jobs AS jobs").Joins("JOIN bello_buddy_accounts AS accounts ON accounts.id = jobs.account_id")
	if name := c.Query("name"); name != "" {
		query = query.Where("jobs.name LIKE ?", "%"+name+"%")
	}
	if city := c.Query("city"); city != "" {
		query = query.Where("jobs.city = ?", city)
	}
	if c.Query("unbound") == "true" {
		query = query.Where("jobs.binding_id = ?", 0)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		belloReply(c, nil, err)
		return
	}
	items := []service.BelloBuddyJobCandidate{}
	err := query.Select("jobs.id AS job_config_id, jobs.account_id, accounts.user_id, accounts.display_name AS account_name, jobs.job_id, jobs.name, jobs.city, jobs.online, jobs.binding_id, jobs.version").Order("jobs.id").Offset((page - 1) * size).Limit(size).Scan(&items).Error
	belloReply(c, gin.H{"items": items, "total": count, "page": page, "page_size": size}, err)
}

func SaveBelloBuddyJobBinding(c *gin.Context) {
	id := 0
	if c.Param("id") != "" {
		var ok bool
		id, ok = belloPlanID(c)
		if !ok {
			return
		}
	}
	var input service.BelloBuddyBindingInput
	if !belloReadBody(c, &input) {
		return
	}
	binding, err := service.SaveBelloBuddyJobBinding(id, input)
	belloReply(c, binding, err)
}

func MapBelloBuddyJobsToBinding(c *gin.Context) {
	id, ok := belloPlanID(c)
	if !ok {
		return
	}
	var input service.BelloBuddyBindingJobsInput
	if !belloReadBody(c, &input) {
		return
	}
	err := service.MapBelloBuddyJobsToBinding(id, input)
	belloReply(c, gin.H{"binding_id": id}, err)
}

func GetBelloBuddyPlan(c *gin.Context) {
	id, ok := belloPlanID(c)
	if !ok {
		return
	}
	plan, err := service.GetBelloBuddyPlan(model.DB, c.GetInt("id"), id, "", false)
	if c.GetBool("bello_public_admin") {
		var public model.BelloBuddyPlan
		err = model.DB.Where("id = ? AND visibility = ?", id, "public").First(&public).Error
		plan = &public
	}
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	belloReply(c, service.BelloBuddyPlanDetail{BelloBuddyPlan: *plan, Content: common.RawMessage(plan.Content)}, nil)
}

func SaveBelloBuddyPlan(c *gin.Context) {
	id := 0
	if c.Param("id") != "" {
		var ok bool
		id, ok = belloPlanID(c)
		if !ok {
			return
		}
	}
	var input service.BelloBuddyPlanInput
	if !belloReadBody(c, &input) {
		return
	}
	plan, err := service.SaveBelloBuddyPlan(c.GetInt("id"), id, c.GetBool("bello_public_admin"), input)
	belloReply(c, plan, err)
}

func ImportBelloBuddyPlans(c *gin.Context) {
	var input service.BelloBuddyPlanImportInput
	if !belloReadBody(c, &input) {
		return
	}
	result, err := service.ImportBelloBuddyPlans(input)
	belloReply(c, result, err)
}

func SyncBelloBuddyJobs(c *gin.Context) {
	var input service.BelloBuddySyncInput
	if !belloReadBody(c, &input) {
		return
	}
	result, err := service.SyncBelloBuddyJobs(c.GetInt("id"), c.Param("accountKey"), input)
	belloReply(c, result, err)
}

func GetBelloBuddyJobs(c *gin.Context) {
	result, err := service.GetBelloBuddyJobs(c.GetInt("id"), c.Param("accountKey"))
	belloReply(c, result, err)
}

func SaveBelloBuddyJobConfig(c *gin.Context) {
	var input service.BelloBuddyJobConfigInput
	if !belloReadBody(c, &input) {
		return
	}
	if input.JobID != "" && input.JobID != c.Param("jobId") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "INVALID_REQUEST", "message": "职位 ID 与路径不一致"})
		return
	}
	input.JobID = c.Param("jobId")
	result, err := service.SaveBelloBuddyJobConfigs(c.GetInt("id"), c.Param("accountKey"), []service.BelloBuddyJobConfigInput{input})
	belloReply(c, result, err)
}

func SaveBelloBuddyJobConfigBatch(c *gin.Context) {
	var input struct {
		Jobs []service.BelloBuddyJobConfigInput `json:"jobs"`
	}
	if !belloReadBody(c, &input) {
		return
	}
	result, err := service.SaveBelloBuddyJobConfigs(c.GetInt("id"), c.Param("accountKey"), input.Jobs)
	belloReply(c, result, err)
}

func GetBelloBuddyRuntimeConfig(c *gin.Context) {
	var input service.BelloBuddyRuntimeInput
	if !belloReadBody(c, &input) {
		return
	}
	result, err := service.GetBelloBuddyRuntimeConfig(c.GetInt("id"), c.Param("accountKey"), input)
	belloReply(c, result, err)
}

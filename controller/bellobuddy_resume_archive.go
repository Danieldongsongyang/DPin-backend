package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ReceiveBelloBuddyResume(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.BelloBuddyResumeMaxBytes+(64<<10))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		status := 400
		var limitError *http.MaxBytesError
		if errors.As(err, &limitError) {
			status = 413
		}
		c.JSON(status, gin.H{"success": false, "code": "INVALID_RESUME_MULTIPART", "message": "仅支持一个 PDF 及 metadata，文件最大 30 MB"})
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	form := c.Request.MultipartForm
	if len(form.File) != 1 || len(form.File["file"]) != 1 || len(form.Value) != 1 || len(form.Value["metadata"]) != 1 || len(form.Value["metadata"][0]) > 16<<10 {
		c.JSON(400, gin.H{"success": false, "code": "INVALID_REQUEST", "message": "仅接受 file 和 metadata，不接受客户端指定归属或服务器目标"})
		return
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal([]byte(form.Value["metadata"][0]), &fields) != nil || fields == nil {
		belloReply(c, nil, &service.BelloBuddyError{400, "INVALID_REQUEST", "metadata 必须是 JSON 对象"})
		return
	}
	for key := range fields {
		switch key {
		case "client_upload_id", "candidate_id", "candidate_name", "downloaded_at":
		default:
			belloReply(c, nil, &service.BelloBuddyError{400, "INVALID_REQUEST", "metadata 包含不支持的字段"})
			return
		}
	}
	var input service.BelloBuddyResumeInput
	if common.Unmarshal([]byte(form.Value["metadata"][0]), &input) != nil {
		belloReply(c, nil, &service.BelloBuddyError{400, "INVALID_REQUEST", "metadata 格式无效"})
		return
	}
	file, err := form.File["file"][0].Open()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	defer file.Close()
	task, err := engine.Accept(c.Request.Context(), c.GetInt("id"), c.Param("accountKey"), c.Param("jobId"), form.File["file"][0].Filename, input, file)
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": resumeArchiveResponse(task), "client_upload_id": input.ClientUploadID})
}

func resumeArchiveResponse(task *model.BelloBuddyResumeArchive) gin.H {
	message := "D-PIN 已可靠接收，等待后台归档"
	switch task.State {
	case "processing":
		message = "正在后台归档；尚未确认全部完成"
	case "blocked":
		message = "归档受阻，请根据错误修复配置或核对结果；文件保留"
	case "archived":
		message = "飞书归档及附件内容已核对完成"
	case "superseded":
		message = "此版本此前已归档，已被同范围新版替换；重复上传不再次追加"
	case "abandoned":
		message = "管理员已明确放弃此任务；不代表飞书没有外部写入"
	}
	return gin.H{"archive_id": task.ArchiveID, "state": task.State, "stage": task.Stage, "code": task.ErrorCode, "message": message, "sha256": task.SHA256, "size": task.Size, "downloaded_at": task.DownloadedAt, "received_at": task.ReceivedAt, "verified_at": task.VerifiedAt, "completed_at": task.CompletedAt, "purged_at": task.PurgedAt, "updated_at": task.UpdatedAt, "missing_scopes": resumeMissingScopes(task)}
}

func resumeMissingScopes(task *model.BelloBuddyResumeArchive) []string {
	result := []string{}
	if task.MissingScopes != "" {
		_ = common.Unmarshal([]byte(task.MissingScopes), &result)
	}
	return result
}

func GetBelloBuddyResumeArchive(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	task, err := engine.Get(c.GetInt("id"), c.Param("archiveId"))
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	belloReply(c, resumeArchiveResponse(task), nil)
}

func GetBelloBuddyResumeUpload(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	task, err := engine.GetByUpload(c.GetInt("id"), c.Param("accountKey"), c.Param("jobId"), c.Param("uploadId"))
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	belloReply(c, resumeArchiveResponse(task), nil)
}

func ListBelloBuddyResumeArchives(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	ids := strings.Split(c.Query("ids"), ",")
	if len(ids) > 50 || len(ids) == 0 || ids[0] == "" {
		belloReply(c, nil, &service.BelloBuddyError{400, "INVALID_REQUEST", "ids 必须包含 1 至 50 个归档编号"})
		return
	}
	items := []gin.H{}
	for _, id := range ids {
		task, err := engine.Get(c.GetInt("id"), id)
		if err != nil {
			belloReply(c, nil, err)
			return
		}
		items = append(items, resumeArchiveResponse(task))
	}
	belloReply(c, gin.H{"items": items}, nil)
}

func ReconcileBelloBuddyResumeArchive(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	task, err := engine.Reconcile(c.GetInt("id"), c.Param("archiveId"))
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	c.JSON(202, gin.H{"success": true, "data": resumeArchiveResponse(task)})
}

func AdminListBelloBuddyResumeArchives(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	page, size, ok := belloPage(c)
	if !ok {
		return
	}
	query := engine.DB.Model(&model.BelloBuddyResumeArchive{})
	if state := c.Query("state"); state != "" {
		query = query.Where("state = ?", state)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		belloReply(c, nil, err)
		return
	}
	var tasks []model.BelloBuddyResumeArchive
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&tasks).Error; err != nil {
		belloReply(c, nil, err)
		return
	}
	items := []gin.H{}
	for _, task := range tasks {
		item := resumeArchiveResponse(&task)
		item["waiting_seconds"] = max(int64(0), time.Now().Unix()-task.ReceivedAt)
		items = append(items, item)
	}
	stats, err := engine.StorageStatus()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	belloReply(c, gin.H{"items": items, "total": count, "storage": stats}, nil)
}

func AbandonBelloBuddyResumeArchive(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	var input struct {
		Confirm bool `json:"confirm_abandon"`
	}
	if !belloReadBody(c, &input) {
		return
	}
	if !input.Confirm {
		belloReply(c, nil, &service.BelloBuddyError{400, "ABANDON_CONFIRMATION_REQUIRED", "需明确确认放弃本任务及清理私有副本"})
		return
	}
	err = engine.Abandon(c.GetInt("id"), c.Param("archiveId"))
	belloReply(c, gin.H{"abandoned": err == nil}, err)
}

func SyncBelloBuddyJobCatalog(c *gin.Context) {
	var input service.BelloBuddyCatalogInput
	if !belloReadBody(c, &input) {
		return
	}
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	result, err := engine.AcceptJobCatalog(c.GetInt("id"), c.Param("accountKey"), input)
	belloReply(c, result, err)
}
func GetBelloBuddyJobCatalog(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	result, err := engine.CatalogReceipt(c.GetInt("id"), c.Param("accountKey"), c.Param("syncId"))
	belloReply(c, result, err)
}
func ReconcileBelloBuddyJobCatalog(c *gin.Context) {
	engine, err := service.CurrentBelloBuddyResumeEngine()
	if err != nil {
		belloReply(c, nil, err)
		return
	}
	result, err := engine.ReconcileJobCatalog(c.GetInt("id"), c.Param("accountKey"), c.Param("syncId"))
	belloReply(c, result, err)
}

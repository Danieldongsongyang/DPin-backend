package controller

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func belloReleaseReply(c *gin.Context, data any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	status, code, message := http.StatusBadRequest, "RELEASE_INVALID_REQUEST", err.Error()
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = 404, "RELEASE_NOT_FOUND", "版本或文件不存在"
	case errors.Is(err, model.ErrBelloBuddyReleaseConflict), errors.Is(err, model.ErrBelloBuddyReleaseIdempotency), errors.Is(err, model.ErrBelloBuddyReleaseState):
		status, code = 409, "RELEASE_CONFLICT"
	default:
		var pathErr *os.PathError
		var linkErr *os.LinkError
		var limitErr *http.MaxBytesError
		if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
			status, code, message = 500, "RELEASE_STORAGE_ERROR", "发布文件存储操作失败，请检查服务器配置与权限"
			break
		}
		if errors.As(err, &limitErr) {
			status, code, message = 413, "RELEASE_UPLOAD_TOO_LARGE", "上传请求超过允许大小"
			break
		}
		var failure *service.BelloBuddyError
		if errors.As(err, &failure) {
			status, code, message = failure.Status, failure.Code, failure.Message
		} else if !strings.HasPrefix(message, "发布") && (strings.Contains(message, "sql") || strings.Contains(message, "SQL") || strings.Contains(message, "constraint") || strings.Contains(message, "Error 1062")) {
			status, code, message = 409, "RELEASE_DATABASE_ERROR", "保存失败，请检查版本是否重复或刷新后重试"
		}
	}
	c.AbortWithStatusJSON(status, gin.H{"success": false, "code": code, "message": message, "source": "dpin"})
}

func belloReleaseJSON(c *gin.Context, input any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10))
	if err == nil {
		err = common.Unmarshal(data, input)
	}
	if err != nil {
		belloReleaseReply(c, nil, errors.New("请求 JSON 无效或超过 256 KiB"))
		return false
	}
	return true
}

// Read the channel and its displayed version in one snapshot, including when
// the target has moved off the current page of the release list.
func belloReleaseChannelView() (any, error) {
	var result struct {
		ReleaseID int    `json:"release_id"`
		Revision  int64  `json:"revision"`
		Version   string `json:"version"`
	}
	err := model.DB.Model(&model.BelloBuddyReleaseChannel{}).
		Select("bello_buddy_release_channels.release_id, bello_buddy_release_channels.revision, bello_buddy_releases.version").
		Joins("LEFT JOIN bello_buddy_releases ON bello_buddy_releases.id = bello_buddy_release_channels.release_id").
		Where("bello_buddy_release_channels.product = ? AND bello_buddy_release_channels.channel = ? AND bello_buddy_release_channels.platform = ? AND bello_buddy_release_channels.arch = ?", model.BelloBuddyReleaseProduct, model.BelloBuddyReleaseStable, model.BelloBuddyReleasePlatform, model.BelloBuddyReleaseArch).
		Scan(&result).Error
	return result, err
}

func ListBelloBuddyReleases(c *gin.Context) {
	page, size, ok := belloPage(c)
	if !ok {
		return
	}
	query := model.DB.Model(&model.BelloBuddyRelease{}).Where("product = ?", model.BelloBuddyReleaseProduct)
	var total int64
	err := query.Count(&total).Error
	var releases []model.BelloBuddyRelease
	if err == nil {
		err = query.Order("id DESC").Limit(size).Offset((page - 1) * size).Find(&releases).Error
	}
	channel, channelErr := belloReleaseChannelView()
	if err == nil {
		err = channelErr
	}
	items := make([]map[string]any, 0, len(releases))
	for i := range releases {
		items = append(items, service.BelloBuddyReleaseView(&releases[i]))
	}
	belloReleaseReply(c, gin.H{"items": items, "total": total, "page": page, "page_size": size, "channel": channel}, err)
}

func GetBelloBuddyRelease(c *gin.Context) {
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	release, err := model.GetBelloBuddyRelease(model.DB, id)
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	artifacts := []model.BelloBuddyReleaseArtifact{}
	uploads := []model.BelloBuddyReleaseUpload{}
	operations := []model.BelloBuddyReleaseOperation{}
	queries := []struct {
		target any
		order  string
	}{{&artifacts, "id"}, {&uploads, "id DESC"}, {&operations, "id DESC"}}
	for _, query := range queries {
		if err := model.DB.Where("release_id = ?", id).Order(query.order).Limit(100).Find(query.target).Error; err != nil {
			belloReleaseReply(c, nil, err)
			return
		}
	}
	channel, err := belloReleaseChannelView()
	var history []map[string]any
	_ = common.UnmarshalJsonStr(release.AcceptanceHistory, &history)
	for _, snapshot := range history {
		if report, ok := snapshot["report"].(map[string]any); ok {
			delete(report, "evidence")
		}
	}
	belloReleaseReply(c, gin.H{"release": service.BelloBuddyReleaseView(release), "artifacts": artifacts, "uploads": uploads, "operations": operations, "channel": channel, "acceptance_history": history}, err)
}

func SaveBelloBuddyRelease(c *gin.Context) {
	var input service.BelloBuddyReleaseDraftInput
	if !belloReleaseJSON(c, &input) {
		return
	}
	id := 0
	if c.Param("id") != "" {
		var err error
		id, err = service.BelloBuddyReleaseID(c.Param("id"))
		if err != nil {
			belloReleaseReply(c, nil, err)
			return
		}
	}
	release, err := service.BelloBuddyReleaseDraft(model.DB, c.GetInt("id"), id, input)
	if err == nil {
		recordManageAudit(c, "bellobuddy.release.draft", map[string]any{"release_id": release.ID, "version": release.Version, "revision": release.Revision})
		belloReleaseReply(c, service.BelloBuddyReleaseView(release), nil)
		return
	}
	belloReleaseReply(c, nil, err)
}

// Only a single file part is accepted. The wrapper consumes and validates the
// multipart trailer before the service commits the artifact index.
type belloReleaseUploadReader struct {
	io.Reader
	finish func() error
	done   bool
}

func (r *belloReleaseUploadReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF && !r.done {
		r.done = true
		if trailerErr := r.finish(); trailerErr != nil {
			return n, trailerErr
		}
	}
	return n, err
}

func UploadBelloBuddyReleaseArtifact(c *gin.Context) {
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	revision, err := strconv.ParseInt(c.Query("revision"), 10, 64)
	if err != nil || revision <= 0 {
		belloReleaseReply(c, nil, errors.New("上传需提供当前 revision 查询参数"))
		return
	}
	limit := service.BelloBuddyReleaseUploadLimit(c.Param("kind"))
	if limit == 0 {
		belloReleaseReply(c, nil, errors.New("未知发布文件种类"))
		return
	}
	bodyLimit := limit + (64 << 10)
	if c.Request.ContentLength > bodyLimit {
		belloReleaseReply(c, nil, &service.BelloBuddyError{Status: 413, Code: "RELEASE_UPLOAD_TOO_LARGE", Message: "上传请求超过当前文件种类的大小限制"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, bodyLimit)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		belloReleaseReply(c, nil, errors.New("上传请求必须为 multipart/form-data"))
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		belloReleaseReply(c, nil, errors.New("上传请求只能包含一个 file 文件"))
		return
	}
	// Part.FileName strips paths. Validate the original parsed filename rather
	// than accepting a path that happens to end in the expected basename.
	disposition, parameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	filename := parameters["filename"]
	if err != nil || disposition != "form-data" || filename == "" || filename != part.FileName() || strings.ContainsAny(filename, "/\\\x00") {
		belloReleaseReply(c, nil, errors.New("上传文件名必须为原始制品名称，不能包含路径"))
		return
	}
	stream := &belloReleaseUploadReader{Reader: part, finish: func() error {
		if _, err := reader.NextPart(); err != io.EOF {
			return errors.New("上传请求含多余字段、文件或不完整结束标记")
		}
		// Multipart EOF alone does not consume the HTTP epilogue. Require the
		// underlying body to finish within its limit before committing metadata.
		if _, err := io.Copy(io.Discard, c.Request.Body); err != nil {
			return errors.New("上传请求尾部不完整或超过大小限制")
		}
		return nil
	}}
	release, err := service.StoreBelloBuddyReleaseArtifact(model.DB, c.GetInt("id"), id, revision, c.Param("kind"), filename, stream)
	if err == nil {
		recordManageAudit(c, "bellobuddy.release.upload", map[string]any{"release_id": id, "kind": c.Param("kind"), "revision": release.Revision})
		belloReleaseReply(c, service.BelloBuddyReleaseView(release), nil)
		return
	}
	belloReleaseReply(c, nil, err)
}

func ValidateBelloBuddyRelease(c *gin.Context) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !belloReleaseJSON(c, &input) {
		return
	}
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	release, err := service.ValidateBelloBuddyRelease(model.DB, id, input.Revision)
	if err == nil {
		recordManageAudit(c, "bellobuddy.release.validate", map[string]any{"release_id": id, "state": release.State, "revision": release.Revision})
		belloReleaseReply(c, service.BelloBuddyReleaseView(release), nil)
		return
	}
	belloReleaseReply(c, nil, err)
}

func RecoverBelloBuddyReleaseValidation(c *gin.Context) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !belloReleaseJSON(c, &input) {
		return
	}
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	release, err := service.RecoverBelloBuddyReleaseValidation(model.DB, id, input.Revision)
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	recordManageAudit(c, "bellobuddy.release.recover_validation", map[string]any{"release_id": id, "revision": release.Revision})
	belloReleaseReply(c, service.BelloBuddyReleaseView(release), nil)
}

func AcceptBelloBuddyRelease(c *gin.Context) {
	var input struct {
		Revision int64                             `json:"revision"`
		Evidence service.BelloBuddyReleaseEvidence `json:"evidence"`
	}
	if !belloReleaseJSON(c, &input) {
		return
	}
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	release, err := service.AcceptBelloBuddyRelease(model.DB, id, input.Revision, input.Evidence)
	if err == nil {
		recordManageAudit(c, "bellobuddy.release.acceptance", map[string]any{"release_id": id, "revision": release.Revision})
		belloReleaseReply(c, service.BelloBuddyReleaseView(release), nil)
		return
	}
	belloReleaseReply(c, nil, err)
}

func OperateBelloBuddyRelease(c *gin.Context) {
	var input struct {
		Revision                int64  `json:"revision"`
		ExpectedChannelRevision int64  `json:"expected_channel_revision"`
		IdempotencyKey          string `json:"idempotency_key"`
		Reason                  string `json:"reason"`
	}
	if !belloReleaseJSON(c, &input) {
		return
	}
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	operation, err := service.ApplyBelloBuddyReleaseOperation(model.DB, model.BelloBuddyReleaseOperationInput{ActorID: c.GetInt("id"), ReleaseID: id, ExpectedReleaseRevision: input.Revision, ExpectedChannelRevision: input.ExpectedChannelRevision, Action: c.GetString("bello_release_action"), Reason: input.Reason, IdempotencyKey: input.IdempotencyKey})
	if err == nil {
		recordManageAudit(c, "bellobuddy.release."+operation.Action, map[string]any{"operation_id": operation.ID, "release_id": operation.ReleaseID, "from_release_id": operation.FromReleaseID, "to_release_id": operation.ToReleaseID, "channel_revision": operation.ChannelRevision})
	}
	belloReleaseReply(c, operation, err)
}

func belloReleaseInternal(c *gin.Context) bool {
	return c.GetInt("role") >= common.RoleAdminUser && authz.Can(c.GetInt("id"), c.GetInt("role"), authz.BelloBuddyReleaseRead)
}

func GetBelloBuddyUpdateStatus(c *gin.Context) {
	// One SELECT observes channel revision and target policy from one statement
	// snapshot on SQLite, MySQL and PostgreSQL, without blocking a publisher.
	var snapshot struct {
		ChannelRevision      int64
		TargetID             int
		ID                   int
		Product              string
		Version              string
		Platform             string
		Arch                 string
		MinimumSystemVersion string
		State                string
		Disabled             bool
		PublishedAt          int64
	}
	channels := model.DB.NamingStrategy.TableName("BelloBuddyReleaseChannel")
	releases := model.DB.NamingStrategy.TableName("BelloBuddyRelease")
	err := model.DB.Table(channels+" AS release_channel").
		Select("release_channel.revision AS channel_revision, release_channel.release_id AS target_id, release_record.id, release_record.product, release_record.version, release_record.platform, release_record.arch, release_record.minimum_system_version, release_record.state, release_record.disabled, release_record.published_at").
		Joins("LEFT JOIN "+releases+" AS release_record ON release_record.id = release_channel.release_id").
		Where("release_channel.product = ? AND release_channel.channel = ? AND release_channel.platform = ? AND release_channel.arch = ?", model.BelloBuddyReleaseProduct, model.BelloBuddyReleaseStable, model.BelloBuddyReleasePlatform, model.BelloBuddyReleaseArch).
		Scan(&snapshot).Error
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	var target any
	if snapshot.TargetID != 0 {
		if snapshot.ID != snapshot.TargetID {
			belloReleaseReply(c, nil, gorm.ErrRecordNotFound)
			return
		}
		release := &model.BelloBuddyRelease{ID: snapshot.ID, Product: snapshot.Product, Version: snapshot.Version, Platform: snapshot.Platform, Arch: snapshot.Arch, MinimumSystemVersion: snapshot.MinimumSystemVersion, State: snapshot.State, Disabled: snapshot.Disabled, PublishedAt: snapshot.PublishedAt}
		if err := service.CheckBelloBuddyReleaseAccess(release, false); err != nil {
			belloReleaseReply(c, nil, err)
			return
		}
		target = gin.H{"releaseId": release.ID, "version": release.Version, "platform": release.Platform, "arch": release.Arch, "minimumSystemVersion": release.MinimumSystemVersion, "feedUrl": fmt.Sprintf("/desktop-updates/bellobuddy/releases/%d/win32/x64/", release.ID)}
	}
	c.JSON(200, gin.H{"schemaVersion": 1, "channelRevision": snapshot.ChannelRevision, "target": target})
}

func GetBelloBuddyFixedUpdateStatus(c *gin.Context) {
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	release, err := model.GetBelloBuddyRelease(model.DB, id)
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	err = service.CheckBelloBuddyReleaseAccess(release, belloReleaseInternal(c))
	allowed := err == nil
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if !allowed {
		var failure *service.BelloBuddyError
		if !errors.As(err, &failure) || failure.Code != "RELEASE_DISABLED" {
			belloReleaseReply(c, nil, err)
			return
		}
	}
	c.JSON(200, gin.H{"schemaVersion": 1, "releaseId": id, "version": release.Version, "policyRevision": release.Revision, "downloadAllowed": allowed, "installAllowed": allowed, "reason": reason})
}

func GetBelloBuddyUpdateFeed(c *gin.Context) {
	id, err := service.BelloBuddyReleaseID(c.Param("id"))
	if c.Param("id") == "" {
		channel, channelErr := model.GetBelloBuddyReleaseChannel(model.DB)
		if channelErr != nil {
			belloReleaseReply(c, nil, channelErr)
			return
		}
		id, err = channel.ReleaseID, nil
		if id == 0 {
			err = gorm.ErrRecordNotFound
		}
	}
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	release, err := model.GetBelloBuddyRelease(model.DB, id)
	if err == nil {
		err = service.CheckBelloBuddyReleaseAccess(release, belloReleaseInternal(c))
	}
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	var artifact model.BelloBuddyReleaseArtifact
	if err := model.DB.Where("release_id = ? AND kind = ?", id, "installer").First(&artifact).Error; err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	data, err := service.BelloBuddyReleaseFeed(release, &artifact)
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-cache, no-transform")
	c.Data(200, "application/yaml; charset=utf-8", data)
}

var belloReleaseDownloadSlots = struct {
	sync.Mutex
	total int
	users map[int]int
}{users: make(map[int]int)}

func DownloadBelloBuddyReleaseArtifact(c *gin.Context) {
	id := c.GetInt("id")
	belloReleaseDownloadSlots.Lock()
	if belloReleaseDownloadSlots.total >= 32 || belloReleaseDownloadSlots.users[id] >= 4 {
		belloReleaseDownloadSlots.Unlock()
		c.Header("Retry-After", "3")
		belloReleaseReply(c, nil, &service.BelloBuddyError{Status: 429, Code: "RELEASE_DOWNLOAD_BUSY", Message: "下载并发已满，请稍后重试"})
		return
	}
	belloReleaseDownloadSlots.total++
	belloReleaseDownloadSlots.users[id]++
	belloReleaseDownloadSlots.Unlock()
	defer func() {
		belloReleaseDownloadSlots.Lock()
		belloReleaseDownloadSlots.total--
		belloReleaseDownloadSlots.users[id]--
		if belloReleaseDownloadSlots.users[id] == 0 {
			delete(belloReleaseDownloadSlots.users, id)
		}
		belloReleaseDownloadSlots.Unlock()
	}()
	release, artifact, err := service.BelloBuddyReleaseArtifactByName(model.DB, c.Param("filename"))
	if err == nil {
		err = service.CheckBelloBuddyReleaseAccess(release, belloReleaseInternal(c))
	}
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	storage, err := service.OpenBelloBuddyReleaseStorage()
	if err != nil {
		belloReleaseReply(c, nil, err)
		return
	}
	defer storage.Close()
	stored, err := storage.Lstat(artifact.StorageKey)
	if err != nil || !stored.Mode().IsRegular() || stored.Size() != artifact.Size {
		belloReleaseReply(c, nil, errors.New("发布文件不可用，请联系管理员"))
		return
	}
	file, err := storage.Open(artifact.StorageKey)
	if err != nil {
		belloReleaseReply(c, nil, errors.New("发布文件不可用，请联系管理员"))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size || !os.SameFile(stored, info) {
		belloReleaseReply(c, nil, errors.New("发布文件大小或类型异常，已停止下载"))
		return
	}
	if strings.Contains(c.GetHeader("Range"), ",") {
		c.Header("Content-Range", fmt.Sprintf("bytes */%d", artifact.Size))
		c.AbortWithStatus(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	c.Header("ETag", `"sha256-`+artifact.SHA256+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-cache, no-transform")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", `attachment; filename="`+artifact.Filename+`"`)
	http.ServeContent(c.Writer, c.Request, artifact.Filename, time.Unix(artifact.CreatedAt, 0), file)
}

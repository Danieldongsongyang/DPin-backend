package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
)

// Assessments use private durable files; existing resume scope/record IDs remain authoritative.
type BelloBuddyTypingConfig struct {
	DataDir        string            `json:"data_dir"`
	PublicURL      string            `json:"public_url"`
	EnabledModels  []string          `json:"enabled_models"`
	AllowedOrigins []string          `json:"allowed_origins"`
	Fields         map[string]string `json:"fields"`
	Article        string            `json:"article"`
	ArticleName    string            `json:"article_name"`
}

type BelloBuddyTypingAttempt struct {
	ID         string  `json:"id"`
	CreatedAt  int64   `json:"created_at"`
	StartedAt  int64   `json:"started_at"`
	LastBeat   int64   `json:"last_beat"`
	Beats      int     `json:"beats"`
	MaxLength  int     `json:"max_length"`
	FinishedAt int64   `json:"finished_at"`
	Speed      int     `json:"speed"`
	Accuracy   float64 `json:"accuracy"`
	Typed      int     `json:"typed"`
	Errors     int     `json:"errors"`
}

type BelloBuddyTypingState struct {
	ScopeKey            string                    `json:"scope_key"`
	Token               string                    `json:"token"`
	UserID              int                       `json:"user_id"`
	AccountID           int                       `json:"account_id"`
	JobID               int                       `json:"job_id"`
	CandidateID         string                    `json:"candidate_id"`
	Name                string                    `json:"name"`
	Education           string                    `json:"education,omitempty"`
	CreatedAt           int64                     `json:"created_at"`
	ExpiresAt           int64                     `json:"expires_at"`
	InvitationDelivered bool                      `json:"invitation_delivered"`
	InvitationMode      string                    `json:"invitation_mode,omitempty"`
	RecordID            string                    `json:"record_id,omitempty"`
	SyncedAttempt       string                    `json:"synced_attempt,omitempty"`
	RetryAt             int64                     `json:"retry_at,omitempty"`
	Attempts            []BelloBuddyTypingAttempt `json:"attempts"`
}

type BelloBuddyTypingEngine struct {
	Resume *BelloBuddyResumeEngine
	Config BelloBuddyTypingConfig
	mu     sync.Mutex
	states map[string]*BelloBuddyTypingState
	now    func() time.Time
}

var typingEngine *BelloBuddyTypingEngine
var typingEngineMu sync.RWMutex

func newTypingToken() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func typingScopeKey(target FeishuArchiveTarget, userID, accountID, jobID int, candidateID string) string {
	return model.BelloBuddyIdentityHash(resumeJobMapKey(target, userID, accountID, jobID) + "\x00" + candidateID)
}

func NewBelloBuddyTypingEngine(resume *BelloBuddyResumeEngine, config BelloBuddyTypingConfig) (*BelloBuddyTypingEngine, error) {
	u, err := url.Parse(config.PublicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(config.DataDir) || utf8.RuneCountInString(config.Article) < 600 || utf8.RuneCountInString(config.Article) > 6000 {
		return nil, errors.New("invalid typing configuration")
	}
	seenFields := map[string]bool{}
	for _, key := range []string{"status", "speed", "accuracy", "duration", "finished_at", "count", "attempt_id", "article", "verification", "errors", "typed", "correct"} {
		id := config.Fields[key]
		if !strings.HasPrefix(id, "fld") || seenFields[id] {
			return nil, errors.New("invalid typing field mapping")
		}
		seenFields[id] = true
	}
	if err = os.MkdirAll(config.DataDir, 0700); err != nil {
		return nil, err
	}
	if err = validateFeishuPath(config.DataDir, true, true); err != nil {
		return nil, err
	}
	e := &BelloBuddyTypingEngine{Resume: resume, Config: config, states: map[string]*BelloBuddyTypingState{}, now: time.Now}
	files, err := os.ReadDir(config.DataDir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(config.DataDir, file.Name())
		if validateFeishuPath(path, false, true) != nil {
			return nil, errors.New("invalid typing state path")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || len(data) > 4<<20 {
			return nil, errors.New("invalid typing state")
		}
		var state BelloBuddyTypingState
		if common.Unmarshal(data, &state) != nil || file.Name() != state.ScopeKey+".json" || len(state.ScopeKey) != 64 || len(state.Token) != 43 {
			return nil, errors.New("invalid typing state")
		}
		e.states[state.ScopeKey] = &state
	}
	return e, nil
}

func StartBelloBuddyTyping() {
	path := os.Getenv("DPIN_TYPING_CONFIG")
	if path == "" {
		return
	}
	if validateFeishuPath(path, false, true) != nil {
		common.SysError("typing: private configuration check failed")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		common.SysError("typing: configuration unavailable")
		return
	}
	var config BelloBuddyTypingConfig
	if common.Unmarshal(data, &config) != nil {
		common.SysError("typing: configuration invalid")
		return
	}
	resume, err := CurrentBelloBuddyResumeEngine()
	if err != nil {
		common.SysError("typing: resume integration unavailable")
		return
	}
	e, err := NewBelloBuddyTypingEngine(resume, config)
	if err != nil {
		common.SysError("typing: private storage/configuration check failed")
		return
	}
	typingEngineMu.Lock()
	typingEngine = e
	typingEngineMu.Unlock()
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := e.SyncOnce(ctx); err != nil {
				common.SysError("typing sync: " + resumeFailureCode(err))
			}
			cancel()
			time.Sleep(3 * time.Second)
		}
	}()
}

func CurrentBelloBuddyTypingEngine() *BelloBuddyTypingEngine {
	typingEngineMu.RLock()
	defer typingEngineMu.RUnlock()
	return typingEngine
}

func (e *BelloBuddyTypingEngine) save(state *BelloBuddyTypingState) error {
	data, err := common.Marshal(state)
	if err != nil {
		return err
	}
	tmp := filepath.Join(e.Config.DataDir, state.ScopeKey+"."+uuid.NewString()+".tmp")
	file, err := createFeishuPrivateFile(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(e.Config.DataDir, state.ScopeKey+".json")); err != nil {
		return err
	}
	return syncFeishuCreatedFile(filepath.Join(e.Config.DataDir, state.ScopeKey+".json"))
}

type BelloBuddyTypingInvitationInput struct {
	JobVersion         int64    `json:"job_version"`
	FilterPlanID       int      `json:"filter_plan_id"`
	FilterVersion      int64    `json:"filter_version"`
	SnapshotRef        string   `json:"snapshot_ref"`
	CandidateID        string   `json:"candidate_id"`
	CandidateName      string   `json:"candidate_name,omitempty"`
	CandidateEducation string   `json:"candidate_education,omitempty"`
	RecruiterMessages  []string `json:"recruiter_messages,omitempty"`
}

type BelloBuddyTypingInvitation struct {
	InvitationID string `json:"invitation_id"`
	State        string `json:"state"`
	Text         string `json:"text"`
	Link         string `json:"link"`
}

// The worker proves greeting/reply ordering before this call. The service independently
// validates the logged-in account and the signed, explicitly enabled plan configuration.
func PrepareBelloBuddyTypingInvitation(identity AuthIdentity, accountKey, jobID string, input BelloBuddyTypingInvitationInput) (*BelloBuddyTypingInvitation, error) {
	e := CurrentBelloBuddyTypingEngine()
	if e == nil {
		return nil, &BelloBuddyError{http.StatusServiceUnavailable, "TYPING_UNAVAILABLE", "打字测评服务暂时不可用"}
	}
	if input.JobVersion <= 0 || input.FilterPlanID <= 0 || input.FilterVersion <= 0 || ValidateBelloBuddyIdentifier(input.CandidateID) != nil || utf8.RuneCountInString(input.CandidateName) > 128 || utf8.RuneCountInString(input.CandidateEducation) > 128 || strings.ContainsAny(input.CandidateName+input.CandidateEducation, "\x00\r\n") || len(input.RecruiterMessages) > 256 {
		return nil, belloInvalid("测评邀请的候选人或配置版本无效")
	}
	total := 0
	for _, message := range input.RecruiterMessages {
		total += len(message)
	}
	if total > 128<<10 {
		return nil, belloInvalid("测评邀请的聊天记录过长")
	}
	snapshot, err := openBelloBuddySnapshot(input.SnapshotRef, identity, accountKey)
	if err != nil {
		return nil, err
	}
	account, job, _, err := e.Resume.ownership(identity.UserID, accountKey, jobID)
	if err != nil {
		return nil, err
	}
	var frozenJob *belloBuddySnapshotJob
	for i := range snapshot.Jobs {
		if snapshot.Jobs[i].JobID == jobID {
			frozenJob = &snapshot.Jobs[i]
			break
		}
	}
	filter, found := snapshot.Plans[input.FilterPlanID]
	if snapshot.AccountID != account.ID || frozenJob == nil || frozenJob.ID != job.ID || frozenJob.Version != input.JobVersion || frozenJob.FilterMode != "plan" || frozenJob.FilterPlanID != input.FilterPlanID || !found || filter.Kind != "filter" || filter.Version != input.FilterVersion {
		return nil, ErrBelloBuddySnapshotInvalid
	}
	if job.Version != input.JobVersion || job.FilterMode != "plan" || job.FilterPlanID != input.FilterPlanID {
		return nil, ErrBelloBuddyConflict
	}
	currentFilter, err := GetBelloBuddyPlan(e.Resume.DB, identity.UserID, input.FilterPlanID, "filter", true)
	if err != nil {
		return nil, err
	}
	if currentFilter.Version != input.FilterVersion {
		return nil, ErrBelloBuddyConflict
	}
	var settings struct {
		EnableTyping bool `json:"enable_typing_assessment"`
	}
	if common.UnmarshalJsonStr(currentFilter.Content, &settings) != nil {
		return nil, ErrBelloBuddyConfig
	}
	if !filter.EnableTyping || !settings.EnableTyping {
		return nil, &BelloBuddyError{http.StatusConflict, "TYPING_NOT_ENABLED", "当前运行筛选方案未启用打字测评"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	key := typingScopeKey(e.Resume.Config.Target, identity.UserID, account.ID, job.ID, input.CandidateID)
	state := e.states[key]
	if state != nil {
		copyState := *state
		copyState.Attempts = slices.Clone(state.Attempts)
		state = &copyState
	}
	if state == nil {
		token, err := newTypingToken()
		if err != nil {
			return nil, err
		}
		state = &BelloBuddyTypingState{ScopeKey: key, Token: token, UserID: identity.UserID, AccountID: account.ID, JobID: job.ID, CandidateID: input.CandidateID, Name: input.CandidateName, Education: input.CandidateEducation, InvitationMode: "plan", CreatedAt: e.now().Unix(), ExpiresAt: e.now().Add(7 * 24 * time.Hour).Unix(), Attempts: []BelloBuddyTypingAttempt{}}
	}
	link := e.Config.PublicURL + "#" + state.Token
	for _, message := range input.RecruiterMessages {
		if strings.Contains(message, link) {
			state.InvitationDelivered = true
			break
		}
	}
	result := &BelloBuddyTypingInvitation{InvitationID: state.ScopeKey, State: "ready", Link: link}
	if latestTypingResult(state) != nil {
		result.State = "completed"
	} else if state.InvitationDelivered {
		result.State = "sent"
	} else if state.ExpiresAt <= e.now().Unix() {
		return nil, &BelloBuddyError{http.StatusConflict, "TYPING_INVITATION_EXPIRED", "原测评邀请已过期，请由人事处理"}
	} else {
		result.Text = "方便的话，请完成一次60秒中文打字测评，帮助我们了解您的客服输入速度和准确率。点击即可开始，无需重复填写个人信息：\n" + link
	}
	if err := e.save(state); err != nil {
		return nil, err
	}
	e.states[key] = state
	return result, nil
}

func latestTypingResult(state *BelloBuddyTypingState) *BelloBuddyTypingAttempt {
	for i := len(state.Attempts) - 1; i >= 0; i-- {
		if state.Attempts[i].FinishedAt != 0 {
			return &state.Attempts[i]
		}
	}
	return nil
}

func (e *BelloBuddyTypingEngine) publicState(state *BelloBuddyTypingState) map[string]any {
	count := 0
	for _, attempt := range state.Attempts {
		if attempt.FinishedAt != 0 {
			count++
		}
	}
	result := map[string]any{"name": state.Name, "duration": 60, "article_name": e.Config.ArticleName, "article": e.Config.Article, "attempt_count": count}
	if latest := latestTypingResult(state); latest != nil {
		result["latest"] = map[string]any{"speed": latest.Speed, "accuracy": latest.Accuracy, "duration": 60, "errors": latest.Errors, "sync": state.SyncedAttempt == latest.ID, "attempt_count": count}
	}
	return result
}

func typingFailure(message string, status int) error {
	return &BelloBuddyError{Status: status, Code: "TYPING_REQUEST_INVALID", Message: message}
}

func (e *BelloBuddyTypingEngine) PublicCall(token, operation string, body []byte) (map[string]any, error) {
	if len(token) != 43 {
		return nil, typingFailure("测评链接无效或已过期，请联系招聘人员重新发送。", http.StatusNotFound)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var state *BelloBuddyTypingState
	for _, value := range e.states {
		if subtle.ConstantTimeCompare([]byte(value.Token), []byte(token)) == 1 {
			state = value
			break
		}
	}
	if state == nil || state.ExpiresAt <= e.now().Unix() {
		return nil, typingFailure("测评链接无效或已过期，请联系招聘人员。", http.StatusNotFound)
	}
	copyState := *state
	copyState.Attempts = slices.Clone(state.Attempts)
	state = &copyState
	if _, _, _, err := e.stateOwnership(state); err != nil {
		return nil, typingFailure("测评所属招聘信息不可用，请联系招聘人员。", http.StatusConflict)
	}
	if operation == "info" {
		return e.publicState(state), nil
	}
	var request struct {
		AttemptID string `json:"attempt_id"`
		Length    int    `json:"length"`
		Typed     string `json:"typed"`
	}
	if len(body) > 40000 || common.Unmarshal(body, &request) != nil {
		return nil, typingFailure("请求内容无效。", 400)
	}
	if operation == "attempt" {
		recent := 0
		for _, attempt := range state.Attempts {
			if e.now().UnixMilli()-max(attempt.CreatedAt, attempt.StartedAt) < 3600*1000 {
				recent++
			}
		}
		if recent >= 10 {
			return nil, typingFailure("测评次数较多，请稍后再试。", 429)
		}
		id, err := newTypingToken()
		if err != nil {
			return nil, err
		}
		state.Attempts = append(state.Attempts, BelloBuddyTypingAttempt{ID: id, CreatedAt: e.now().UnixMilli()})
		if err = e.save(state); err != nil {
			return nil, err
		}
		e.states[state.ScopeKey] = state
		return map[string]any{"attempt_id": id, "article": e.Config.Article, "article_name": e.Config.ArticleName, "duration": 60}, nil
	}
	var attempt *BelloBuddyTypingAttempt
	for i := range state.Attempts {
		if state.Attempts[i].ID == request.AttemptID {
			attempt = &state.Attempts[i]
			break
		}
	}
	if attempt == nil {
		return nil, typingFailure("测评批次无效，请重新开始。", 404)
	}
	now := e.now().UnixMilli()
	if attempt.FinishedAt != 0 {
		return e.publicState(state), nil
	}
	switch operation {
	case "start":
		if attempt.StartedAt == 0 {
			attempt.StartedAt = now
			attempt.LastBeat = now
		}
	case "heartbeat":
		if attempt.StartedAt == 0 || request.Length < 0 || request.Length > utf8.RuneCountInString(e.Config.Article) {
			return nil, typingFailure("输入统计无效。", 400)
		}
		if now-attempt.LastBeat >= 2000 {
			attempt.Beats++
			attempt.LastBeat = now
			attempt.MaxLength = max(attempt.MaxLength, request.Length)
		}
	case "finish":
		typed := []rune(request.Typed)
		article := []rune(e.Config.Article)
		if attempt.StartedAt == 0 || now-attempt.StartedAt < 59500 || now-attempt.StartedAt > 90000 || now-attempt.LastBeat > 12000 || attempt.Beats < 10 || len(typed) == 0 || len(typed) > len(article) {
			return nil, typingFailure("本次测评尚未有效完成或连接已中断，请重新测评；不会新增人才记录。", 400)
		}
		correct := 0
		for i, character := range typed {
			if character == article[i] {
				correct++
			}
		}
		attempt.FinishedAt = now
		attempt.Typed = len(typed)
		attempt.Errors = len(typed) - correct
		attempt.Speed = correct
		attempt.Accuracy = math.Round(float64(correct)*10000/float64(len(typed))) / 100
	default:
		return nil, typingFailure("接口不存在。", 404)
	}
	if err := e.save(state); err != nil {
		return nil, err
	}
	e.states[state.ScopeKey] = state
	result := e.publicState(state)
	result["attempt_id"] = attempt.ID
	return result, nil
}

// Preserve the historical model-based archival gate. A plan invitation never adds one.
func RequireBelloBuddyTyping(jobID int, scopeKey string) error {
	e := CurrentBelloBuddyTypingEngine()
	if e == nil {
		if os.Getenv("DPIN_TYPING_CONFIG") != "" {
			return resumeUnknown("TYPING_CONFIG_UNAVAILABLE")
		}
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.states[scopeKey]
	if state == nil || state.InvitationMode == "plan" {
		return nil
	}
	var job model.BelloBuddyJob
	if e.Resume.DB.First(&job, jobID).Error != nil {
		return resumeUnknown("TYPING_CONFIG_UNAVAILABLE")
	}
	if job.BindingID == 0 {
		return nil
	}
	var binding model.BelloBuddyJobBinding
	if e.Resume.DB.First(&binding, job.BindingID).Error != nil {
		return resumeUnknown("TYPING_CONFIG_UNAVAILABLE")
	}
	if !binding.Enabled || !slices.Contains(e.Config.EnabledModels, binding.Model) {
		return nil
	}
	if state == nil || latestTypingResult(state) == nil || state.RecordID == "" {
		return resumeUnknown("TYPING_REQUIRED")
	}
	return nil
}

func (e *BelloBuddyTypingEngine) SyncOnce(ctx context.Context) error {
	e.mu.Lock()
	var state *BelloBuddyTypingState
	for _, item := range e.states {
		latest := latestTypingResult(item)
		if latest != nil && latest.ID != item.SyncedAttempt && item.RetryAt <= e.now().Unix() {
			copyState := *item
			copyState.Attempts = slices.Clone(item.Attempts)
			state = &copyState
			break
		}
	}
	e.mu.Unlock()
	if state == nil {
		return nil
	}
	key := model.BelloBuddyIdentityHash("feishu-base:" + e.Resume.Config.Target.Base)
	lease, claimed, err := model.ClaimBelloBuddyResumeLease(e.Resume.DB, key, uuid.NewString(), e.now().Unix())
	if err != nil || !claimed {
		return err
	}
	keepBusy := lease.BusyArchiveID != 0 || lease.BusyJobSyncID != 0
	defer func() { _ = model.ReleaseBelloBuddyResumeLease(e.Resume.DB, lease, keepBusy) }()
	if keepBusy {
		return nil
	}
	recordID, err := e.syncState(ctx, state)
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.states[state.ScopeKey]
	if current == nil {
		return nil
	}
	copyCurrent := *current
	copyCurrent.Attempts = slices.Clone(current.Attempts)
	current = &copyCurrent
	if err != nil {
		current.RetryAt = e.now().Add(time.Minute).Unix()
		if saveErr := e.save(current); saveErr != nil {
			return saveErr
		}
		e.states[current.ScopeKey] = current
		return err
	}
	current.RecordID = recordID
	current.SyncedAttempt = latestTypingResult(state).ID
	current.RetryAt = 0
	if err = e.save(current); err != nil {
		return err
	}
	e.states[current.ScopeKey] = current
	// Retry only the known pre-write assessment gate, never unknown external writes.
	var scopes []model.BelloBuddyResumeScope
	if err = e.Resume.DB.Where("scope_key = ?", state.ScopeKey).Find(&scopes).Error; err != nil {
		return err
	}
	for _, scope := range scopes {
		if err = e.Resume.DB.Model(&model.BelloBuddyResumeArchive{}).Where("scope_id = ? AND state = ? AND error_code = ?", scope.ID, "blocked", "TYPING_REQUIRED").Updates(map[string]any{"state": "queued", "error_code": "", "updated_at": e.now().Unix()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// Issued tickets remain usable after plan or model changes; account ownership is live.
func (e *BelloBuddyTypingEngine) stateOwnership(state *BelloBuddyTypingState) (*model.BelloBuddyAccount, *model.BelloBuddyJob, *model.User, error) {
	var account model.BelloBuddyAccount
	var job model.BelloBuddyJob
	var user model.User
	if err := e.Resume.DB.First(&account, state.AccountID).Error; err != nil {
		return nil, nil, nil, err
	}
	if err := e.Resume.DB.First(&job, state.JobID).Error; err != nil {
		return nil, nil, nil, err
	}
	if err := e.Resume.DB.Select("id", "username", "status").First(&user, state.UserID).Error; err != nil {
		return nil, nil, nil, err
	}
	if user.Status != common.UserStatusEnabled || account.UserID != state.UserID || job.AccountID != account.ID {
		return nil, nil, nil, errors.New("typing ownership changed")
	}
	return &account, &job, &user, nil
}

func (e *BelloBuddyTypingEngine) syncState(ctx context.Context, state *BelloBuddyTypingState) (string, error) {
	account, job, user, err := e.stateOwnership(state)
	if err != nil {
		return "", err
	}
	target := e.Resume.Config.Target
	var mapping model.BelloBuddyResumeJobMap
	if err := e.Resume.DB.Where("map_key = ?", resumeJobMapKey(target, state.UserID, account.ID, job.ID)).First(&mapping).Error; err != nil || mapping.RecordID == "" {
		return "", errors.New("typing job catalog mapping is not ready")
	}
	recordID := state.RecordID
	if recordID == "" {
		records, err := e.Resume.Backend.Search(ctx, target.TalentTable, target.Fields["archive_key"], state.ScopeKey)
		if err != nil {
			return "", err
		}
		if len(records) > 1 {
			return "", errors.New("typing talent identity conflict")
		}
		if len(records) == 1 {
			recordID = records[0].ID
		}
	}
	latest := latestTypingResult(state)
	if latest == nil {
		return "", errors.New("typing incomplete")
	}
	count := 0
	for _, a := range state.Attempts {
		if a.FinishedAt != 0 {
			count++
		}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	values := map[string]any{
		e.Config.Fields["status"]: "已完成", e.Config.Fields["speed"]: float64(latest.Speed), e.Config.Fields["accuracy"]: latest.Accuracy,
		e.Config.Fields["duration"]: float64(60), e.Config.Fields["finished_at"]: time.UnixMilli(latest.FinishedAt).In(loc).Format("2006-01-02 15:04:05"),
		e.Config.Fields["count"]: float64(count), e.Config.Fields["attempt_id"]: latest.ID, e.Config.Fields["article"]: e.Config.ArticleName,
		e.Config.Fields["errors"]: float64(latest.Errors), e.Config.Fields["typed"]: float64(latest.Typed), e.Config.Fields["correct"]: float64(latest.Speed),
		e.Config.Fields["verification"]: "招聘会话专属链接；后台计时及文本比对；在线自助测评，未经监考",
	}
	if recordID == "" {
		for field, value := range map[string]any{target.Fields["archive_key"]: state.ScopeKey, target.Fields["candidate_id"]: state.CandidateID, target.Fields["owner"]: user.Username, target.Fields["account"]: account.DisplayName, target.Fields["account_id"]: account.AccountKey, target.Fields["platform"]: account.Platform, target.Fields["job_link"]: []map[string]any{{"id": mapping.RecordID}}} {
			values[field] = value
		}
		if state.Name != "" {
			values[target.Fields["name"]] = state.Name
		}
		if state.Education != "" && e.Config.Fields["education"] != "" {
			values[e.Config.Fields["education"]] = state.Education
		}
	} else {
		record, err := e.Resume.Backend.Get(ctx, target.TalentTable, recordID)
		if err != nil {
			return "", err
		}
		if record.Fields[target.Fields["archive_key"]] != state.ScopeKey || record.Fields[target.Fields["candidate_id"]] != state.CandidateID {
			return "", errors.New("typing talent ownership conflict")
		}
	}
	for field := range values {
		if field == "" {
			return "", errors.New("typing field mapping missing")
		}
	}
	receipt, err := e.Resume.Backend.Write(ctx, target.TalentTable, recordID, values)
	if recordID == "" {
		recordID = resumeCreatedRecordID(receipt)
		if recordID == "" {
			records, findErr := e.Resume.Backend.Search(ctx, target.TalentTable, target.Fields["archive_key"], state.ScopeKey)
			if findErr == nil && len(records) == 1 {
				recordID = records[0].ID
			}
		}
	}
	if recordID == "" {
		return "", errors.New("typing write awaits reconciliation")
	}
	// Reconcile an uncertain write by reading its identity and all assessment cells.
	var verified *FeishuArchiveRecord
	if projected, ok := e.Resume.Backend.(interface {
		GetFields(context.Context, string, string, []string) (*FeishuArchiveRecord, error)
	}); ok {
		ids := []string{target.Fields["archive_key"], target.Fields["candidate_id"]}
		for key, id := range e.Config.Fields {
			if key != "education" {
				ids = append(ids, id)
			}
		}
		verified, err = projected.GetFields(ctx, target.TalentTable, recordID, ids)
	} else {
		verified, err = e.Resume.Backend.Get(ctx, target.TalentTable, recordID)
	}
	if err != nil || verified == nil || verified.Fields[target.Fields["archive_key"]] != state.ScopeKey || verified.Fields[target.Fields["candidate_id"]] != state.CandidateID {
		return "", errors.New("typing write awaits identity verification")
	}
	for _, key := range []string{"status", "speed", "accuracy", "duration", "count", "attempt_id", "article", "errors", "typed", "correct"} {
		field := e.Config.Fields[key]
		if !feishuCellEquals(verified.Fields[field], values[field]) {
			return "", errors.New("typing write awaits score verification")
		}
	}
	return recordID, nil
}

func (e *BelloBuddyTypingEngine) OriginAllowed(origin string) bool {
	return origin == "" || slices.Contains(e.Config.AllowedOrigins, origin)
}

package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// 有效期约束凭证，不限制任务时长；有效期内可续签同一配置，不加载新正文。
const BelloBuddyRuntimeSnapshotTTL int64 = 24 * 60 * 60
const belloBuddyMaxSnapshotBytes = 512 << 10

var ErrBelloBuddySnapshotInvalid = &BelloBuddyError{http.StatusConflict, "RUNTIME_SNAPSHOT_INVALID", "运行快照无效或与当前会话、账号及职位不匹配"}
var ErrBelloBuddySnapshotExpired = &BelloBuddyError{http.StatusConflict, "RUNTIME_SNAPSHOT_EXPIRED", "运行快照凭证已过期，请明确选择如何恢复任务"}

type BelloBuddyRuntimeSnapshot struct {
	SnapshotRef       string `json:"snapshot_ref"`
	SnapshotExpiresAt int64  `json:"snapshot_expires_at"`
}

type belloBuddySnapshotJob struct {
	ID                int    `json:"id"`
	JobID             string `json:"job_id"`
	Version           int64  `json:"version"`
	FilterMode        string `json:"filter_mode"`
	FilterPlanID      int    `json:"filter_plan_id"`
	CommunicationMode string `json:"communication_mode"`
	CommunicationID   int    `json:"communication_plan_id"`
	BindingID         int    `json:"binding_id"`
	BindingVersion    int64  `json:"binding_version"`
	ChannelID         int    `json:"channel_id"`
	Model             string `json:"model"`
	ChannelSignature  string `json:"channel_signature"`
}

type belloBuddySnapshotPlan struct {
	ID           int    `json:"id"`
	Kind         string `json:"kind"`
	Version      int64  `json:"version"`
	FilterPlanID int    `json:"filter_plan_id"`
	EnableAI     bool   `json:"enable_ai_screening"`
}

// 只含服务端签发的路由决策，不存候选人、正文、招聘进度或认证凭据。
type belloBuddySnapshot struct {
	Version        int                            `json:"version"`
	UserID         int                            `json:"user_id"`
	AuthVersion    int64                          `json:"auth_version"`
	SessionID      string                         `json:"session_id"`
	AccountID      int                            `json:"account_id"`
	AccountKey     string                         `json:"account_key"`
	AccountVersion int64                          `json:"account_version"`
	IssuedAt       int64                          `json:"issued_at"`
	ExpiresAt      int64                          `json:"expires_at"`
	Jobs           []belloBuddySnapshotJob        `json:"jobs"`
	Plans          map[int]belloBuddySnapshotPlan `json:"plans"`
}

func belloBuddySnapshotCipher() (cipher.AEAD, error) {
	if len(common.SessionSecret) < 16 {
		return nil, errors.New("runtime snapshot key is unavailable")
	}
	key := sha256.Sum256([]byte("dpin-bellobuddy-runtime-snapshot-v1\x00" + common.SessionSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealBelloBuddySnapshot(snapshot belloBuddySnapshot) (*BelloBuddyRuntimeSnapshot, error) {
	data, err := common.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	aead, err := belloBuddySnapshotCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nonce, nonce, data, []byte("bellobuddy-runtime-v1"))
	reference := base64.RawURLEncoding.EncodeToString(sealed)
	if len(reference) > belloBuddyMaxSnapshotBytes {
		return nil, ErrBelloBuddyConfig
	}
	return &BelloBuddyRuntimeSnapshot{SnapshotRef: reference, SnapshotExpiresAt: snapshot.ExpiresAt}, nil
}

func IssueBelloBuddyRuntimeSnapshot(identity AuthIdentity, config *BelloBuddyRuntimeConfig) error {
	if identity.UserID <= 0 || identity.UserAuthVersion <= 0 || identity.SessionID == "" || config.Account.UserID != identity.UserID {
		return ErrBelloBuddySnapshotInvalid
	}
	snapshot := belloBuddySnapshot{Version: 1, UserID: identity.UserID, AuthVersion: identity.UserAuthVersion, SessionID: identity.SessionID, AccountID: config.Account.ID, AccountKey: config.Account.AccountKey, AccountVersion: config.Account.Version, IssuedAt: config.FetchedAt, ExpiresAt: config.FetchedAt + BelloBuddyRuntimeSnapshotTTL, Plans: make(map[int]belloBuddySnapshotPlan)}
	for _, job := range config.Jobs {
		snapshot.Jobs = append(snapshot.Jobs, belloBuddySnapshotJob{ID: job.ID, JobID: job.JobID, Version: job.Version, FilterMode: job.FilterMode, FilterPlanID: job.FilterPlanID, CommunicationMode: job.CommunicationMode, CommunicationID: job.CommunicationID, BindingID: job.BindingID, BindingVersion: job.BotBindingVersion, ChannelID: job.BotChannelID, Model: job.BotModel, ChannelSignature: job.BotChannelSignature})
	}
	for _, plan := range config.Plans {
		var settings struct {
			EnableAI bool `json:"enable_ai_screening"`
		}
		if plan.Kind == "filter" && common.Unmarshal(plan.Content, &settings) != nil {
			return ErrBelloBuddyConfig
		}
		snapshot.Plans[plan.ID] = belloBuddySnapshotPlan{ID: plan.ID, Kind: plan.Kind, Version: plan.Version, FilterPlanID: plan.FilterPlanID, EnableAI: settings.EnableAI}
	}
	credential, err := sealBelloBuddySnapshot(snapshot)
	if err != nil {
		return err
	}
	config.BelloBuddyRuntimeSnapshot = *credential
	return nil
}

func openBelloBuddySnapshot(reference string, identity AuthIdentity, accountKey string) (*belloBuddySnapshot, error) {
	if reference == "" || len(reference) > belloBuddyMaxSnapshotBytes || strings.TrimSpace(reference) != reference {
		return nil, ErrBelloBuddySnapshotInvalid
	}
	aead, err := belloBuddySnapshotCipher()
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(reference)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrBelloBuddySnapshotInvalid
	}
	data, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("bellobuddy-runtime-v1"))
	if err != nil {
		return nil, ErrBelloBuddySnapshotInvalid
	}
	var snapshot belloBuddySnapshot
	if common.Unmarshal(data, &snapshot) != nil || snapshot.Version != 1 || snapshot.UserID != identity.UserID || snapshot.AuthVersion != identity.UserAuthVersion || snapshot.SessionID != identity.SessionID || snapshot.AccountKey != accountKey || snapshot.AccountID <= 0 || len(snapshot.Jobs) == 0 || len(snapshot.Jobs) > BelloBuddyMaxJobs || snapshot.IssuedAt > common.GetTimestamp() || snapshot.ExpiresAt-snapshot.IssuedAt != BelloBuddyRuntimeSnapshotTTL {
		return nil, ErrBelloBuddySnapshotInvalid
	}
	if snapshot.ExpiresAt <= common.GetTimestamp() {
		return nil, ErrBelloBuddySnapshotExpired
	}
	return &snapshot, nil
}

// 续签只读当前归属和权限，不读最新选择或正文；原版本、模式及关联保持不变。
func RenewBelloBuddyRuntimeSnapshot(identity AuthIdentity, accountKey, reference string) (*BelloBuddyRuntimeSnapshot, error) {
	snapshot, err := openBelloBuddySnapshot(reference, identity, accountKey)
	if err != nil {
		return nil, err
	}
	err = model.WithBelloBuddyAccount(identity.UserID, accountKey, false, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		if account.ID != snapshot.AccountID {
			return ErrBelloBuddySnapshotInvalid
		}
		for _, job := range snapshot.Jobs {
			var current model.BelloBuddyJob
			if err := tx.Where("id = ? AND account_id = ? AND job_hash = ?", job.ID, account.ID, model.BelloBuddyIdentityHash(job.JobID)).First(&current).Error; err != nil {
				return err
			}
		}
		for id, plan := range snapshot.Plans {
			if _, err := GetBelloBuddyPlan(tx, identity.UserID, id, plan.Kind, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	snapshot.IssuedAt = common.GetTimestamp()
	snapshot.ExpiresAt = snapshot.IssuedAt + BelloBuddyRuntimeSnapshotTTL
	return sealBelloBuddySnapshot(*snapshot)
}

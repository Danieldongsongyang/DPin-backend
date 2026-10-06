package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const BelloBuddyScreenModel = "jev-latest"

type BelloBuddyAIInput struct {
	AccountKey      string            `json:"account_key"`
	JobID           string            `json:"job_id"`
	JobVersion      int64             `json:"job_version"`
	SchemaVersion   int               `json:"schema_version"`
	CandidateID     string            `json:"candidate_id"`
	FilterPlanID    int               `json:"filter_plan_id"`
	FilterVersion   int64             `json:"filter_version"`
	BindingVersion  int64             `json:"binding_version"`
	Request         common.RawMessage `json:"request,omitempty"`
	Input           string            `json:"input"`
	ConversationRef string            `json:"conversation_ref"`
}

type BelloBuddyConversationScope struct {
	UserID           int    `json:"user_id"`
	AuthVersion      int64  `json:"auth_version"`
	AccountID        int    `json:"account_id"`
	JobID            int    `json:"job_id"`
	CandidateHash    string `json:"candidate_hash"`
	BindingID        int    `json:"binding_id"`
	BindingVersion   int64  `json:"binding_version"`
	ChannelID        int    `json:"channel_id"`
	ChannelSignature string `json:"channel_signature"`
}

type BelloBuddyAIRoute struct {
	Model     string
	ChannelID int
	Scope     BelloBuddyConversationScope
}

func ResolveBelloBuddyAIRoute(identity AuthIdentity, input BelloBuddyAIInput, reply bool) (*BelloBuddyAIRoute, error) {
	if input.SchemaVersion != BelloBuddySchemaVersion || input.JobVersion <= 0 {
		return nil, belloInvalid("AI 请求缺少有效的配置版本")
	}
	for _, id := range []string{input.AccountKey, input.JobID, input.CandidateID} {
		if err := ValidateBelloBuddyIdentifier(id); err != nil {
			return nil, err
		}
	}
	route := &BelloBuddyAIRoute{Model: BelloBuddyScreenModel}
	err := model.WithBelloBuddyAccount(identity.UserID, input.AccountKey, false, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		var job model.BelloBuddyJob
		if err := tx.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(input.JobID)).First(&job).Error; err != nil {
			return err
		}
		if job.Version != input.JobVersion {
			return ErrBelloBuddyConflict
		}
		if !reply {
			filterID := job.FilterPlanID
			if job.FilterMode != "plan" || filterID != input.FilterPlanID {
				if job.CommunicationMode != "plan" {
					return ErrBelloBuddyConfig
				}
				communication, err := GetBelloBuddyPlan(tx, identity.UserID, job.CommunicationID, "communication", true)
				if err != nil {
					return err
				}
				filterID = communication.FilterPlanID
			}
			if filterID <= 0 || filterID != input.FilterPlanID {
				return ErrBelloBuddyConfig
			}
			filter, err := GetBelloBuddyPlan(tx, identity.UserID, filterID, "filter", true)
			if err != nil {
				return err
			}
			if input.FilterVersion <= 0 || filter.Version != input.FilterVersion {
				return ErrBelloBuddyConflict
			}
			var settings struct {
				EnableAI bool `json:"enable_ai_screening"`
			}
			if common.UnmarshalJsonStr(filter.Content, &settings) != nil || !settings.EnableAI {
				return belloInvalid("当前筛选方案未启用 AI 筛选")
			}
			return nil
		}
		if job.CommunicationMode != "plan" {
			return ErrBelloBuddyConfig
		}
		if _, err := belloBuddyConversationCipher(); err != nil {
			return err
		}
		if _, err := GetBelloBuddyPlan(tx, identity.UserID, job.CommunicationID, "communication", true); err != nil {
			return err
		}
		status, _, err := GetBelloBuddyBotStatus(tx, job)
		if err != nil {
			return err
		}
		if status != "BOT_CONFIGURED" {
			return &BelloBuddyError{http.StatusConflict, status, "当前职位没有可用的沟通机器人"}
		}
		var binding model.BelloBuddyJobBinding
		if err := tx.First(&binding, job.BindingID).Error; err != nil {
			return err
		}
		if input.BindingVersion <= 0 || binding.Version != input.BindingVersion {
			return &BelloBuddyError{http.StatusConflict, "BOT_BINDING_CHANGED", "机器人绑定已变更，请重新读取职位配置"}
		}
		var channel model.Channel
		if err := tx.First(&channel, binding.ChannelID).Error; err != nil {
			return err
		}
		route.Model, route.ChannelID = binding.Model, binding.ChannelID
		route.Scope = BelloBuddyConversationScope{
			UserID: identity.UserID, AuthVersion: identity.UserAuthVersion, AccountID: account.ID, JobID: job.ID,
			CandidateHash: model.BelloBuddyIdentityHash(input.CandidateID), BindingID: binding.ID,
			BindingVersion: binding.Version, ChannelID: binding.ChannelID,
			ChannelSignature: model.BelloBuddyIdentityHash(channel.GetBaseURL() + "\x00" + channel.GetModelMapping() + "\x00" + binding.Model),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return route, nil
}

// 固定渠道也必须满足真实用户组的能力记录，不能依赖 pin 路径跳过的检查。
func CheckBelloBuddyChannelAccess(group, modelName string, channelID int) error {
	var count int64
	err := model.DB.Model(&model.Ability{}).Where(map[string]any{
		"group": group, "model": modelName, "channel_id": channelID, "enabled": true,
	}).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return &BelloBuddyError{http.StatusForbidden, "AI_RESOURCE_FORBIDDEN", "当前用户组无权使用该模型或渠道"}
	}
	return nil
}

func BuildBelloBuddyScreenRequest(raw common.RawMessage) ([]byte, error) {
	var fields map[string]common.RawMessage
	if common.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, belloInvalid("JEV 请求必须是结构化 JSON 对象")
	}
	for name := range fields {
		if !slices.Contains([]string{"model", "state", "questions", "answers"}, name) {
			return nil, belloInvalid("JEV 请求包含不支持的字段")
		}
	}
	if value, ok := fields["model"]; ok {
		var name string
		if common.Unmarshal(value, &name) != nil || name != BelloBuddyScreenModel {
			return nil, belloInvalid("筛选模型由服务端指定")
		}
	}
	name, err := common.Marshal(BelloBuddyScreenModel)
	if err != nil {
		return nil, err
	}
	fields["model"] = name
	return common.Marshal(fields)
}

type belloBuddyConversationReference struct {
	Version   int                         `json:"version"`
	Scope     BelloBuddyConversationScope `json:"scope"`
	ChatID    string                      `json:"chat_id"`
	ExpiresAt int64                       `json:"expires_at"`
}

func belloBuddyConversationCipher() (cipher.AEAD, error) {
	if len(common.SessionSecret) < 16 {
		return nil, errors.New("conversation reference key is unavailable")
	}
	key := sha256.Sum256([]byte("dpin-bellobuddy-conversation-v1\x00" + common.SessionSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func SealBelloBuddyConversation(scope BelloBuddyConversationScope, chatID string) (string, error) {
	id, err := uuid.Parse(chatID)
	if err != nil || id == uuid.Nil || scope.UserID <= 0 || scope.AuthVersion <= 0 {
		return "", errors.New("invalid conversation scope")
	}
	data, err := common.Marshal(belloBuddyConversationReference{Version: 1, Scope: scope, ChatID: chatID, ExpiresAt: common.GetTimestamp() + 30*24*60*60})
	if err != nil {
		return "", err
	}
	aead, err := belloBuddyConversationCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, data, []byte("bellobuddy-maxkb-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func OpenBelloBuddyConversation(reference string, scope BelloBuddyConversationScope) (string, error) {
	invalid := &BelloBuddyError{http.StatusConflict, "CONVERSATION_REFERENCE_INVALID", "会话引用已失效或不匹配，请明确选择如何恢复对话"}
	if reference == "" || len(reference) > 4096 || strings.TrimSpace(reference) != reference {
		return "", invalid
	}
	aead, err := belloBuddyConversationCipher()
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(reference)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return "", invalid
	}
	data, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("bellobuddy-maxkb-v1"))
	if err != nil {
		return "", invalid
	}
	var value belloBuddyConversationReference
	if common.Unmarshal(data, &value) != nil || value.Version != 1 || value.Scope != scope || value.ExpiresAt <= common.GetTimestamp() {
		return "", invalid
	}
	id, err := uuid.Parse(value.ChatID)
	if err != nil || id == uuid.Nil {
		return "", invalid
	}
	return value.ChatID, nil
}

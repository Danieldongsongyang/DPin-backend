package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
)

const FeishuArchiveCLIVersion = "1.0.84"

type FeishuArchiveTarget struct {
	Version     string            `json:"version"`
	Base        string            `json:"base"`
	JobTable    string            `json:"job_table"`
	TalentTable string            `json:"talent_table"`
	Fields      map[string]string `json:"fields"`
}

type ResumeArchiveConfig struct {
	Enabled         bool                `json:"enabled"`
	CLIPath         string              `json:"cli_path"`
	CLISHA256       string              `json:"cli_sha256"`
	AppID           string              `json:"app_id"`
	SecretFile      string              `json:"secret_file"`
	DataDir         string              `json:"data_dir"`
	MaxStorageBytes int64               `json:"max_storage_bytes"`
	Target          FeishuArchiveTarget `json:"target"`
	// 独立扩展，不能改变旧 Target 的序列化及既有职位映射键。
	JobSync struct {
		Enabled bool              `json:"enabled"`
		Fields  map[string]string `json:"fields"`
	} `json:"job_sync"`
}

type FeishuArchiveFailure struct {
	Code   string
	Scopes []string
}

func (e *FeishuArchiveFailure) Error() string { return e.Code }

type FeishuArchiveField struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Multiple bool   `json:"multiple"`
	Options  []struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"options"`
}
type FeishuArchiveRecord struct {
	ID     string
	Fields map[string]any
}
type FeishuArchiveAttachment struct {
	Token, Name string
	Size        int64
}

// 测试替身仅由显式测试构造器注入。日常启动始终使用固定原生 CLI。
type FeishuArchiveBackend interface {
	Field(context.Context, string, string) (*FeishuArchiveField, error)
	Get(context.Context, string, string) (*FeishuArchiveRecord, error)
	Search(context.Context, string, string, string) ([]FeishuArchiveRecord, error)
	Write(context.Context, string, string, map[string]any) (map[string]any, error)
	Upload(context.Context, string, string) (map[string]any, error)
	Remove(context.Context, string, []string) (map[string]any, error)
	DownloadHash(context.Context, string, string) (string, error)
}

type FeishuArchiveCLI struct {
	Config       ResumeArchiveConfig
	fieldMu      sync.Mutex
	fieldCache   map[string]map[string]FeishuArchiveField
	fieldCacheAt map[string]time.Time
}

func (cli *FeishuArchiveCLI) InvalidateFields() {
	cli.fieldMu.Lock()
	defer cli.fieldMu.Unlock()
	cli.fieldCache = nil
	cli.fieldCacheAt = nil
}

func LoadResumeArchiveConfig() (*ResumeArchiveConfig, error) {
	path := os.Getenv("DPIN_RESUME_ARCHIVE_CONFIG")
	if path == "" {
		return nil, &FeishuArchiveFailure{Code: "ARCHIVE_DISABLED"}
	}
	if validateFeishuPath(path, false, true) != nil {
		return nil, &FeishuArchiveFailure{Code: "ARCHIVE_CONFIG_INVALID"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &FeishuArchiveFailure{Code: "ARCHIVE_CONFIG_INVALID"}
	}
	var config ResumeArchiveConfig
	if common.Unmarshal(data, &config) != nil || !config.Enabled || !filepath.IsAbs(config.DataDir) || config.MaxStorageBytes < BelloBuddyResumeMaxBytes || config.Target.Version == "" || config.Target.Base == "" || config.Target.JobTable == "" || config.Target.TalentTable == "" {
		return nil, &FeishuArchiveFailure{Code: "ARCHIVE_CONFIG_INVALID"}
	}
	for _, name := range []string{"resume", "job_link", "name", "owner", "account", "account_id", "platform", "candidate_id", "archive_key", "downloaded_at", "archived_at", "job_name", "hr"} {
		if !strings.HasPrefix(config.Target.Fields[name], "fld") {
			return nil, &FeishuArchiveFailure{Code: "ARCHIVE_CONFIG_INVALID"}
		}
	}
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return nil, &FeishuArchiveFailure{Code: "ARCHIVE_DISK_ERROR"}
	}
	if validateFeishuPath(config.DataDir, true, true) != nil {
		return nil, &FeishuArchiveFailure{Code: "ARCHIVE_CONFIG_INVALID"}
	}
	directories := append([]string{"files", "inputs", "receipts", "downloads", "cli-home", "cli-home/tmp"}, feishuPlatformDirectories()...)
	for _, directory := range directories {
		p := filepath.Join(config.DataDir, directory)
		if err := os.MkdirAll(p, 0700); err != nil {
			return nil, &FeishuArchiveFailure{Code: "ARCHIVE_DISK_ERROR"}
		}
		if validateFeishuPath(p, true, true) != nil {
			return nil, &FeishuArchiveFailure{Code: "ARCHIVE_CONFIG_INVALID"}
		}
	}
	return &config, nil
}

func (cli *FeishuArchiveCLI) ValidateBinary(ctx context.Context) error {
	c := cli.Config
	if validateFeishuPath(c.CLIPath, false, false) != nil {
		return &FeishuArchiveFailure{Code: "CLI_PATH_INVALID"}
	}
	file, err := os.Open(c.CLIPath)
	if err != nil {
		return &FeishuArchiveFailure{Code: "CLI_PATH_INVALID"}
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil || fmt.Sprintf("%x", hash.Sum(nil)) != c.CLISHA256 {
		return &FeishuArchiveFailure{Code: "CLI_HASH_MISMATCH"}
	}
	if err := validateFeishuExecutable(c.CLIPath); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, c.CLIPath, "--version")
	command.Env = append(feishuCLIEnvironment(c), "LARKSUITE_CLI_REMOTE_META=off")
	command.Dir = c.DataDir
	output := &feishuLimitedBuffer{limit: 64 << 10}
	command.Stdout = output
	command.Stderr = &feishuLimitedBuffer{limit: 64 << 10}
	command.WaitDelay = 2 * time.Second
	if err := runFeishuCLI(command); err != nil || !slices.Contains(strings.Fields(output.String()), FeishuArchiveCLIVersion) {
		return &FeishuArchiveFailure{Code: "CLI_VERSION_MISMATCH"}
	}
	return nil
}

// Private files and executable parents must not be redirected through links.
// Platform checks use POSIX modes on macOS and the actual DACL on Windows.
func validateFeishuPath(path string, directory, private bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || validateFeishuLocalPath(path) != nil {
		return errors.New("invalid CLI filesystem path")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || feishuPathIsReparsePoint(current) {
			return errors.New("invalid CLI filesystem object")
		}
		if current == path {
			if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
				return errors.New("invalid CLI filesystem type")
			}
			if err := validateFeishuPermissions(current, info, private); err != nil {
				return err
			}
		} else if current == filepath.Dir(path) {
			if err := validateFeishuPermissions(current, info, private); err != nil {
				return err
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func feishuCLIEnvironment(config ResumeArchiveConfig) []string {
	home := filepath.Join(config.DataDir, "cli-home")
	env := feishuPlatformEnvironment(home)
	return append(env, "HOME="+home, "TZ=Asia/Shanghai", "LANG=en_US.UTF-8", "LARKSUITE_CLI_CONFIG_DIR="+home,
		"LARKSUITE_CLI_BRAND=feishu", "LARKSUITE_CLI_DEFAULT_AS=bot", "LARKSUITE_CLI_STRICT_MODE=bot",
		"LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1")
}

type feishuLimitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *feishuLimitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("CLI output limit")
	}
	return b.Buffer.Write(p)
}

// 参数只由下述固定操作生成；无 shell、个人 PATH、UAT 或个人 CLI 配置。
func (cli *FeishuArchiveCLI) call(ctx context.Context, command, table string, args []string, input any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := cli.ValidateBinary(ctx); err != nil {
		return nil, err
	}
	if validateFeishuPath(cli.Config.SecretFile, false, true) != nil {
		return nil, &FeishuArchiveFailure{Code: "CLI_CREDENTIAL_INVALID"}
	}
	secretData, err := os.ReadFile(cli.Config.SecretFile)
	var credentials struct {
		AppSecret string `json:"app_secret"`
	}
	if err != nil || common.Unmarshal(secretData, &credentials) != nil || credentials.AppSecret == "" {
		return nil, &FeishuArchiveFailure{Code: "CLI_CREDENTIAL_INVALID"}
	}
	argv := []string{"base", command, "--base-token", cli.Config.Target.Base, "--table-id", table, "--as", "bot", "--format", "json"}
	argv = append(argv, args...)
	if input != nil {
		data, err := common.Marshal(input)
		if err != nil {
			return nil, err
		}
		name := filepath.Join("inputs", uuid.NewString()+".json")
		if err := os.WriteFile(filepath.Join(cli.Config.DataDir, name), data, 0600); err != nil {
			return nil, &FeishuArchiveFailure{Code: "ARCHIVE_DISK_ERROR"}
		}
		defer os.Remove(filepath.Join(cli.Config.DataDir, name))
		flag := "--json"
		if command == "+record-list" {
			flag = "--filter-json"
		}
		argv = append(argv, flag, "@"+name)
	}
	process := exec.CommandContext(ctx, cli.Config.CLIPath, argv...)
	process.Dir = cli.Config.DataDir
	process.Env = append(feishuCLIEnvironment(cli.Config), "LARKSUITE_CLI_APP_ID="+cli.Config.AppID, "LARKSUITE_CLI_APP_SECRET="+credentials.AppSecret)
	process.WaitDelay = 2 * time.Second
	out := &feishuLimitedBuffer{limit: 8 << 20}
	stderr := &feishuLimitedBuffer{limit: 64 << 10}
	process.Stdout, process.Stderr = out, stderr
	err = runFeishuCLI(process)
	var envelope struct {
		OK       bool           `json:"ok"`
		Identity string         `json:"identity"`
		Data     map[string]any `json:"data"`
		Error    struct {
			Type          string   `json:"type"`
			Subtype       string   `json:"subtype"`
			Code          int      `json:"code"`
			MissingScopes []string `json:"missing_scopes"`
		} `json:"error"`
	}
	if err == nil && common.Unmarshal(out.Bytes(), &envelope) == nil && envelope.OK && envelope.Identity == "bot" && envelope.Data != nil {
		return envelope.Data, nil
	}
	failure := &FeishuArchiveFailure{Code: "CLI_RESULT_UNKNOWN"}
	if ctx.Err() != nil {
		failure.Code = "CLI_TIMEOUT"
		return nil, failure
	}
	// 附件下载部分失败输出在stdout的data内；固定CLI不会把HTTP403放stderr。
	// 只读取类别和数值码，不回显附件路径、URL、原错误文本或其他供应商正文。
	if command == "+record-download-attachment" && common.Unmarshal(out.Bytes(), &envelope) == nil && !envelope.OK && envelope.Data != nil {
		return nil, feishuDownloadFailure(envelope.Data)
	}
	// stderr 可含进度文本；只解析尾部结构化错误，不回显原始文本。
	data := stderr.Bytes()
	if start := bytes.Index(data, []byte("{\n")); start >= 0 {
		data = data[start:]
	}
	if common.Unmarshal(data, &envelope) == nil {
		failure.Scopes = envelope.Error.MissingScopes
		switch envelope.Error.Type {
		case "authorization", "authentication":
			failure.Code = "FEISHU_PERMISSION_OR_CREDENTIAL"
		case "validation", "config":
			failure.Code = "FEISHU_REQUEST_OR_CONFIG_INVALID"
		case "confirmation":
			failure.Code = "FEISHU_CONFIRMATION_REQUIRED"
		case "network":
			failure.Code = "FEISHU_NETWORK_ERROR"
		}
		if envelope.Error.Code != 0 {
			failure.Code += fmt.Sprintf("_%d", envelope.Error.Code)
		}
	}
	return nil, failure
}

func feishuDownloadFailure(data map[string]any) *FeishuArchiveFailure {
	code := "CLI_RESULT_UNKNOWN"
	if data["type"] == "network" {
		code = "FEISHU_NETWORK_ERROR"
	}
	if data["code"] == float64(403) {
		code = "FEISHU_DOWNLOAD_FORBIDDEN_403"
	}
	return &FeishuArchiveFailure{Code: code}
}

func (cli *FeishuArchiveCLI) Field(ctx context.Context, table, id string) (*FeishuArchiveField, error) {
	cli.fieldMu.Lock()
	defer cli.fieldMu.Unlock()
	if cli.fieldCache == nil {
		cli.fieldCache = map[string]map[string]FeishuArchiveField{}
		cli.fieldCacheAt = map[string]time.Time{}
	}
	if time.Since(cli.fieldCacheAt[table]) > 5*time.Minute {
		data, err := cli.call(ctx, "+field-list", table, []string{"--limit", "200"}, nil)
		if err != nil {
			return nil, err
		}
		encoded, _ := common.Marshal(data["fields"])
		var fields []FeishuArchiveField
		if common.Unmarshal(encoded, &fields) != nil || len(fields) == 0 {
			return nil, &FeishuArchiveFailure{Code: "FEISHU_SCHEMA_INVALID"}
		}
		cli.fieldCache[table] = map[string]FeishuArchiveField{}
		for _, field := range fields {
			cli.fieldCache[table][field.ID] = field
		}
		cli.fieldCacheAt[table] = time.Now()
	}
	field, ok := cli.fieldCache[table][id]
	if !ok {
		return nil, &FeishuArchiveFailure{Code: "FEISHU_SCHEMA_INVALID"}
	}
	return &field, nil
}

// 当前 base/v3 使用列式 CellValue，而非 bitable-v1 的 fields map。
func decodeFeishuRecords(data map[string]any) ([]FeishuArchiveRecord, error) {
	ids, idOK := data["record_id_list"].([]any)
	fields, fieldOK := data["field_id_list"].([]any)
	fieldTypes, _ := data["field_type_list"].([]any)
	rows, rowOK := data["data"].([]any)
	if !idOK || !fieldOK || !rowOK || len(ids) != len(rows) {
		return nil, &FeishuArchiveFailure{Code: "FEISHU_RESPONSE_INVALID"}
	}
	records := make([]FeishuArchiveRecord, 0, len(ids))
	for i, id := range ids {
		row, ok := rows[i].([]any)
		name, valid := id.(string)
		if !ok || !valid || len(row) != len(fields) {
			return nil, &FeishuArchiveFailure{Code: "FEISHU_RESPONSE_INVALID"}
		}
		record := FeishuArchiveRecord{ID: name, Fields: make(map[string]any)}
		for n, field := range fields {
			key, ok := field.(string)
			if !ok {
				return nil, &FeishuArchiveFailure{Code: "FEISHU_RESPONSE_INVALID"}
			}
			value := row[n]
			// base/v3 日期读回为带时区ISO时间；写入用上海本地秒精度。
			// 仅规范datetime列，普通文本不能因为外形像日期而被改写。
			if n < len(fieldTypes) && fieldTypes[n] == "datetime" && value != nil {
				date, ok := value.(string)
				if !ok {
					return nil, &FeishuArchiveFailure{Code: "FEISHU_DATETIME_RESPONSE_INVALID"}
				}
				if date != "" {
					parsed, err := time.Parse(time.RFC3339Nano, date)
					if err != nil {
						return nil, &FeishuArchiveFailure{Code: "FEISHU_DATETIME_RESPONSE_INVALID"}
					}
					value = parsed.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05")
				}
			}
			record.Fields[key] = value
		}
		records = append(records, record)
	}
	return records, nil
}

func (cli *FeishuArchiveCLI) Get(ctx context.Context, table, id string) (*FeishuArchiveRecord, error) {
	args := []string{"--record-id", id}
	// Field projection is table-specific; cross-table IDs must not be supplied.
	keys := []string{"job_name", "hr"}
	if table == cli.Config.Target.TalentTable {
		keys = []string{"resume", "job_link", "name", "owner", "account", "account_id", "platform", "candidate_id", "archive_key", "downloaded_at", "archived_at"}
	}
	for _, key := range keys {
		args = append(args, "--field-id", cli.Config.Target.Fields[key])
	}
	if table == cli.Config.Target.JobTable && cli.Config.JobSync.Enabled {
		ids := make([]string, 0, len(cli.Config.JobSync.Fields))
		for _, id := range cli.Config.JobSync.Fields {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			args = append(args, "--field-id", id)
		}
	}
	data, err := cli.call(ctx, "+record-get", table, args, nil)
	if err != nil {
		return nil, err
	}
	records, err := decodeFeishuRecords(data)
	if err != nil {
		return nil, err
	}
	if len(records) != 1 || records[0].ID != id {
		return nil, &FeishuArchiveFailure{Code: "FEISHU_RECORD_MISSING"}
	}
	return &records[0], nil
}
func (cli *FeishuArchiveCLI) GetFields(ctx context.Context, table, id string, fields []string) (*FeishuArchiveRecord, error) {
	args := []string{"--record-id", id}
	for _, field := range fields {
		args = append(args, "--field-id", field)
	}
	data, err := cli.call(ctx, "+record-get", table, args, nil)
	if err != nil {
		return nil, err
	}
	records, err := decodeFeishuRecords(data)
	if err != nil || len(records) != 1 || records[0].ID != id {
		return nil, &FeishuArchiveFailure{Code: "FEISHU_RECORD_MISSING"}
	}
	return &records[0], nil
}

func (cli *FeishuArchiveCLI) Search(ctx context.Context, table, field, value string) ([]FeishuArchiveRecord, error) {
	filter := map[string]any{"logic": "and", "conditions": [][]any{{field, "==", value}}}
	args := []string{"--limit", "2"}
	for _, key := range []string{"resume", "job_link", "name", "owner", "account", "account_id", "platform", "candidate_id", "archive_key", "downloaded_at", "archived_at"} {
		args = append(args, "--field-id", cli.Config.Target.Fields[key])
	}
	data, err := cli.call(ctx, "+record-list", table, args, filter)
	if err != nil {
		return nil, err
	}
	if more, _ := data["has_more"].(bool); more {
		return nil, &FeishuArchiveFailure{Code: "FEISHU_ARCHIVE_KEY_CONFLICT"}
	}
	return decodeFeishuRecords(data)
}
func (cli *FeishuArchiveCLI) Write(ctx context.Context, table, id string, fields map[string]any) (map[string]any, error) {
	args := []string{}
	if id != "" {
		args = append(args, "--record-id", id)
	}
	return cli.call(ctx, "+record-upsert", table, args, fields)
}
func (cli *FeishuArchiveCLI) Upload(ctx context.Context, id, name string) (map[string]any, error) {
	return cli.call(ctx, "+record-upload-attachment", cli.Config.Target.TalentTable, []string{"--record-id", id, "--field-id", cli.Config.Target.Fields["resume"], "--file", filepath.Join("files", name)}, nil)
}
func (cli *FeishuArchiveCLI) Remove(ctx context.Context, id string, tokens []string) (map[string]any, error) {
	args := []string{"--record-id", id, "--field-id", cli.Config.Target.Fields["resume"], "--yes"}
	for _, token := range tokens {
		args = append(args, "--file-token", token)
	}
	return cli.call(ctx, "+record-remove-attachment", cli.Config.Target.TalentTable, args, nil)
}
func (cli *FeishuArchiveCLI) DownloadHash(ctx context.Context, id, token string) (string, error) {
	name := filepath.Join("downloads", uuid.NewString()+".pdf")
	defer os.Remove(filepath.Join(cli.Config.DataDir, name))
	_, err := cli.call(ctx, "+record-download-attachment", cli.Config.Target.TalentTable, []string{"--record-id", id, "--file-token", token, "--output", name}, nil)
	if err != nil {
		return "", err
	}
	f, err := os.Open(filepath.Join(cli.Config.DataDir, name))
	if err != nil {
		return "", &FeishuArchiveFailure{Code: "FEISHU_ATTACHMENT_UNREADABLE"}
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, BelloBuddyResumeMaxBytes+1))
	if err != nil || size > BelloBuddyResumeMaxBytes {
		return "", &FeishuArchiveFailure{Code: "FEISHU_ATTACHMENT_INVALID"}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

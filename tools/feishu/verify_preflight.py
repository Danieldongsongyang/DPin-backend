#!/usr/bin/env python3
"""用部署固定 CLI 只读复核 F1 前置契约；不改字段、记录、附件或任务。"""
import argparse
from datetime import datetime, timezone, timedelta
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--evidence-dir", required=True)
    args = parser.parse_args()
    evidence = Path(args.evidence_dir)
    evidence.mkdir(parents=True, exist_ok=True, mode=0o700)
    if evidence.is_symlink() or evidence.stat().st_mode & 0o077:
        raise RuntimeError("证据目录必须为私有真实目录")
    receipt = {"started_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
               "readonly": True, "cli_calls": 0, "ready_for_real_archive": False,
               "checks": {}, "new_external_writes": 0, "extra_ai_calls": 0}
    output = evidence / ("preflight-" + uuid.uuid4().hex + ".json")
    try:
        config = json.loads(Path(args.config).read_text())
        binary = Path(config["cli_path"])
        secret = Path(config["secret_file"])
        if binary.is_symlink() or not binary.is_absolute() or not stat.S_ISREG(binary.stat().st_mode):
            raise RuntimeError("CLI_PATH_INVALID")
        if hashlib.sha256(binary.read_bytes()).hexdigest() != config["cli_sha256"]:
            raise RuntimeError("CLI_HASH_MISMATCH")
        if secret.is_symlink() or not stat.S_ISREG(secret.stat().st_mode) or secret.stat().st_mode & 0o077:
            raise RuntimeError("CLI_CREDENTIAL_INVALID")
        data_dir = Path(config["data_dir"])
        cli_home = str(data_dir / "cli-home")
        env = {"PATH": "/usr/bin:/bin", "HOME": cli_home, "TZ": "Asia/Shanghai",
               "LARKSUITE_CLI_CONFIG_DIR": cli_home,
               "LARKSUITE_CLI_APP_ID": config["app_id"],
               "LARKSUITE_CLI_APP_SECRET": json.loads(secret.read_text())["app_secret"],
               "LARKSUITE_CLI_BRAND": "feishu", "LARKSUITE_CLI_DEFAULT_AS": "bot",
               "LARKSUITE_CLI_STRICT_MODE": "bot", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER": "1",
               "LARKSUITE_CLI_NO_SKILLS_NOTIFIER": "1"}
        version = subprocess.run([str(binary), "--version"], env=env, capture_output=True, timeout=10)
        if version.returncode or "1.0.84" not in version.stdout.decode().split():
            raise RuntimeError("CLI_VERSION_MISMATCH")
        target = config["target"]

        def read(command, table=None, flags=()):
            argv = [str(binary), "base", command, "--base-token", target["base"],
                    "--as", "bot", "--format", "json"]
            if table:
                argv += ["--table-id", table]
            receipt["cli_calls"] += 1
            process = subprocess.run(argv + list(flags), env=env, cwd=data_dir,
                                     capture_output=True, timeout=45)
            if process.returncode:
                # 仅保存结构化错误分类，绝不输出 stderr 或凭据环境。
                raw = process.stderr
                start = raw.find(b"{\n")
                try:
                    error = json.loads(raw[start:])["error"] if start >= 0 else {}
                except (ValueError, KeyError):
                    error = {}
                receipt["cli_failure"] = {key: error[key] for key in
                                          ("type", "subtype", "code", "missing_scopes") if key in error}
                raise RuntimeError("CLI_READ_FAILED")
            envelope = json.loads(process.stdout)
            if envelope.get("ok") is not True or envelope.get("identity") != "bot":
                raise RuntimeError("CLI_RESPONSE_INVALID")
            return envelope["data"]

        jobs = read("+field-list", target["job_table"], ("--limit", "200"))["fields"]
        talents = read("+field-list", target["talent_table"], ("--limit", "200"))["fields"]
        receipt["fields"] = {target["job_table"]: jobs, target["talent_table"]: talents}
        by_id = {field["id"]: field for field in jobs + talents}
        hr = by_id.get(target["fields"]["hr"], {})
        receipt["hr"] = {"id": hr.get("id"), "type": hr.get("type"), "multiple": hr.get("multiple")}
        checks = receipt["checks"]
        checks["hr_is_text"] = hr.get("type") == "text"
        expected = {"job_name": "text", "resume": "attachment", "job_link": "link",
                    "name": "text", "owner": "text", "account": "text", "account_id": "text",
                    "platform": "text", "candidate_id": "text", "archive_key": "text",
                    "downloaded_at": "datetime", "archived_at": "datetime"}
        for key, field_type in expected.items():
            source = jobs if key == "job_name" else talents
            checks["field_" + key] = any(field["id"] == target["fields"][key] and
                                        field["type"] == field_type for field in source)
        checks["job_link_target"] = by_id.get(target["fields"]["job_link"], {}).get("link_table") == target["job_table"]
        workflows = read("+workflow-list")
        receipt["workflow_count"] = workflows.get("total")
        receipt["workflow_configuration_hash"] = hashlib.sha256(
            json.dumps(workflows, sort_keys=True).encode()).hexdigest()
        # 有工作流须另外审查触发行为；不自动禁用或修改用户配置。
        checks["no_workflow_requires_review"] = workflows.get("total") == 0
        records = {}
        for table in (target["job_table"], target["talent_table"]):
            projection = target["fields"]["job_name" if table == target["job_table"] else "archive_key"]
            result = read("+record-list", table, ("--limit", "3", "--field-id", projection))
            records[table] = {"record_id_list": result.get("record_id_list"), "has_more": result.get("has_more")}
        # 只保存记录 ID，不保留无关业务行内容，不认领已存在记录。
        receipt["existing_records"] = records
        receipt["ready_for_real_archive"] = all(checks.values())
        if not checks["hr_is_text"]:
            receipt["blocked_code"] = "HR_FIELD_NOT_TEXT"
        elif not receipt["ready_for_real_archive"]:
            receipt["blocked_code"] = "FEISHU_PREFLIGHT_REQUIRES_REVIEW"
    except Exception as error:
        receipt["failure"] = {"type": type(error).__name__}
        if isinstance(error, RuntimeError):
            receipt["failure"]["code"] = str(error)
    finally:
        receipt["finished_at"] = datetime.now(timezone(timedelta(hours=8))).isoformat()
        with os.fdopen(os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as file:
            json.dump(receipt, file, ensure_ascii=False, indent=2)
        print(json.dumps({"ready_for_real_archive": receipt["ready_for_real_archive"],
                          "hr": receipt.get("hr"), "blocked_code": receipt.get("blocked_code"),
                          "failed_checks": [key for key, ok in receipt["checks"].items() if not ok],
                          "failure": receipt.get("failure"), "evidence": str(output)}, ensure_ascii=False))
    return 0 if receipt["ready_for_real_archive"] else 2


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""只读复验本批 F1_TEST_F2 的真实人才/职位/附件；不写飞书业务记录。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--evidence-dir", required=True)
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    target = config["target"]
    directory = Path(args.evidence_dir)
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    result = {"ok": False, "real_api": True, "read_only_cloud": True, "real_boss_download": False, "model_calls": 0, "archives": []}
    env = {"PATH": "/usr/bin:/bin", "HOME": config["data_dir"] + "/cli-home",
           "LARKSUITE_CLI_CONFIG_DIR": config["data_dir"] + "/cli-home", "LARKSUITE_CLI_APP_ID": config["app_id"],
           "LARKSUITE_CLI_APP_SECRET": json.loads(Path(config["secret_file"]).read_text())["app_secret"],
           "LARKSUITE_CLI_BRAND": "feishu", "LARKSUITE_CLI_DEFAULT_AS": "bot", "LARKSUITE_CLI_STRICT_MODE": "bot",
           "LARKSUITE_CLI_NO_UPDATE_NOTIFIER": "1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER": "1", "TZ": "Asia/Shanghai"}

    def cli(command, table, flags):
        process = subprocess.run([config["cli_path"], "base", command, "--base-token", target["base"], "--table-id", table,
                                  "--as", "bot", "--format", "json"] + flags, cwd=config["data_dir"], env=env, capture_output=True, timeout=45)
        if process.returncode:
            raise RuntimeError("CLI_RESULT_UNKNOWN")
        reply = json.loads(process.stdout)
        if reply.get("ok") is not True or reply.get("identity") != "bot":
            raise RuntimeError("CLI_RESULT_INVALID")
        return reply["data"]

    def record(table, record_id):
        data = cli("+record-get", table, ["--record-id", record_id])
        assert data["record_id_list"] == [record_id]
        return dict(zip(data["field_id_list"], data["data"][0]))

    try:
        result["stage"] = "cli_hash"
        assert hashlib.sha256(Path(config["cli_path"]).read_bytes()).hexdigest() == config["cli_sha256"]
        database = Path(__file__).resolve().parents[2] / "one-api.db"
        with sqlite3.connect("file:" + str(database) + "?mode=ro", uri=True) as db:
            db.row_factory = sqlite3.Row
            rows = db.execute("SELECT a.*,s.record_id,s.scope_key,s.candidate_id,u.username,c.account_key,j.job_id,m.record_id AS job_record FROM bello_buddy_resume_archives a JOIN bello_buddy_resume_scopes s ON s.id=a.scope_id JOIN users u ON u.id=s.user_id JOIN bello_buddy_accounts c ON c.id=s.account_id JOIN bello_buddy_jobs j ON j.id=s.job_id JOIN bello_buddy_resume_job_maps m ON m.id=s.job_map_id WHERE s.candidate_id IN ('F1_TEST_F2_RUN1','F1_TEST_F2_RESTART')").fetchall()
            assert len(rows) == 2
            for row in rows:
                result["stage"] = "ownership_and_completion"
                assert row["username"] == "F1_TEST" and row["account_key"] == "F1_TEST_F2_ACCOUNT" and row["job_id"] == "F1_TEST_F2_JOB"
                assert row["state"] == "archived" and row["stage"] == "completed"
                job, talent = record(target["job_table"], row["job_record"]), record(target["talent_table"], row["record_id"])
                fields = target["fields"]
                result["stage"] = "record_fields"
                assert job[fields["hr"]] == row["username"]
                assert talent[fields["name"]] == "F1_TEST" and talent[fields["archive_key"]] == row["scope_key"]
                assert talent[fields["job_link"]][0]["id"] == row["job_record"]
                assert [item["file_token"] for item in talent[fields["resume"]]] == [row["file_token"]]
                name = "F1_TEST_F2_READBACK_" + uuid.uuid4().hex + ".pdf"
                result["stage"] = "attachment_download"
                output = Path(config["data_dir"]) / "downloads" / name
                cli("+record-download-attachment", target["talent_table"], ["--record-id", row["record_id"], "--file-token", row["file_token"], "--output", "downloads/" + name])
                pdf = output.read_bytes()
                with os.fdopen(os.open(directory / name, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "wb") as stream:
                    stream.write(pdf)
                output.unlink()
                assert len(pdf) == row["size"] and hashlib.sha256(pdf).hexdigest() == row["sha256"]
                result["stage"] = "stable_upload_receipt"
                uploads = db.execute("SELECT client_upload_id FROM bello_buddy_resume_uploads WHERE archive_id=?", (row["id"],)).fetchall()
                assert len(uploads) == 1
                result["archives"].append({key: row[key] for key in ["archive_id", "candidate_id", "record_id", "job_record", "sha256", "size", "downloaded_at"]} | {"client_upload_id": uploads[0][0], "record_link_username_and_hash": True})
        result["ok"] = True
        result["stage"] = "completed"
    except Exception as error:
        result["code"] = str(error) if isinstance(error, RuntimeError) else type(error).__name__
    filename = directory / ("client-readback-" + uuid.uuid4().hex + ".json")
    with os.fdopen(os.open(filename, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "w") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2)
    print(json.dumps({"ok": result["ok"], "archives": len(result["archives"]), "evidence": str(filename)}))
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""继续本批 F1_TEST 原任务的真实归档；不操作未标记对象，不盲重试 CLI 写入。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--evidence-dir", required=True)
    args = parser.parse_args()
    evidence = Path(args.evidence_dir)
    config = json.loads(Path(args.config).read_text())
    target = config["target"]
    prior = json.loads((evidence / "real-api-results.json").read_text())
    pdf = (evidence / "F1_TEST.pdf").read_bytes()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    result = {"ok": False, "checks": {}, "real_api": True, "real_boss_download": False,
              "extra_ai_calls": 0, "archives": []}
    state_path = evidence / "real-archive-continuation.json"
    progress = json.loads(state_path.read_text()) if state_path.exists() else {}

    def save():
        temp = state_path.with_suffix(".tmp")
        with os.fdopen(os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as file:
            json.dump(progress, file, ensure_ascii=False, indent=2)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temp, state_path)

    def request(method, route, token, body=None, content_type="application/json"):
        payload = body if isinstance(body, bytes) else json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request("http://127.0.0.1:3000" + route, method=method, data=payload,
                                     headers={"Content-Type": content_type, "Authorization": "Bearer " + token})
        try:
            with opener.open(req, timeout=60) as response:
                data = json.load(response)
                if data.get("success") is not True:
                    raise RuntimeError("DPIN_RESULT_INVALID")
                return data["data"]
        except urllib.error.HTTPError as error:
            raise RuntimeError("DPIN_HTTP_" + str(error.code)) from None

    env = {"PATH": "/usr/bin:/bin", "HOME": config["data_dir"] + "/cli-home",
           "LARKSUITE_CLI_CONFIG_DIR": config["data_dir"] + "/cli-home",
           "LARKSUITE_CLI_APP_ID": config["app_id"],
           "LARKSUITE_CLI_APP_SECRET": json.loads(Path(config["secret_file"]).read_text())["app_secret"],
           "LARKSUITE_CLI_BRAND": "feishu", "LARKSUITE_CLI_DEFAULT_AS": "bot",
           "LARKSUITE_CLI_STRICT_MODE": "bot", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER": "1",
           "LARKSUITE_CLI_NO_SKILLS_NOTIFIER": "1", "TZ": "Asia/Shanghai"}

    def cli(command, table, flags):
        process = subprocess.run([config["cli_path"], "base", command, "--base-token", target["base"],
                                  "--table-id", table, "--as", "bot", "--format", "json"] + flags,
                                 env=env, cwd=config["data_dir"], capture_output=True, timeout=45)
        if process.returncode:
            raise RuntimeError("CLI_RESULT_UNKNOWN")
        reply = json.loads(process.stdout)
        if reply.get("ok") is not True or reply.get("identity") != "bot":
            raise RuntimeError("CLI_RESULT_INVALID")
        return reply["data"]

    def record(table, record_id):
        reply = cli("+record-get", table, ["--record-id", record_id])
        if reply["record_id_list"] != [record_id]:
            raise RuntimeError("RECORD_RESPONSE_INVALID")
        return dict(zip(reply["field_id_list"], reply["data"][0]))

    def mapping(archive_id):
        with sqlite3.connect("file:" + str(Path(__file__).resolve().parents[2] / "one-api.db") + "?mode=ro", uri=True) as db:
            db.row_factory = sqlite3.Row
            row = db.execute("SELECT a.*,s.record_id,s.scope_key,s.candidate_id,u.username,c.account_key,j.job_id,m.record_id AS job_record FROM bello_buddy_resume_archives a JOIN bello_buddy_resume_scopes s ON s.id=a.scope_id JOIN users u ON u.id=s.user_id JOIN bello_buddy_accounts c ON c.id=s.account_id JOIN bello_buddy_jobs j ON j.id=s.job_id JOIN bello_buddy_resume_job_maps m ON m.id=s.job_map_id WHERE a.archive_id=?", (archive_id,)).fetchone()
        if not row or row["username"] not in ("F1_TEST", "F1_TEST_B") or not all(row[key].startswith("F1_TEST") for key in ("candidate_id", "account_key", "job_id")):
            raise RuntimeError("TEST_OBJECT_OWNERSHIP_INVALID")
        return dict(row)

    def wait(archive_id, token):
        deadline = time.monotonic() + 180
        previous = None
        while time.monotonic() < deadline:
            task = request("GET", "/api/bellobuddy/resume-archives/" + archive_id, token)
            if task["state"] != previous:
                print(json.dumps({"archive_id": archive_id, "state": task["state"], "stage": task["stage"], "code": task["code"]}), flush=True)
                previous = task["state"]
            if task["state"] == "archived" and task["stage"] == "completed":
                return task
            if task["state"] == "blocked":
                raise RuntimeError(task["code"])
            time.sleep(3)
        raise RuntimeError("ARCHIVE_WAIT_TIMEOUT")

    def attachments(fields):
        return fields.get(target["fields"]["resume"]) or []

    def download_hash(record_id, token):
        name = "F1_TEST_READBACK_" + uuid.uuid4().hex + ".pdf"
        output = Path(config["data_dir"]) / "downloads" / name
        cli("+record-download-attachment", target["talent_table"], ["--record-id", record_id, "--file-token", token, "--output", "downloads/" + name])
        data = output.read_bytes()
        (evidence / name).write_bytes(data)
        output.unlink()
        return hashlib.sha256(data).hexdigest()

    try:
        if hashlib.sha256(Path(config["cli_path"]).read_bytes()).hexdigest() != config["cli_sha256"]:
            raise RuntimeError("CLI_HASH_MISMATCH")
        fields = cli("+field-list", target["job_table"], ["--limit", "200"])["fields"]
        if not any(f["id"] == target["fields"]["hr"] and f["type"] == "text" for f in fields):
            raise RuntimeError("HR_FIELD_NOT_TEXT")
        tokens = {}
        for user in json.loads((evidence / "api-fixture.credentials.json").read_text()):
            if user["username"] not in ("F1_TEST", "F1_TEST_B"):
                raise RuntimeError("TEST_USER_INVALID")
            tokens[user["username"]] = request("POST", "/api/user/login", "", user)["access_token"]
        rows = []
        for item in prior["archives"]:
            row = mapping(item["archive_id"])
            token = tokens[row["username"]]
            if row["state"] == "blocked":
                request("POST", "/api/bellobuddy/resume-archives/" + row["archive_id"] + "/reconcile", token)
            task = wait(row["archive_id"], token)
            result["archives"].append(task)
            row = mapping(row["archive_id"])
            job = record(target["job_table"], row["job_record"])
            talent = record(target["talent_table"], row["record_id"])
            assert job[target["fields"]["hr"]] == row["username"]
            assert talent[target["fields"]["job_link"]][0]["id"] == row["job_record"]
            assert talent[target["fields"]["archive_key"]] == row["scope_key"]
            assert download_hash(row["record_id"], row["file_token"]) == row["sha256"]
            rows.append(row)
        result["checks"]["real_records_links_hashes_and_full_username"] = True
        result["checks"]["four_scopes_isolated"] = len({r["record_id"] for r in rows}) == 4 and len({r["job_record"] for r in rows}) == 4
        first = rows[0]
        token = tokens[first["username"]]
        fields = record(target["talent_table"], first["record_id"])
        manual_name = "F1_TEST_MANUAL.pdf"
        manual = [item for item in attachments(fields) if item["name"] == manual_name]
        if not manual:
            if progress.get("manual_upload_started"):
                raise RuntimeError("MANUAL_TEST_ATTACHMENT_RESULT_UNKNOWN")
            progress["manual_upload_started"] = True
            save()
            manual_path = Path(config["data_dir"]) / "inputs" / manual_name
            if manual_path.exists() and manual_path.read_bytes() != pdf:
                raise RuntimeError("MANUAL_TEST_FILE_CONFLICT")
            manual_path.write_bytes(pdf)
            cli("+record-upload-attachment", target["talent_table"], ["--record-id", first["record_id"], "--field-id", target["fields"]["resume"], "--file", "inputs/" + manual_name])
            manual = [item for item in attachments(record(target["talent_table"], first["record_id"])) if item["name"] == manual_name]
        assert len(manual) == 1 and download_hash(first["record_id"], manual[0]["file_token"]) == hashlib.sha256(pdf).hexdigest()
        progress["manual_token"] = manual[0]["file_token"]
        save()
        route = f"/api/bellobuddy/accounts/{first['account_key']}/jobs/{first['job_id']}/resume-archives"
        metadata = {"client_upload_id": "F1_TEST_VERSION_2", "candidate_id": first["candidate_id"], "candidate_name": "F1_TEST候选人", "downloaded_at": progress.setdefault("v2_downloaded_at", int(time.time() * 1000))}
        save()
        version2 = pdf + b"\n% F1_TEST version 2\n"
        boundary = "F1_TEST_" + uuid.uuid4().hex
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n'.encode() + json.dumps(metadata).encode() + f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="F1_TEST.pdf"\r\nContent-Type: application/pdf\r\n\r\n'.encode() + version2 + f"\r\n--{boundary}--\r\n".encode())
        # 跨进程恢复先按原编号查询；只有明确404才发送同一稳定编号。
        try:
            new = request("GET", route + "/by-upload/" + metadata["client_upload_id"], token)
        except RuntimeError as error:
            if str(error) != "DPIN_HTTP_404":
                raise
            new = request("POST", route, token, body, "multipart/form-data; boundary=" + boundary)
        progress["v2_archive_id"] = new["archive_id"]
        save()
        task = wait(new["archive_id"], token)
        second = mapping(task["archive_id"])
        final = record(target["talent_table"], second["record_id"])
        found = {item["file_token"] for item in attachments(final)}
        result["checks"]["new_version_same_record"] = second["record_id"] == first["record_id"]
        result["checks"]["trusted_old_removed_manual_preserved"] = first["file_token"] not in found and progress["manual_token"] in found and second["file_token"] in found and len(found) == 2
        result["checks"]["replacement_sha256"] = download_hash(second["record_id"], second["file_token"]) == hashlib.sha256(version2).hexdigest()
        duplicate = request("POST", route, token, body, "multipart/form-data; boundary=" + boundary)
        result["checks"]["same_upload_deduplicated"] = duplicate["archive_id"] == task["archive_id"] and duplicate["state"] == "archived" and attachments(record(target["talent_table"], second["record_id"])) == attachments(final)
        old = request("GET", "/api/bellobuddy/resume-archives/" + first["archive_id"], token)
        result["checks"]["old_version_superseded"] = old["state"] == "superseded"
        result["ok"] = all(result["checks"].values())
        result["version2"] = task
    except Exception as error:
        result["failure"] = {"type": type(error).__name__, "code": str(error) if isinstance(error, RuntimeError) else "VERIFICATION_FAILED"}
    with os.fdopen(os.open(evidence / ("real-archive-result-" + uuid.uuid4().hex + ".json"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as file:
        json.dump(result, file, ensure_ascii=False, indent=2)
    print(json.dumps({"ok": result["ok"], "checks": result["checks"], "failure": result.get("failure")}, ensure_ascii=False), flush=True)
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

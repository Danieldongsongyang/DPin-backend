#!/usr/bin/env python3
"""真实本机 API + F1_TEST PDF；正常注册/登录，回执中不保存密码和 Token。"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import secrets
import sqlite3
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--evidence-dir", required=True)
    args = parser.parse_args()
    evidence = Path(args.evidence_dir)
    pdf = (evidence / "F1_TEST.pdf").read_bytes()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    base = "http://127.0.0.1:3000"

    def request(method, path, token="", body=None, content_type="application/json", origin=""):
        data = body if isinstance(body, bytes) else (json.dumps(body).encode() if body is not None else None)
        req = urllib.request.Request(base + path, data=data, method=method)
        req.add_header("Content-Type", content_type)
        if token:
            req.add_header("Authorization", "Bearer " + token)
        if origin:
            req.add_header("Origin", origin)
        try:
            with opener.open(req, timeout=60) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            data = error.read()
            try:
                return error.code, json.loads(data)
            except json.JSONDecodeError:
                return error.code, {"success": False, "code": f"HTTP_{error.code}"}

    status = request("GET", "/api/status")[1]["data"]
    if status.get("turnstile_check") or status.get("email_verification") or status.get("password_login_encryption_enabled"):
        raise RuntimeError("当前需要附加登录验证，保留其要求，不使用验证豁免")
    credential_path = evidence / "api-fixture.credentials.json"
    if credential_path.exists():
        credentials = json.loads(credential_path.read_text())
    else:
        with sqlite3.connect(Path(__file__).resolve().parents[2] / "one-api.db") as database:
            existing = {row[0] for row in database.execute("SELECT username FROM users WHERE username IN ('F1_TEST','F1_TEST_B')")}
        if existing:
            raise RuntimeError("测试用户名已存在且无本批凭据回执，不认领原账号")
        credentials = [{"username": name, "password": secrets.token_urlsafe(24)} for name in ("F1_TEST", "F1_TEST_B")]
        fd = os.open(credential_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            json.dump(credentials, output)
        for user in credentials:
            code, reply = request("POST", "/api/user/register", body=user)
            if code != 200 or not reply.get("success"):
                raise RuntimeError("正常注册未完成，不修改验证或注册配置")
    tokens = []
    for user in credentials:
        code, reply = request("POST", "/api/user/login", body=user)
        if code != 200 or not reply.get("success"):
            raise RuntimeError("正常登录未完成")
        tokens.append(reply["data"]["access_token"])
    account, job = "F1_TEST_ACCOUNT_A", "F1_TEST_JOB_A"
    for token, key, jobs in ((tokens[0], account, [job, "F1_TEST_JOB_B"]),
                             (tokens[0], "F1_TEST_ACCOUNT_B", [job]),
                             (tokens[1], account, [job])):
        code, reply = request("POST", f"/api/bellobuddy/accounts/{key}/jobs/sync", token,
                              {"display_name": key, "jobs": [{"job_id": value, "name": "F1_TEST测试职位", "online": True} for value in jobs]})
        if code != 200 or not reply.get("success"):
            raise RuntimeError("测试账号/职位同步未完成")
    input_data = {"client_upload_id": "F1_TEST_UPLOAD_A", "candidate_id": "F1_TEST_CANDIDATE_A",
                  "candidate_name": "F1_TEST候选人", "downloaded_at": int(time.time() * 1000)}

    def upload(token, key, value, metadata, payload=pdf, origin=""):
        boundary = "F1_TEST_" + uuid.uuid4().hex
        body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"metadata\"\r\n\r\n".encode()
                + json.dumps(metadata).encode() + f"\r\n--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"F1_TEST.pdf\"\r\nContent-Type: application/pdf\r\n\r\n".encode()
                + payload + f"\r\n--{boundary}--\r\n".encode())
        return request("POST", f"/api/bellobuddy/accounts/{key}/jobs/{value}/resume-archives", token, body,
                       "multipart/form-data; boundary=" + boundary, origin)

    code, reply = upload(tokens[0], account, job, input_data)
    if code != 202:
        raise RuntimeError("真实 PDF 未可靠接收，错误码：" + str(reply.get("code")))
    archive_id = reply["data"]["archive_id"]
    checks = {"pdf_accepted": True, "server_sha256_verified": reply["data"]["sha256"] == hashlib.sha256(pdf).hexdigest()}
    code, lookup = request("GET", f"/api/bellobuddy/accounts/{account}/jobs/{job}/resume-archives/by-upload/{input_data['client_upload_id']}", tokens[0])
    checks["lost_receive_response_lookup"] = code == 200 and lookup["data"]["archive_id"] == archive_id
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(upload, tokens[0], account, job, dict(input_data, client_upload_id="F1_TEST_CONCURRENT_" + str(i))) for i in range(2)]
        checks["concurrent_receive_deduplicated"] = all(code == 202 and value["data"]["archive_id"] == archive_id for code, value in (future.result() for future in futures))
    checks["foreign_user_status_denied"] = request("GET", f"/api/bellobuddy/resume-archives/{archive_id}", tokens[1])[0] == 404
    checks["anonymous_denied"] = request("GET", f"/api/bellobuddy/resume-archives/{archive_id}")[0] == 401
    checks["foreign_account_denied"] = upload(tokens[1], "F1_TEST_ACCOUNT_B", job, dict(input_data, client_upload_id="F1_TEST_FOREIGN"))[0] == 404
    checks["arbitrary_owner_rejected"] = upload(tokens[0], account, job, dict(input_data, user_id=99999))[0] == 400
    checks["cross_site_multipart_rejected"] = upload(tokens[0], account, job, input_data, origin="https://evil.example")[0] == 403
    checks["same_upload_different_content_conflict"] = upload(tokens[0], account, job, input_data, pdf + b"\n% F1_TEST changed\n")[0] == 409
    tasks = [archive_id]
    for token, key, value, number in ((tokens[0], "F1_TEST_ACCOUNT_B", job, 1),
                                      (tokens[0], account, "F1_TEST_JOB_B", 2),
                                      (tokens[1], account, job, 3)):
        code, reply = upload(token, key, value, dict(input_data, client_upload_id="F1_TEST_ISOLATED_" + str(number)))
        if code != 202:
            raise RuntimeError("隔离范围文件未接收")
        tasks.append(reply["data"]["archive_id"])
    checks["different_user_account_job_isolated"] = len(set(tasks)) == 4
    deadline = time.monotonic() + 45
    states = []
    while time.monotonic() < deadline:
        states = [request("GET", f"/api/bellobuddy/resume-archives/{value}", tokens[0] if i < 3 else tokens[1])[1]["data"] for i, value in enumerate(tasks)]
        if all(value["state"] in ("blocked", "archived") for value in states):
            break
        time.sleep(2)
    checks["no_false_archive_success"] = all(value["state"] != "archived" or value["stage"] == "completed" for value in states)
    result = {"checks": checks, "archives": states, "test_users": [user["username"] for user in credentials],
              "normal_authentication_used": True, "feishu_real_archive_verified": all(value["state"] == "archived" for value in states),
              "real_boss_download": False, "extra_ai_calls": 0}
    (evidence / "real-api-results.json").write_text(json.dumps(result, ensure_ascii=False, indent=2))
    print(json.dumps({"checks": checks, "states": [{"archive_id": value["archive_id"], "state": value["state"], "code": value["code"]} for value in states]}, ensure_ascii=False))
    if not all(checks.values()):
        raise RuntimeError("真实 API 验收存在失败项")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""原地更新唯一的本机3000服务，保留原进程环境、工作目录和SQLite数据。"""
import argparse
import ctypes
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import tempfile
import time
import urllib.request


def process_arguments(pid):
    libc = ctypes.CDLL(None, use_errno=True)
    mib = (ctypes.c_int * 3)(1, 49, pid)  # macOS KERN_PROCARGS2，保留环境值中的空格。
    size = ctypes.c_size_t(256 << 10)
    buffer = ctypes.create_string_buffer(size.value)
    if libc.sysctl(mib, 3, buffer, ctypes.byref(size), None, 0):
        raise RuntimeError("无法可靠读取原服务环境，未重启")
    data = buffer.raw[:size.value]
    argc = int.from_bytes(data[:4], "little")
    position = data.index(b"\0", 4) + 1
    while position < len(data) and data[position] == 0:
        position += 1
    arguments = []
    for _ in range(argc):
        end = data.index(b"\0", position)
        arguments.append(data[position:end].decode())
        position = end + 1
    environment = {}
    for item in data[position:].split(b"\0"):
        if b"=" in item:
            key, value = item.split(b"=", 1)
            environment[key.decode()] = value.decode()
    if not arguments or not environment:
        raise RuntimeError("原服务环境读取不完整，未重启")
    return arguments, environment


def healthy():
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open("http://127.0.0.1:3000/api/status", timeout=2) as response:
            return json.load(response).get("success") is True
    except Exception:
        return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--archive-config", required=True)
    parser.add_argument("--evidence-dir", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    pids = subprocess.check_output(["lsof", "-t", "-iTCP:3000", "-sTCP:LISTEN"], text=True).split()
    if len(pids) != 1 or not healthy():
        raise RuntimeError("3000服务不是唯一健康实例，未重启")
    pid = int(pids[0])
    cwd = subprocess.check_output(["lsof", "-p", str(pid), "-a", "-d", "cwd", "-Fn"], text=True)
    if "n" + str(root) not in cwd.splitlines():
        raise RuntimeError("原服务工作目录不匹配，未重启")
    arguments, environment = process_arguments(pid)
    if not Path(args.binary).is_file() or not Path(arguments[0]).is_file():
        raise RuntimeError("更新或回滚程序缺失，未重启")
    state = Path(tempfile.mkdtemp(prefix="dpin-f1-deploy-"))
    fd = os.open(state / "original-environment.json", os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w") as output:
        json.dump({"cwd": str(root), "argv": arguments, "environment": environment}, output)
    database = root / "one-api.db"
    backup = state / "before-upgrade.db"
    with sqlite3.connect(f"file:{database}?mode=ro", uri=True) as source, sqlite3.connect(backup) as destination:
        source.backup(destination)
    backup.chmod(0o600)
    updated = dict(environment, DPIN_RESUME_ARCHIVE_CONFIG=args.archive_config)
    os.kill(pid, signal.SIGTERM)
    deadline = time.monotonic() + 20
    while subprocess.run(["lsof", "-t", "-iTCP:3000", "-sTCP:LISTEN"], capture_output=True).stdout:
        if time.monotonic() > deadline:
            raise RuntimeError("原服务未退出，未启动第二实例")
        time.sleep(0.2)
    log = open(state / "backend.log", "wb")
    os.chmod(state / "backend.log", 0o600)
    process = subprocess.Popen([args.binary] + arguments[1:], cwd=root, env=updated,
                               stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    deadline = time.monotonic() + 45
    while not healthy():
        if process.poll() is not None or time.monotonic() > deadline:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
            subprocess.Popen(arguments, cwd=root, env=environment, stdin=subprocess.DEVNULL,
                             stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            raise RuntimeError(f"更新启动失败，已使用原程序和配置回滚；新模型与回执保留，诊断：{state}")
        time.sleep(0.2)
    evidence = Path(args.evidence_dir)
    evidence.mkdir(parents=True, exist_ok=True, mode=0o700)
    receipt = {"old_pid": pid, "new_pid": process.pid, "healthy": True, "cwd_preserved": True,
               "original_environment_preserved": True, "private_backup_state": str(state),
               "database": str(database), "archive_config": args.archive_config}
    (evidence / f"deployment-{process.pid}.json").write_text(json.dumps(receipt, indent=2))
    print(json.dumps(receipt))


if __name__ == "__main__":
    main()

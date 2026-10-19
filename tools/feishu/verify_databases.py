#!/usr/bin/env python3
"""仅创建本批独立测试数据库；现有 D-PIN/MaxKB 服务和业务库不替换。"""
import argparse
import json
import getpass
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--mysql-binary", default="/opt/homebrew/opt/mysql@8.0/bin/mysqld")
    parser.add_argument("--mysql-client", default="/opt/homebrew/opt/mysql@8.0/bin/mysql")
    parser.add_argument("--mysql-lib-dir", action="append", default=[])
    parser.add_argument("--evidence-dir", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    evidence = Path(args.evidence_dir)
    evidence.mkdir(parents=True, exist_ok=True, mode=0o700)
    state = Path(tempfile.mkdtemp(prefix="dpin-f1-database-"))
    data = state / "mysql-data"
    data.mkdir(mode=0o700)
    env = dict(os.environ)
    if args.mysql_lib_dir:
        env["DYLD_LIBRARY_PATH"] = ":".join(args.mysql_lib_dir)
    socket = state / "mysql.sock"
    mysql_log = state / "mysql.log"
    init = subprocess.run([args.mysql_binary, "--no-defaults", "--initialize-insecure", f"--datadir={data}"],
                          env=env, capture_output=True)
    (state / "initialize.log").write_bytes(init.stdout + init.stderr)
    if init.returncode:
        raise RuntimeError(f"原生 MySQL 初始化失败，诊断保存在 {state}")
    output = open(mysql_log, "wb")
    process = subprocess.Popen([args.mysql_binary, "--no-defaults", f"--datadir={data}", f"--socket={socket}",
                                f"--pid-file={state / 'mysql.pid'}", "--skip-networking", "--mysqlx=0"],
                               env=env, stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT)
    pg_name = "dpin_f1_test_" + uuid.uuid4().hex[:12]
    pg_created = False
    try:
        deadline = time.monotonic() + 45
        while not socket.exists():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError(f"原生 MySQL 启动失败，诊断保存在 {state}")
            time.sleep(0.2)
        mysql_args = [args.mysql_client, "--no-defaults", "-uroot", f"--socket={socket}"]
        subprocess.run(mysql_args + ["-e", "CREATE DATABASE dpin_f1_test CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"], env=env, check=True, capture_output=True)
        subprocess.run(["createdb", pg_name], check=True, capture_output=True)
        pg_created = True
        test_env = dict(env, TEST_MYSQL_DSN=f"root@unix({socket})/dpin_f1_test?charset=utf8mb4&parseTime=True&loc=Local",
                        TEST_POSTGRES_DSN=f"postgres://{getpass.getuser()}@localhost:5432/{pg_name}?sslmode=disable")
        receipt = {"mysql_private_state": str(state), "postgres_test_database": pg_name,
                   "business_services_replaced": False, "postgres_created": True}
        (evidence / "database-environment.json").write_text(json.dumps(receipt, indent=2))
        commands = [(["go", "test", "./service", "-run", "TestBelloBuddyResumeArchiveDatabaseMatrix", "-count=1", "-v"], "database-matrix.log"),
                    (["go", "test", "./router", "-run", "^TestBelloBuddy", "-count=1"], "existing-bellobuddy-regression.log")]
        for command, filename in commands:
            result = subprocess.run(command, cwd=root, env=test_env, capture_output=True)
            (evidence / filename).write_bytes(result.stdout + result.stderr)
            print(json.dumps({"command": command, "exit_code": result.returncode, "log": filename}), flush=True)
            if result.returncode:
                raise RuntimeError(f"测试未通过：{filename}")
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        output.close()
        if pg_created:
            subprocess.run(["dropdb", pg_name], check=True, capture_output=True)
        # 保留本批原生 MySQL 数据与日志证据，不清理此前任何文件。


if __name__ == "__main__":
    main()

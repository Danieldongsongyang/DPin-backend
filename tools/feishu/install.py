#!/usr/bin/env python3
"""固定官方 1.0.84 原生 CLI，两种 Mac 架构；不安装 npm、不自动升级。"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import tarfile
import urllib.request

VERSION = "1.0.84"
ARCHIVES = {
    "arm64": "bc56a15fce5b6fdb065702ac03ef4d051bc71ea511b75f316461008e4a359ba5",
    "amd64": "d9958d1d877e235eba7395e10c443e70849a838806137760170d404786f5a2b4",
}


def exclusive_write(path, data, mode):
    if path.exists():
        if path.read_bytes() != data:
            raise RuntimeError(f"已有文件不同，保留原文件：{path.name}")
        return
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, mode)
    with os.fdopen(fd, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--deployment-root", required=True)
    args = parser.parse_args()
    root = Path(args.deployment_root)
    if not root.is_absolute():
        raise RuntimeError("部署路径必须绝对路径")
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    if root.resolve() != root or root.stat().st_mode & 0o077:
        raise RuntimeError("部署根目录必须为非符号链接私有目录")
    manifests = {}
    for arch, checksum in ARCHIVES.items():
        directory = root / "tools" / "feishu" / f"darwin-{arch}"
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        archive_name = f"lark-cli-{VERSION}-darwin-{arch}.tar.gz"
        url = f"https://github.com/larksuite/cli/releases/download/v{VERSION}/{archive_name}"
        archive_path = directory / archive_name
        if archive_path.exists():
            content = archive_path.read_bytes()
        else:
            with urllib.request.urlopen(url, timeout=60) as response:
                content = response.read(150 << 20)
        if hashlib.sha256(content).hexdigest() != checksum:
            raise RuntimeError(f"官方归档校验失败：{arch}")
        exclusive_write(archive_path, content, 0o600)
        with tarfile.open(fileobj=io.BytesIO(content), mode="r:gz") as archive:
            members = {Path(member.name).name: member for member in archive.getmembers() if member.isfile()}
            if "lark-cli" not in members or "LICENSE" not in members:
                raise RuntimeError("官方归档缺少可执行文件或许可证")
            binary = archive.extractfile(members["lark-cli"]).read()
            license_data = archive.extractfile(members["LICENSE"]).read()
        exclusive_write(directory / "lark-cli", binary, 0o755)
        exclusive_write(directory / "LICENSE", license_data, 0o644)
        manifest = {"version": VERSION, "os": "darwin", "arch": arch,
                    "source": url, "archive_sha256": checksum,
                    "binary_sha256": hashlib.sha256(binary).hexdigest(),
                    "cli_source_commit": "2d787fe5d6f35e279f39fe630328ad3689bf2aff"}
        exclusive_write(directory / "manifest.json", json.dumps(manifest, indent=2).encode(), 0o644)
        manifests[arch] = manifest
        print(json.dumps({"arch": arch, "version": VERSION, "verified_archive": True}))
    arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
    if platform.system() != "Darwin" or arch is None:
        raise RuntimeError("当前运行配置仅支持 macOS ARM64／Intel")
    config_dir = root / "config"
    config_dir.mkdir(exist_ok=True, mode=0o700)
    data_dir = root / "data" / "resume-archive"
    data_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    config = {"enabled": True, "cli_path": str(root / "tools" / "feishu" / f"darwin-{arch}" / "lark-cli"),
              "cli_sha256": manifests[arch]["binary_sha256"], "app_id": "cli_aa2b9b27ecf99cb4",
              "secret_file": str(config_dir / "feishu-secret.json"), "data_dir": str(data_dir),
              "max_storage_bytes": 2 << 30,
              "target": {"version": "haiyi-resume-f1-v1", "base": "MouGb9ScBabGdgssj3mc9TnanNf",
                         "job_table": "tbldVXMWMP1lxTq2", "talent_table": "tblDtr0KJWMmONlv",
                         "fields": {"resume": "fldQwqTzu6", "job_link": "fldJbd8fxM", "name": "fldw1pQ6Se",
                                    "owner": "fldT7pfICk", "account": "fldiJTXsBn", "account_id": "fldQk9HSEe",
                                    "platform": "fldwR9XcxF", "candidate_id": "fldPYJtbaE", "archive_key": "fldkzShpBE",
                                    "downloaded_at": "fld9Wy2B0I", "archived_at": "fld6kDWIku",
                                    "job_name": "fldiDfXxNj", "hr": "fldSfVJTNz"}}}
    path = config_dir / "resume-archive.json"
    if not path.exists():
        exclusive_write(path, json.dumps(config, ensure_ascii=False, indent=2).encode(), 0o600)
    print(json.dumps({"configuration": str(path), "secret_configured": (config_dir / "feishu-secret.json").exists()}))


if __name__ == "__main__":
    main()

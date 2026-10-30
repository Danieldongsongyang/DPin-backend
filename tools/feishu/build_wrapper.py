#!/usr/bin/env python3
"""从固定 Go module 构建 macOS/Windows 凭据封装和校验清单。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

from install import (
    ARCHIVES, SERVICE_SID, VERSION, atomic_config_write, checked_path, exclusive_write,
    host_target, private_directory, private_file, selected_targets,
)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--deployment-root", required=True)
    parser.add_argument("--target", action="append", choices=[*ARCHIVES, "all"])
    parser.add_argument("--no-config-update", action="store_true", help="只构建程序，不更新运行配置")
    parser.add_argument("--service-sid", default=SERVICE_SID)
    parser.add_argument("--go", default="go", help="Go 可执行文件路径")
    args = parser.parse_args()
    targets = selected_targets(args.target)
    native = None if args.no_config_update else host_target()
    if native is not None and native not in targets:
        raise RuntimeError("更新运行配置时必须构建本机目标，或指定 --no-config-update")
    root = private_directory(Path(args.deployment_root), args.service_sid)
    source = Path(__file__).resolve().parent / "cli-wrapper"
    digest = hashlib.sha256()
    for name in ("go.mod", "go.sum", "main.go"):
        digest.update(name.encode())
        digest.update((source / name).read_bytes())
    source_hash = digest.hexdigest()
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="0")
    subprocess.run([args.go, "mod", "verify"], cwd=source, env=env, check=True)
    binaries = {}
    for target in targets:
        system, arch = target.split("-", 1)
        directory = private_directory(root / "tools" / "feishu" / target, args.service_sid)
        suffix = ".exe" if system == "windows" else ""
        binary = directory / f"dpin-feishu-cli-{VERSION}-{source_hash[:12]}{suffix}"
        manifest_path = directory / f"wrapper-manifest-{source_hash[:12]}.json"
        checked_path(binary)
        checked_path(manifest_path)
        if binary.exists() and not manifest_path.exists():
            raise RuntimeError(f"已有程序缺少来源清单，保留原文件：{binary.name}")
        if not binary.exists():
            descriptor, temporary_name = tempfile.mkstemp(prefix=binary.name + ".", suffix=".new", dir=directory)
            os.close(descriptor)
            temporary = Path(temporary_name)
            try:
                subprocess.run(
                    [args.go, "build", "-trimpath", "-buildvcs=false", "-ldflags",
                     f"-s -w -X github.com/larksuite/cli/internal/build.Version={VERSION}", "-o", str(temporary), "."],
                    cwd=source, env=dict(env, GOOS=system, GOARCH=arch), check=True,
                )
                private_file(temporary, 0o755, args.service_sid)
                os.replace(temporary, binary)
            finally:
                temporary.unlink(missing_ok=True)
        private_file(binary, 0o755, args.service_sid)
        checksum = hashlib.sha256(binary.read_bytes()).hexdigest()
        manifest = {"version": VERSION, "wrapper_source_sha256": source_hash, "binary_sha256": checksum,
                    "arch": arch, "os": system, "upstream": f"github.com/larksuite/cli@v{VERSION}",
                    "credential_mode": "application-client-credentials", "personal_login_state": False}
        # Existing manifests are immutable: corrupted binaries must not become trusted config.
        exclusive_write(manifest_path, json.dumps(manifest, indent=2).encode(), 0o644, args.service_sid)
        binaries[target] = (binary, checksum)
        print(json.dumps({"target": target, "built": True, "source_sha256": source_hash}))
    if native is None:
        return
    private_directory(root / "config", args.service_sid)
    config_path = root / "config" / "resume-archive.json"
    private_file(config_path, 0o600, args.service_sid)
    config = json.loads(config_path.read_text())
    config["cli_path"], config["cli_sha256"] = str(binaries[native][0]), binaries[native][1]
    atomic_config_write(config_path, config, args.service_sid)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""从固定 Go module 构建两种 macOS 凭据封装，生成不可变文件名和校验清单。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--deployment-root", required=True)
    args = parser.parse_args()
    root = Path(args.deployment_root).resolve()
    source = Path(__file__).resolve().parent / "cli-wrapper"
    digest = hashlib.sha256()
    for name in ("go.mod", "go.sum", "main.go"):
        digest.update(name.encode())
        digest.update((source / name).read_bytes())
    source_hash = digest.hexdigest()
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="0", GOOS="darwin")
    subprocess.run(["go", "mod", "verify"], cwd=source, env=env, check=True)
    binaries = {}
    for arch in ("arm64", "amd64"):
        directory = root / "tools" / "feishu" / f"darwin-{arch}"
        binary = directory / f"dpin-feishu-cli-1.0.84-{source_hash[:12]}"
        if not binary.exists():
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags",
                            "-s -w -X github.com/larksuite/cli/internal/build.Version=1.0.84", "-o", str(binary), "."],
                           cwd=source, env=dict(env, GOARCH=arch), check=True)
            binary.chmod(0o755)
        checksum = hashlib.sha256(binary.read_bytes()).hexdigest()
        manifest = {"version": "1.0.84", "wrapper_source_sha256": source_hash, "binary_sha256": checksum,
                    "arch": arch, "os": "darwin", "upstream": "github.com/larksuite/cli@v1.0.84",
                    "credential_mode": "application-client-credentials", "personal_login_state": False}
        path = directory / f"wrapper-manifest-{source_hash[:12]}.json"
        if not path.exists():
            path.write_text(json.dumps(manifest, indent=2))
        binaries[arch] = (binary, checksum)
        print(json.dumps({"arch": arch, "built": True, "source_sha256": source_hash}))
    arch = {"arm64": "arm64", "x86_64": "amd64"}.get(platform.machine())
    if arch is None or platform.system() != "Darwin":
        raise RuntimeError("当前运行配置仅支持 macOS")
    config_path = root / "config" / "resume-archive.json"
    config = json.loads(config_path.read_text())
    config["cli_path"], config["cli_sha256"] = str(binaries[arch][0]), binaries[arch][1]
    temporary = config_path.with_suffix(".new")
    fd = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w") as output:
        json.dump(config, output, ensure_ascii=False, indent=2)
        output.flush()
        os.fsync(output.fileno())
    os.replace(temporary, config_path)


if __name__ == "__main__":
    main()

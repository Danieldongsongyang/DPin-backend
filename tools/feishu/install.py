#!/usr/bin/env python3
"""固定官方 1.0.84 原生 CLI，支持 macOS/Windows；不自动升级。"""
import argparse
import ctypes
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import stat
import subprocess
import tarfile
import tempfile
import urllib.request
import zipfile

VERSION = "1.0.84"
ARCHIVES = {
    "darwin-arm64": "bc56a15fce5b6fdb065702ac03ef4d051bc71ea511b75f316461008e4a359ba5",
    "darwin-amd64": "d9958d1d877e235eba7395e10c443e70849a838806137760170d404786f5a2b4",
    "windows-arm64": "a97d2150d2d4c36be146730ce7119c261c5aacb3c2d55839b613909cf62dcbb3",
    "windows-amd64": "458e1fe338c3150f2d9ae0cf5781d59f8dc6e416e63f8d4668cb76de1ae6ec75",
}
SERVICE_SID = "S-1-5-19"


def host_target():
    system = {"Darwin": "darwin", "Windows": "windows"}.get(platform.system())
    arch = {"arm64": "arm64", "aarch64": "arm64", "amd64": "amd64", "x86_64": "amd64"}.get(
        platform.machine().lower()
    )
    if system is None or arch is None:
        raise RuntimeError("运行配置仅支持 macOS／Windows ARM64 或 AMD64")
    return f"{system}-{arch}"


def selected_targets(requested):
    if not requested:
        system = host_target().split("-", 1)[0]
        return [target for target in ARCHIVES if target.startswith(system + "-")]
    return list(ARCHIVES) if "all" in requested else list(dict.fromkeys(requested))


def checked_path(path):
    path = Path(path)
    if not path.is_absolute() or path != Path(os.path.abspath(path)):
        raise RuntimeError("部署路径必须是无相对路径片段的绝对路径")
    for component in [*reversed(path.parents), path]:
        if not os.path.lexists(component):
            continue
        info = component.lstat()
        if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
            raise RuntimeError(f"拒绝符号链接或 Windows 重解析点：{component}")
    return path


def windows_private_acl(path, service_sid=SERVICE_SID, writable=False):
    # Paths are JSON stdin data; never interpolate them into executable PowerShell.
    if not ctypes.windll.shell32.IsUserAnAdmin():
        raise RuntimeError("Windows 服务部署需要在管理员终端运行，以设置私有文件权限")
    if not re.fullmatch(r"S-\d+(?:-\d+)+", service_sid):
        raise RuntimeError("无效的 Windows 服务 SID")
    script = r"""
$ErrorActionPreference = 'Stop'
$request = [Console]::In.ReadToEnd() | ConvertFrom-Json
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Feishu service installation requires an administrator.'
}
$attributes = [IO.File]::GetAttributes($request.path)
if (($attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Reparse point refused.' }
$isDirectory = ($attributes -band [IO.FileAttributes]::Directory) -ne 0
$admins = New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')
if ($isDirectory) {
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $inheritance = [Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
} else {
    $acl = New-Object Security.AccessControl.FileSecurity
    $inheritance = [Security.AccessControl.InheritanceFlags]::None
}
$acl.SetOwner($admins)
$acl.SetAccessRuleProtection($true, $false)
$grants = @{'S-1-5-32-544'='FullControl'; 'S-1-5-18'='FullControl'}
if (-not $grants.ContainsKey($request.service_sid)) {
    $grants[$request.service_sid] = if ($request.writable) { 'Modify' } else { 'ReadAndExecute' }
}
foreach ($sid in $grants.Keys) {
    $rule = New-Object Security.AccessControl.FileSystemAccessRule(
        (New-Object Security.Principal.SecurityIdentifier($sid)),
        [Security.AccessControl.FileSystemRights]$grants[$sid], $inheritance,
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow)
    $acl.AddAccessRule($rule)
}
if ($isDirectory) {
    [IO.Directory]::SetAccessControl($request.path, $acl)
    $verified = [IO.Directory]::GetAccessControl($request.path)
} else {
    [IO.File]::SetAccessControl($request.path, $acl)
    $verified = [IO.File]::GetAccessControl($request.path)
}
if (-not $verified.AreAccessRulesProtected -or $verified.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $admins.Value) {
    throw 'Private ACL verification failed.'
}
foreach ($rule in $verified.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
    if ($rule.AccessControlType -eq 'Allow' -and -not $grants.ContainsKey($rule.IdentityReference.Value)) {
        throw 'Unexpected access grant after ACL update.'
    }
}
"""
    executable = Path(os.environ.get("SystemRoot", r"C:\Windows")) / "System32/WindowsPowerShell/v1.0/powershell.exe"
    result = subprocess.run(
        [str(executable), "-NoProfile", "-NonInteractive", "-Command", script],
        input=json.dumps({"path": str(path), "service_sid": service_sid, "writable": writable}),
        text=True, encoding="utf-8", errors="replace", capture_output=True,
        creationflags=subprocess.CREATE_NO_WINDOW,
    )
    if result.returncode:
        raise RuntimeError(f"Windows 私有 ACL 设置失败：{path}")


def private_directory(path, service_sid=SERVICE_SID, writable=False):
    path = checked_path(path)
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if not path.is_dir():
        raise RuntimeError(f"不是部署目录：{path}")
    if os.name == "nt":
        windows_private_acl(path, service_sid, writable)
    elif path.stat().st_mode & 0o077:
        raise RuntimeError(f"部署目录必须为私有目录：{path}")
    return path


def private_file(path, mode, service_sid=SERVICE_SID):
    checked_path(path)
    if not path.is_file():
        raise RuntimeError(f"不是普通文件：{path}")
    if os.name == "nt":
        windows_private_acl(path, service_sid)
    else:
        path.chmod(mode)


def exclusive_write(path, data, mode, service_sid=SERVICE_SID):
    checked_path(path)
    if path.exists():
        if path.read_bytes() != data:
            raise RuntimeError(f"已有文件不同，保留原文件：{path.name}")
        private_file(path, mode, service_sid)
        return
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, mode)
    with os.fdopen(fd, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())
    private_file(path, mode, service_sid)


def atomic_config_write(path, config, service_sid=SERVICE_SID):
    checked_path(path)
    descriptor, name = tempfile.mkstemp(prefix=path.name + ".", suffix=".new", dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as output:
            json.dump(config, output, ensure_ascii=False, indent=2)
            output.flush()
            os.fsync(output.fileno())
        private_file(temporary, 0o600, service_sid)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def archive_files(content, target):
    binary_name = "lark-cli.exe" if target.startswith("windows-") else "lark-cli"
    if target.startswith("windows-"):
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            members = {name: [info for info in archive.infolist() if Path(info.filename).name == name]
                       for name in (binary_name, "LICENSE")}
            if any(len(items) != 1 or items[0].is_dir() for items in members.values()):
                raise RuntimeError("官方归档缺少唯一的可执行文件或许可证")
            return tuple(archive.read(members[name][0]) for name in (binary_name, "LICENSE"))
    with tarfile.open(fileobj=io.BytesIO(content), mode="r:gz") as archive:
        members = {name: [member for member in archive.getmembers() if Path(member.name).name == name]
                   for name in (binary_name, "LICENSE")}
        if any(len(items) != 1 or not items[0].isfile() for items in members.values()):
            raise RuntimeError("官方归档缺少唯一的可执行文件或许可证")
        return tuple(archive.extractfile(members[name][0]).read() for name in (binary_name, "LICENSE"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--deployment-root", required=True)
    parser.add_argument("--target", action="append", choices=[*ARCHIVES, "all"])
    parser.add_argument("--no-config-update", action="store_true", help="只准备程序，不创建运行配置")
    parser.add_argument("--service-sid", default=SERVICE_SID, help="Windows 服务账户 SID（默认 LocalService）")
    args = parser.parse_args()
    targets = selected_targets(args.target)
    native = None if args.no_config_update else host_target()
    if native is not None and native not in targets:
        raise RuntimeError("创建运行配置时必须包含本机目标，或指定 --no-config-update")
    root = private_directory(Path(args.deployment_root), args.service_sid)
    manifests = {}
    for target in targets:
        system, arch = target.split("-", 1)
        checksum = ARCHIVES[target]
        directory = private_directory(root / "tools" / "feishu" / target, args.service_sid)
        extension = "zip" if system == "windows" else "tar.gz"
        archive_name = f"lark-cli-{VERSION}-{target}.{extension}"
        url = f"https://github.com/larksuite/cli/releases/download/v{VERSION}/{archive_name}"
        archive_path = directory / archive_name
        if archive_path.exists():
            content = archive_path.read_bytes()
        else:
            with urllib.request.urlopen(url, timeout=60) as response:
                content = response.read(150 << 20)
        if hashlib.sha256(content).hexdigest() != checksum:
            raise RuntimeError(f"官方归档校验失败：{arch}")
        exclusive_write(archive_path, content, 0o600, args.service_sid)
        binary, license_data = archive_files(content, target)
        binary_name = "lark-cli.exe" if system == "windows" else "lark-cli"
        exclusive_write(directory / binary_name, binary, 0o755, args.service_sid)
        exclusive_write(directory / "LICENSE", license_data, 0o644, args.service_sid)
        manifest = {"version": VERSION, "os": system, "arch": arch,
                    "source": url, "archive_sha256": checksum,
                    "binary_sha256": hashlib.sha256(binary).hexdigest(),
                    "cli_source_commit": "2d787fe5d6f35e279f39fe630328ad3689bf2aff"}
        exclusive_write(directory / "manifest.json", json.dumps(manifest, indent=2).encode(), 0o644, args.service_sid)
        manifests[target] = manifest
        print(json.dumps({"target": target, "version": VERSION, "verified_archive": True}))
    if native is None:
        return
    config_dir = private_directory(root / "config", args.service_sid)
    data_dir = private_directory(root / "data" / "resume-archive", args.service_sid, writable=True)
    for name in ("files", "inputs", "receipts", "downloads", "cli-home"):
        private_directory(data_dir / name, args.service_sid, writable=True)
    binary_name = "lark-cli.exe" if native.startswith("windows-") else "lark-cli"
    config = {"enabled": False, "cli_path": str(root / "tools" / "feishu" / native / binary_name),
              "cli_sha256": manifests[native]["binary_sha256"], "app_id": "cli_aa2b9b27ecf99cb4",
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
        exclusive_write(path, json.dumps(config, ensure_ascii=False, indent=2).encode(), 0o600, args.service_sid)
    else:
        private_file(path, 0o600, args.service_sid)
    secret = config_dir / "feishu-secret.json"
    if secret.exists():
        private_file(secret, 0o600, args.service_sid)
    print(json.dumps({"configuration": str(path), "secret_configured": (config_dir / "feishu-secret.json").exists()}))


if __name__ == "__main__":
    main()

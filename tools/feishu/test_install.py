"""Deployment artifact, configuration and native Windows ACL regressions."""
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile

import install


class ArchiveTests(unittest.TestCase):
    def test_native_target_selection(self):
        for system, machine, target in [
            ("Darwin", "arm64", "darwin-arm64"),
            ("Darwin", "x86_64", "darwin-amd64"),
            ("Windows", "AMD64", "windows-amd64"),
            ("Windows", "ARM64", "windows-arm64"),
        ]:
            with self.subTest(target=target), mock.patch("install.platform.system", return_value=system), \
                    mock.patch("install.platform.machine", return_value=machine):
                self.assertEqual(target, install.host_target())
                self.assertIn(target, install.selected_targets(None))
        with mock.patch("install.platform.system", return_value="Linux"):
            with self.assertRaises(RuntimeError):
                install.host_target()

    def test_windows_and_mac_archives(self):
        windows = io.BytesIO()
        with zipfile.ZipFile(windows, "w") as archive:
            archive.writestr("release/lark-cli.exe", b"native-windows")
            archive.writestr("release/LICENSE", b"license")
        self.assertEqual((b"native-windows", b"license"), install.archive_files(windows.getvalue(), "windows-amd64"))
        mac = io.BytesIO()
        with tarfile.open(fileobj=mac, mode="w:gz") as archive:
            for name, data in [("lark-cli", b"native-mac"), ("LICENSE", b"license")]:
                item = tarfile.TarInfo(name)
                item.size = len(data)
                archive.addfile(item, io.BytesIO(data))
        self.assertEqual((b"native-mac", b"license"), install.archive_files(mac.getvalue(), "darwin-arm64"))

    def test_duplicate_or_missing_archive_program_is_rejected(self):
        for names in [["LICENSE"], ["a/lark-cli.exe", "b/lark-cli.exe", "LICENSE"]]:
            with self.subTest(names=names):
                stream = io.BytesIO()
                with zipfile.ZipFile(stream, "w") as archive:
                    for name in names:
                        archive.writestr(name, b"data")
                with self.assertRaises(RuntimeError):
                    install.archive_files(stream.getvalue(), "windows-amd64")


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="dpin-feishu-tooling-")
        self.addCleanup(self.temporary.cleanup)
        self.root = install.private_directory(Path(self.temporary.name))

    def test_immutable_artifact_cannot_be_replaced(self):
        target = self.root / "cli-artifact"
        install.exclusive_write(target, b"original", 0o600)
        install.exclusive_write(target, b"original", 0o600)
        with self.assertRaises(RuntimeError):
            install.exclusive_write(target, b"changed", 0o600)
        self.assertEqual(b"original", target.read_bytes())

    def test_atomic_update_preserves_disabled_configuration_and_cleans_failed_temporary_file(self):
        config = {"enabled": False, "job_sync": {"enabled": False}, "target": {"base": "existing-base"}}
        path = self.root / "resume-archive.json"
        install.atomic_config_write(path, config)
        changed = dict(config, cli_path="native-program", cli_sha256="verified-hash")
        with mock.patch("install.os.replace", side_effect=OSError("synthetic failure")):
            with self.assertRaises(OSError):
                install.atomic_config_write(path, changed)
        self.assertEqual(config, json.loads(path.read_text(encoding="utf-8")))
        self.assertFalse(list(self.root.glob("*.new")))
        install.atomic_config_write(path, changed)
        self.assertEqual(changed, json.loads(path.read_text(encoding="utf-8")))

    @unittest.skipUnless(os.name == "nt", "requires native Windows ACLs")
    def test_windows_service_grants_are_private_and_data_is_writable(self):
        config_dir = install.private_directory(self.root / "config")
        data_dir = install.private_directory(self.root / "data", writable=True)
        secret = config_dir / "secret.json"
        install.exclusive_write(secret, b'{"app_secret":"synthetic-test-value"}', 0o600)
        script = r"""
$paths = [Console]::In.ReadToEnd() | ConvertFrom-Json
foreach ($path in $paths) {
    $acl = if ([IO.Directory]::Exists($path)) { [IO.Directory]::GetAccessControl($path) } else { [IO.File]::GetAccessControl($path) }
    [pscustomobject]@{owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value;
      protected=$acl.AreAccessRulesProtected;
      rules=@($acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]) | ForEach-Object {
        [pscustomobject]@{sid=$_.IdentityReference.Value;rights=[int]$_.FileSystemRights}
      })} | ConvertTo-Json -Depth 4 -Compress
}
"""
        executable = Path(os.environ["SystemRoot"]) / "System32/WindowsPowerShell/v1.0/powershell.exe"
        output = subprocess.run([str(executable), "-NoProfile", "-NonInteractive", "-Command", script],
                                input=json.dumps([str(config_dir), str(data_dir), str(secret)]),
                                text=True, capture_output=True, check=True,
                                creationflags=subprocess.CREATE_NO_WINDOW)
        acls = [json.loads(line) for line in output.stdout.splitlines() if line.strip()]
        for acl in acls:
            self.assertEqual("S-1-5-32-544", acl["owner"])
            self.assertTrue(acl["protected"])
            self.assertEqual({"S-1-5-32-544", "S-1-5-18", "S-1-5-19"}, {rule["sid"] for rule in acl["rules"]})
        service_rights = [next(rule["rights"] for rule in acl["rules"] if rule["sid"] == "S-1-5-19")
                          for acl in acls]
        self.assertFalse(service_rights[0] & 2)  # WriteData / CreateFiles
        self.assertTrue(service_rights[1] & 2)
        self.assertFalse(service_rights[2] & 2)

    @unittest.skipUnless(os.name == "nt", "requires native Windows junctions")
    def test_windows_junction_component_is_rejected(self):
        target = self.root / "real"
        target.mkdir()
        junction = self.root / "junction"
        executable = Path(os.environ["SystemRoot"]) / "System32/WindowsPowerShell/v1.0/powershell.exe"
        script = "$p=[Console]::In.ReadToEnd()|ConvertFrom-Json; New-Item -ItemType Junction -Path $p.link -Target $p.target|Out-Null"
        subprocess.run([str(executable), "-NoProfile", "-NonInteractive", "-Command", script],
                       input=json.dumps({"link": str(junction), "target": str(target)}),
                       text=True, check=True, capture_output=True, creationflags=subprocess.CREATE_NO_WINDOW)
        try:
            with self.assertRaises(RuntimeError):
                install.checked_path(junction / "child")
        finally:
            junction.rmdir()


if __name__ == "__main__":
    unittest.main()

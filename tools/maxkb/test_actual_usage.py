import os
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
import tempfile
import shutil
import hashlib
import subprocess
import sys

spec = importlib.util.spec_from_file_location(
    "dpin_actual_usage", Path(__file__).with_name("dpin_actual_usage.py")
)
usage_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(usage_module)


class ActualUsageTests(unittest.TestCase):
    def test_actual_metadata_is_used_including_explicit_zero(self):
        cases = [
            (
                {"input_tokens": 809, "output_tokens": 22, "total_tokens": 831},
                {},
                (809, 22),
            ),
            (
                None,
                {
                    "token_usage": {
                        "prompt_tokens": 744,
                        "completion_tokens": 98,
                        "total_tokens": 842,
                    }
                },
                (744, 98),
            ),
            ({"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}, {}, (0, 0)),
            (
                {"input_tokens": 1, "output_tokens": 2},
                {
                    "token_usage": {
                        "prompt_tokens": 1,
                        "completion_tokens": 2,
                        "total_tokens": 3,
                    }
                },
                (1, 2),
            ),
        ]
        for metadata, response, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(
                    usage_module.actual_token_usage(
                        SimpleNamespace(
                            usage_metadata=metadata, response_metadata=response
                        )
                    ),
                    expected,
                )

    def test_missing_invalid_and_conflicting_metadata_never_becomes_an_estimate(self):
        cases = [
            ({}, {}),
            ({"input_tokens": 4}, {}),
            ({"input_tokens": -1, "output_tokens": 1}, {}),
            ({"input_tokens": True, "output_tokens": 1}, {}),
            ({"input_tokens": 1.5, "output_tokens": 1}, {}),
            ({"input_tokens": "1", "output_tokens": 1}, {}),
            ({"input_tokens": 2**31 - 1, "output_tokens": 1}, {}),
            ({"input_tokens": 1, "output_tokens": 1, "total_tokens": 3}, {}),
            (
                {"input_tokens": 1, "output_tokens": 2},
                {"token_usage": {"prompt_tokens": 4, "completion_tokens": 5}},
            ),
            (
                {
                    "input_tokens": 1,
                    "output_tokens": 2,
                    "prompt_tokens": 1,
                    "completion_tokens": 3,
                },
                {},
            ),
        ]
        for metadata, response in cases:
            with self.subTest(metadata=metadata):
                with self.assertRaisesRegex(
                    usage_module.ActualUsageUnavailable,
                    "^MAXKB_ACTUAL_USAGE_UNAVAILABLE$",
                ):
                    usage_module.actual_token_usage(
                        SimpleNamespace(
                            usage_metadata=metadata, response_metadata=response
                        )
                    )


class ContractToolTests(unittest.TestCase):
    def test_v2_apply_is_idempotent_and_inconsistent_tree_is_never_written(self):
        configured_root = os.environ.get("P7A_MAXKB_ROOT")
        if not configured_root:
            self.skipTest("本地集成契约核对需显式 P7A_MAXKB_ROOT；默认用量单测仍执行")
        source = Path(configured_root).resolve()
        self.assertTrue(source.is_dir(), "显式 MaxKB 工程路径不存在")
        spec = importlib.util.spec_from_file_location(
            "dpin_contract_checker", Path(__file__).with_name("apply-strict-usage.py")
        )
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        with tempfile.TemporaryDirectory(prefix="p7a-tool-test-") as directory:
            root = Path(directory)
            for name in checker.REQUIRED:
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(source / name, target)

            def hashes():
                return {
                    str(path.relative_to(root)): hashlib.sha256(
                        path.read_bytes()
                    ).hexdigest()
                    for path in root.rglob("*")
                    if path.is_file()
                }

            original = hashes()
            for _ in range(2):
                result = subprocess.run(
                    [
                        sys.executable,
                        str(Path(__file__).with_name("apply-strict-usage.py")),
                        "--maxkb-root",
                        str(root),
                        "--apply",
                    ],
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(hashes(), original)
            helper = root / "apps/common/handle/dpin_actual_usage.py"
            helper.write_text(
                helper.read_text().replace(
                    "class RequestActualUsage:", "class OutdatedUsage:"
                )
            )
            inconsistent = hashes()
            result = subprocess.run(
                [
                    sys.executable,
                    str(Path(__file__).with_name("apply-strict-usage.py")),
                    "--maxkb-root",
                    str(root),
                    "--apply",
                ],
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(hashes(), inconsistent)


if __name__ == "__main__":
    unittest.main()

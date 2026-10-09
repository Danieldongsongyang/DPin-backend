#!/usr/bin/env python3
"""核对已部署的 MaxKB 请求级实际用量契约 v2，不覆盖现行源码。

旧 v1 单回复补丁已停用。--apply 兼容旧命令，但只在完整 v2 已存在时幂等成功。
未知、不完整或旧版本必须在 MaxKB 源码工程修复，不能回装旧 helper、关闭知识或 ROUND。
"""

import argparse
import ast
from pathlib import Path

REQUIRED = {
    "apps/common/handle/dpin_actual_usage.py": [
        "class RequestActualUsage:",
        "def actual_token_usage(",
        "def actual_cached_tokens(",
        "def cached_tokens(",
        "def invoke(",
        "def finalize(",
    ],
    "apps/application/chat_pipeline/pipeline_manage.py": [
        "RequestActualUsage()",
        "self.actual_usage",
    ],
    "apps/common/handle/impl/response/openai_to_response.py": [
        "# DPin actual-usage contract v2",
        "usage.finalize()",
        "usage.cached_tokens()",
        "cache_usage_source",
        "data['usage']['calls'] = usage.details()",
    ],
    "apps/application/chat_pipeline/step/chat_step/impl/base_chat_step.py": [
        "actual_usage.invoke('reply'",
        "extract_long_term_memory_for_request(",
        "manage.context['actual_usage_calls'] = usage.details()",
    ],
    "apps/application/chat_pipeline/step/reset_problem_step/impl/base_reset_problem_step.py": [
        "usage.invoke('problem_optimization'",
        "raise ActualUsageUnavailable()",
    ],
    "apps/application/long_term_memory/__init__.py": [
        "actual_usage.invoke('long_term_memory'",
        "current_conversation",
    ],
    "apps/chat/serializers/chat.py": [
        "params['actual_memory_config'] = request_memory_config",
        "MAXKB_ACTUAL_USAGE_UNSUPPORTED",
    ],
    "apps/application/chat_pipeline/step/search_dataset_step/impl/base_search_dataset_step.py": [
        "model.provider != 'model_local_provider'",
        "'source': 'local_embedding'",
        "'token_usage': 'not_applicable'",
    ],
}


def prepare_changes(root):
    root = Path(root).resolve()
    for name, markers in REQUIRED.items():
        path = root / name
        if not path.is_file():
            raise ValueError("现行 v2 契约缺少必要文件，未写入任何文件")
        source = path.read_text()
        ast.parse(source, filename=str(path))
        if any(marker not in source for marker in markers):
            raise ValueError("现行 v2 契约不完整或版本不一致，未写入任何文件")
    serializer = (root / "apps/chat/serializers/chat.py").read_text()
    if "instance.get('stream'), chat_info.knowledge_id_list" in serializer:
        raise ValueError("知识检索仍被旧契约阻断，未写入任何文件")
    return {}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--maxkb-root", required=True)
    parser.add_argument(
        "--apply", action="store_true", help="兼容旧命令；完整v2已部署时不重复写入"
    )
    args = parser.parse_args()
    prepare_changes(args.maxkb_root)
    print("现行请求级实际用量 v2 与缓存契约源码静态核对通过；未写入文件，运行行为需独立验收")


if __name__ == "__main__":
    main()

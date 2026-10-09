#!/usr/bin/env python3
"""使用 MaxKB 工程自己的 Python 环境验证真实用量与同会话续接。

只发送两条固定测试消息；凭据仅在内存中读取，不打印回复或密钥，不改应用配置。
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import sys
from types import SimpleNamespace
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--maxkb-root", required=True)
    parser.add_argument("--application-id", required=True)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--env-file", help="此本地部署使用的环境配置文件；内容不会输出")
    args = parser.parse_args()
    from urllib.parse import urlsplit

    base = urlsplit(args.base_url)
    if (
        base.scheme != "http"
        or base.hostname not in ("localhost", "127.0.0.1", "::1")
        or base.username
        or base.password
        or base.path not in ("", "/")
        or base.query
        or base.fragment
    ):
        raise ValueError("验证器只允许本机 HTTP Origin")
    application_id = str(uuid.UUID(args.application_id))
    root = Path(args.maxkb_root).resolve()
    os.environ.setdefault("MAXKB_PROJECT_DIR", str(root))
    os.chdir(root)
    sys.path.insert(0, str(root / "apps"))
    if args.env_file:
        from dotenv import load_dotenv

        load_dotenv(Path(args.env_file).resolve())
    os.environ.setdefault("DJANGO_SETTINGS_MODULE", "maxkb.settings")
    os.environ.setdefault("SERVER_NAME", "web")
    import django

    django.setup()
    from django.utils import timezone
    from application.models import Application, ApplicationApiKey, ApplicationVersion

    application = Application.objects.filter(id=application_id).first()
    published = ApplicationVersion.objects.filter(application_id=application_id).order_by("-create_time").first()
    if application is None or published is None or not application.is_publish:
        raise ValueError("应用不存在或没有可用的发布版本")
    if published.type != "SIMPLE" or any(
        (
            published.long_term_enable and published.long_term_trigger_type != "ROUND",
            application.long_term_enable and application.long_term_trigger_type != "ROUND",
            published.mcp_enable,
            published.tool_enable,
            published.application_enable,
            published.skill_tool_ids,
        )
    ):
        raise ValueError("MAXKB_ACTUAL_USAGE_UNSUPPORTED：请先核对应用的额外模型调用配置")
    spec = importlib.util.spec_from_file_location(
        "dpin_usage_verifier", root / "apps/common/handle/dpin_actual_usage.py"
    )
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    # 每次验证使用独立合法短期身份，避免污染原 API Key 的共享记忆。
    import secrets
    from datetime import timedelta
    from application.models import (
        Chat,
        ApplicationLongTermMemory,
        ApplicationChatUserStats,
    )
    from django.core.cache import cache
    from application.serializers.common import Cache_Version

    key = ApplicationApiKey.objects.create(
        application=application,
        workspace_id=application.workspace_id,
        secret_key="application-" + secrets.token_hex(32),
        is_permanent=False,
        expire_time=timezone.now() + timedelta(minutes=30),
    )
    chat_id = None
    try:
        for index, message in enumerate(
            (
                "这是本机合成岗位联调，请用一句话说明岗位工作地点。",
                "这是同一测试会话的第二轮，请简短说明工作地点信息来自哪些岗位资料。",
            ),
            start=1,
        ):
            payload = {
                "model": application_id,
                "messages": [{"role": "user", "content": message}],
                "stream": False,
            }
            if chat_id:
                payload["chat_id"] = chat_id
            request = urllib.request.Request(
                args.base_url.rstrip("/") + "/chat/api/" + application_id + "/chat/completions",
                data=json.dumps(payload).encode(),
                method="POST",
                headers={
                    "Content-Type": "application/json",
                    "Authorization": "Bearer " + key.secret_key,
                },
            )
            try:
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                response = opener.open(request, timeout=60)
            except urllib.error.HTTPError as error:
                raise ValueError("MaxKB 验证失败：HTTP " + str(error.code)) from None
            with response:
                body = response.read((1 << 20) + 1)
            if len(body) > 1 << 20:
                raise ValueError("MaxKB 响应超过限制")
            data = json.loads(body)
            usage = data.get("usage")
            if not isinstance(usage, dict) or usage.get("usage_source") != "upstream":
                raise ValueError("缺少真实上游用量标记，未通过验证")
            prompt, completion = helper.actual_token_usage(SimpleNamespace(response_metadata={"token_usage": usage}))
            cached = helper.actual_cached_tokens(SimpleNamespace(response_metadata={"token_usage": usage}), prompt)
            if cached is None or usage.get("cache_usage_source") != "upstream":
                raise ValueError("缺少真实缓存命中用量")
            calls = usage.get("calls")
            if not isinstance(calls, list) or not calls:
                raise ValueError("缺少逐次实际调用用量")
            ids = set()
            call_tokens = []
            call_cached = []
            for call in calls:
                if (
                    not isinstance(call, dict)
                    or not isinstance(call.get("call_id"), str)
                    or not call["call_id"]
                    or call["call_id"] in ids
                    or call.get("purpose") not in {"reply", "problem_optimization", "long_term_memory"}
                ):
                    raise ValueError("逐次实际调用标识或用途无效")
                ids.add(call["call_id"])
                call_tokens.append(helper.actual_token_usage(SimpleNamespace(response_metadata={"token_usage": call})))
                cached_value = call.get("cached_tokens")
                if call.get("cache_usage_source") != "upstream":
                    raise ValueError("逐次调用缺少真实缓存来源")
                validated_cached = helper.actual_cached_tokens(
                    SimpleNamespace(response_metadata={"token_usage": {"prompt_cache_hit_tokens": cached_value}}),
                    call_tokens[-1][0],
                )
                if validated_cached is None:
                    raise ValueError("逐次调用缺少真实缓存数量")
                call_cached.append(validated_cached)
            if cached != sum(call_cached):
                raise ValueError("缓存调用用量与汇总不一致")
            if prompt != sum(tokens[0] for tokens in call_tokens) or completion != sum(
                tokens[1] for tokens in call_tokens
            ):
                raise ValueError("逐次实际调用与汇总用量不一致")
            choices = data.get("choices")
            if not isinstance(choices, list) or len(choices) != 1:
                raise ValueError("回复选择数量无效")
            choice = choices[0]
            content = choice.get("message", {}).get("content")
            if (
                choice.get("index") != 0
                or choice.get("finish_reason") != "stop"
                or choice.get("message", {}).get("role") != "assistant"
                or not isinstance(content, str)
                or not content.strip()
            ):
                raise ValueError("回复不是完整的助手文本")
            returned_chat_id = str(uuid.UUID(choice.get("chat_id")))
            if chat_id is not None and chat_id != returned_chat_id:
                raise ValueError("MaxKB 续接返回了不同会话")
            chat_id = returned_chat_id
            print(
                json.dumps(
                    {
                        "turn": index,
                        "status": 200,
                        "prompt_tokens": prompt,
                        "completion_tokens": completion,
                        "total_tokens": prompt + completion,
                        "usage_source": "upstream",
                        "conversation_verified": True,
                        "calls": calls,
                        "retrieval": usage.get("retrieval"),
                    }
                )
            )
    finally:
        chats = list(
            Chat.objects.filter(application=application, chat_user_id=str(key.id)).values_list("id", flat=True)
        )
        Chat.objects.filter(id__in=chats).delete()
        ApplicationLongTermMemory.objects.filter(application=application, chat_user_id=str(key.id)).delete()
        ApplicationChatUserStats.objects.filter(application=application, chat_user_id=str(key.id)).delete()
        for chat in chats:
            cache.delete(
                Cache_Version.CHAT.get_key(key=str(chat)),
                version=Cache_Version.CHAT_INFO.get_version(),
            )
        key.delete()
        print(json.dumps({"temporary_identity_cleaned": True, "chats_cleaned": len(chats)}))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # 网络、SDK、数据库异常文本可能包含 URL/凭据或正文，只输出安全业务错误。
        message = str(error) if type(error) is ValueError else type(error).__name__
        print("验证失败：" + message, file=sys.stderr)
        raise SystemExit(1) from None

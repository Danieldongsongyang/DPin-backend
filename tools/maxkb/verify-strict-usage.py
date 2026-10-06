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
        base.scheme != "http" or base.hostname not in ("localhost", "127.0.0.1", "::1")
        or base.username or base.password or base.path not in ("", "/") or base.query or base.fragment
    ):
        raise ValueError("验证器只允许本机 HTTP Origin")
    application_id = str(uuid.UUID(args.application_id))
    root = Path(args.maxkb_root).resolve()
    os.chdir(root)
    sys.path.insert(0, str(root / "apps"))
    if args.env_file:
        from dotenv import load_dotenv
        load_dotenv(Path(args.env_file).resolve())
    os.environ.setdefault("DJANGO_SETTINGS_MODULE", "maxkb.settings")
    os.environ.setdefault("SERVER_NAME", "web")
    import django
    django.setup()
    from django.db.models import Q
    from django.utils import timezone
    from application.models import Application, ApplicationApiKey, ApplicationVersion

    application = Application.objects.filter(id=application_id).first()
    published = ApplicationVersion.objects.filter(application_id=application_id).order_by("-create_time").first()
    if application is None or published is None or not application.is_publish:
        raise ValueError("应用不存在或没有可用的发布版本")
    if published.type != "SIMPLE" or any((
        published.problem_optimization, published.long_term_enable, application.long_term_enable,
        published.mcp_enable, published.tool_enable, published.application_enable, published.skill_tool_ids,
    )):
        raise ValueError("MAXKB_ACTUAL_USAGE_UNSUPPORTED：请先核对应用的额外模型调用配置")
    key = ApplicationApiKey.objects.filter(application_id=application_id, is_active=True).filter(
        Q(is_permanent=True) | Q(expire_time__gt=timezone.now())
    ).first()
    if key is None:
        raise ValueError("没有有效的应用 API Key")
    spec = importlib.util.spec_from_file_location("dpin_usage_verifier", Path(__file__).with_name("dpin_actual_usage.py"))
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    chat_id = None
    for index, message in enumerate((
        "Reply with OK only. This is a local integration test.",
        "Reply with OK again. This is the second turn of the same local integration test.",
    ), start=1):
        payload = {"model": application_id, "messages": [{"role": "user", "content": message}], "stream": False}
        if chat_id:
            payload["chat_id"] = chat_id
        request = urllib.request.Request(
            args.base_url.rstrip("/") + "/chat/api/" + application_id + "/chat/completions",
            data=json.dumps(payload).encode(), method="POST",
            headers={"Content-Type": "application/json", "Authorization": "Bearer " + key.secret_key},
        )
        try:
            response = urllib.request.urlopen(request, timeout=60)
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
        choices = data.get("choices")
        if not isinstance(choices, list) or len(choices) != 1:
            raise ValueError("回复选择数量无效")
        choice = choices[0]
        content = choice.get("message", {}).get("content")
        if (
            choice.get("index") != 0 or choice.get("finish_reason") != "stop"
            or choice.get("message", {}).get("role") != "assistant"
            or not isinstance(content, str) or not content.strip()
        ):
            raise ValueError("回复不是完整的助手文本")
        returned_chat_id = str(uuid.UUID(choice.get("chat_id")))
        if chat_id is not None and chat_id != returned_chat_id:
            raise ValueError("MaxKB 续接返回了不同会话")
        chat_id = returned_chat_id
        print(json.dumps({
            "turn": index, "status": 200, "prompt_tokens": prompt, "completion_tokens": completion,
            "total_tokens": prompt + completion, "usage_source": "upstream", "conversation_verified": True,
        }))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # 网络、SDK、数据库异常文本可能包含 URL/凭据或正文，只输出安全业务错误。
        message = str(error) if type(error) is ValueError else type(error).__name__
        print("验证失败：" + message, file=sys.stderr)
        raise SystemExit(1) from None

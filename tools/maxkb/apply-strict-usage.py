#!/usr/bin/env python3
"""为已核对的 MaxKB SIMPLE 非流式 OpenAI 接口添加真实用量契约。

默认仅检查并输出改动文件；--apply 写入指定源码工程，保留原文件备份。
不操作运行中的容器、不重启服务、不读取模型配置或凭据。
"""

import argparse
import hashlib
import os
from pathlib import Path


def prepare_changes(root):
    root = Path(root).resolve()
    helper = Path(__file__).with_name("dpin_actual_usage.py").read_text()
    pipeline_path = root / "apps/application/chat_pipeline/step/chat_step/impl/base_chat_step.py"
    response_path = root / "apps/common/handle/impl/response/openai_to_response.py"
    serializer_path = root / "apps/chat/serializers/chat.py"
    helper_path = root / "apps/common/handle/dpin_actual_usage.py"
    pipeline = pipeline_path.read_text()
    response = response_path.read_text()
    serializer = serializer_path.read_text()
    marker = "# DPin actual-usage contract v1"
    installed = any(marker in source for source in (pipeline, response, serializer))
    if installed:
        if not all(marker in source for source in (pipeline, response, serializer)) or not helper_path.exists() or helper_path.read_text() != helper:
            raise ValueError("真实用量补丁不完整或版本不一致，未写入任何文件")
        backups = [path.with_name(path.name + ".dpin-original") for path in (pipeline_path, response_path, serializer_path)]
        if not all(path.is_file() for path in backups):
            raise ValueError("缺少原始备份，无法验证已安装补丁的完整性")
        pipeline, response, serializer = [path.read_text() for path in backups]

    token_block = """            if is_ai_chat:
                request_token = chat_model.get_num_tokens_from_messages(message_list)
                response_token = chat_model.get_num_tokens(chat_result.content)
            else:
                request_token = 0
                response_token = 0
"""
    actual_block = """            # DPin actual-usage contract v1
            # Only the OpenAI application API requires this strict contract.
            # Inspect this invocation's response, never cached model state.
            actual_usage_verified = False
            if isinstance(manage.get_base_to_response(), OpenaiToResponse):
                if not is_ai_chat:
                    raise ActualUsageUnavailable()
                request_token, response_token = actual_token_usage(chat_result)
                actual_usage_verified = True
            elif is_ai_chat:
                request_token = chat_model.get_num_tokens_from_messages(message_list)
                response_token = chat_model.get_num_tokens(chat_result.content)
            else:
                request_token = 0
                response_token = 0
"""
    answer_marker = """                    "answer_list": [
                        {"content": content, "reasoning_content": reasoning_content if reasoning_content_enable else ""}
                    ],
"""
    response_marker = """        return JsonResponse(data=data, status=_status)
"""
    serializer_marker = """        if chat_info.application.type == ApplicationTypeChoices.SIMPLE:
            self.is_valid_application_simple(raise_exception=True, chat_info=chat_info)
"""
    invoke_marker = """        try:
            chat_result, is_ai_chat = self.get_block_result(
"""
    exception_marker = """        except Exception as e:
            all_text = "Exception:" + str(e)
            write_context(self, manage, 0, 0, all_text)
"""
    # 精确匹配已检查的调用点；源码版本不同就停止，不能凭模糊替换修改运行语义。
    if (
        pipeline.count(token_block) != 1 or pipeline.count(answer_marker) != 1
        or pipeline.count(invoke_marker) != 1 or pipeline.count(exception_marker) != 1
        or response.count(response_marker) != 1 or serializer.count(serializer_marker) != 1
    ):
        raise ValueError("MaxKB 源码与已核对的接口版本不匹配，未写入任何文件")
    if helper_path.exists() and helper_path.read_text() != helper:
        raise ValueError("目标辅助模块已存在且内容不同，未覆盖")
    pipeline = pipeline.replace(token_block, actual_block, 1).replace(
        answer_marker, '                    "usage_source": "upstream" if actual_usage_verified else "unverified",\n' + answer_marker, 1
    )
    pipeline = pipeline.replace(invoke_marker, """        try:
            if isinstance(manage.get_base_to_response(), OpenaiToResponse) and any(
                (mcp_tool_ids, mcp_servers, tool_ids, application_ids, skill_tool_ids)
            ):
                raise ActualUsageUnavailable()
            chat_result, is_ai_chat = self.get_block_result(
""", 1).replace(exception_marker, """        except ActualUsageUnavailable:
            # No reply or synthetic zero usage is persisted for a failed contract.
            return manage.get_base_to_response().actual_usage_error()
        except Exception as e:
            all_text = "Exception:" + str(e)
            write_context(self, manage, 0, 0, all_text)
""", 1)
    # 放在首个 import 前，保留文件原有版权、作者及模块说明。
    import_start = pipeline.find("\nimport ")
    if import_start < 0:
        raise ValueError("无法确定 MaxKB 模块导入位置")
    pipeline = pipeline[:import_start] + "\nfrom common.handle.dpin_actual_usage import actual_token_usage, ActualUsageUnavailable\nfrom common.handle.impl.response.openai_to_response import OpenaiToResponse\n" + pipeline[import_start:]
    response = response.replace(response_marker, """        # DPin actual-usage contract v1
        if _status == status.HTTP_200_OK:
            if other_params.get("usage_source") != "upstream":
                return self.actual_usage_error()
            data["usage"]["usage_source"] = "upstream"
        return JsonResponse(data=data, status=_status)

    def actual_usage_error(self, code="MAXKB_ACTUAL_USAGE_UNAVAILABLE"):
        return JsonResponse(
            {"error": {"code": code, "message": "Actual token usage contract is unavailable"}},
            status=status.HTTP_502_BAD_GATEWAY,
        )
""", 1)
    serializer = serializer.replace(serializer_marker, """        # DPin actual-usage contract v1
        # These paths can invoke additional models without aggregate upstream usage.
        # Reject before generation rather than billing only the final model response.
        if isinstance(base_to_response, OpenaiToResponse) and not instance.get('stream'):
            application = chat_info.application
            active_application = QuerySet(Application).filter(id=chat_info.application_id).only('long_term_enable').first()
            if application.type != ApplicationTypeChoices.SIMPLE or any((
                application.problem_optimization, application.long_term_enable,
                application.mcp_enable, application.tool_enable, application.application_enable,
                application.skill_tool_ids, active_application is None,
                active_application is not None and active_application.long_term_enable,
            )):
                return base_to_response.actual_usage_error("MAXKB_ACTUAL_USAGE_UNSUPPORTED")
        if chat_info.application.type == ApplicationTypeChoices.SIMPLE:
            self.is_valid_application_simple(raise_exception=True, chat_info=chat_info)
""", 1)
    changes = {pipeline_path: pipeline, response_path: response, serializer_path: serializer, helper_path: helper}
    for path, content in changes.items():
        compile(content, str(path), "exec")
    if installed:
        if any(path.read_text() != content for path, content in changes.items()):
            raise ValueError("已安装补丁与当前契约不同，未覆盖任何文件")
        return {}
    return changes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--maxkb-root", required=True, help="包含 apps/ 的 MaxKB 可编辑源码目录")
    parser.add_argument("--apply", action="store_true", help="在备份后实际写入源码；服务启动仍由部署者执行")
    args = parser.parse_args()
    changes = prepare_changes(args.maxkb_root)
    if not changes:
        print("真实用量补丁已完整安装，无需重复写入")
        return
    if args.apply:
        # 全部检查通过才写入。已有不一致备份需要人工检查，不静默覆盖。
        for path in changes:
            backup = path.with_name(path.name + ".dpin-original")
            if path.exists() and backup.exists() and backup.read_bytes() != path.read_bytes():
                raise ValueError("原始备份与当前源码不同，未写入任何文件")
        for path, content in changes.items():
            if path.exists():
                backup = path.with_name(path.name + ".dpin-original")
                if not backup.exists():
                    backup.write_bytes(path.read_bytes())
            temporary = path.with_name(path.name + ".dpin-tmp")
            temporary.write_text(content)
            os.replace(temporary, path)
    for path, content in changes.items():
        print(("已写入 " if args.apply else "待写入 ") + str(path) + " SHA256=" + hashlib.sha256(content.encode()).hexdigest())


if __name__ == "__main__":
    main()

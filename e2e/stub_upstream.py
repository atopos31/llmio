#!/usr/bin/env python3
"""双向协议的桩上游。

作用有两个，缺一不可：

1. **挑剔**——按真上游的规矩校验 llmio 发来的请求（Anthropic 的 max_tokens 必填、
   content 必须是块数组、tool_result 必须找得到对应的 tool_use；OpenAI 的
   assistant 消息必须带 tool_calls 才配得上后面的 tool 消息……）。不合规矩就 400，
   而且把原因写在响应体里——翻译层漏掉的东西会在这里变成一次可见的失败，而不是
   一个"看起来也对"的答案。
2. **留痕**——每个收到的请求体原样写进 jsonl，事后核对 llmio 到底发出去了什么
   （比如 seed 有没有被丢掉、max_tokens 补了没有、usage 是不是真的被要求了）。

行为由收到的 model 名决定（llmio 会把关联行的"提供商模型"写进去），这样一套桩就能
伺候十几条用例。响应侧同样要有花样：两条候选、content_filter、reasoning_content、
认不出的 stop_reason——它们各自对应翻译层的一条记账。
"""

import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STUB_LOG = os.environ.get("STUB_LOG", "stub-received.jsonl")
# 故意在文本里带上 model 名：客户端能看见自己请求的是哪个"提供商模型"，
# 于是"这次到底落在谁身上"不用翻日志也能看出来
TEXT = "这是桩上游对 {model} 的回答。"


def log_request(body, headers, path):
    with open(STUB_LOG, "a", encoding="utf-8") as f:
        f.write(
            json.dumps(
                {
                    "path": path,
                    "auth": headers.get("x-api-key") or headers.get("authorization", ""),
                    "version": headers.get("anthropic-version", ""),
                    "body": body,
                },
                ensure_ascii=False,
            )
            + "\n"
        )


def behavior(model):
    """model 形如 stub-<行为>；没写行为就是普通文本。"""
    name = model or ""
    for b in (
        "two-tools",
        "bad-args",
        "choices",
        "filter",
        "reason",
        "pause",
        "refusal",
        "thinking",
        "boom",
        "tool",
        "text",
    ):
        if name.endswith(b):
            return b
    return "text"


# ── Anthropic 侧的校验与响应 ────────────────────────────────────────────────


def anthropic_validate(req):
    errs = []
    if "max_tokens" not in req:
        errs.append("max_tokens is required")
    msgs = req.get("messages")
    if not isinstance(msgs, list) or not msgs:
        errs.append("messages must be a non-empty array")
        return errs
    if "system" in req and not isinstance(req["system"], (str, list)):
        errs.append("system must be a string or an array of blocks")
    for i, m in enumerate(msgs):
        if m.get("role") not in ("user", "assistant"):
            errs.append(f"messages[{i}].role is invalid: {m.get('role')!r}")
        c = m.get("content")
        if not isinstance(c, list):
            errs.append(f"messages[{i}].content must be an array of blocks")
            continue
        for b in c:
            t = b.get("type")
            if t == "text" and not b.get("text"):
                errs.append(f"messages[{i}] has an empty text block")
            if t == "tool_result" and not b.get("tool_use_id"):
                errs.append(f"messages[{i}] tool_result without tool_use_id")
            if t == "tool_use" and not b.get("id"):
                errs.append(f"messages[{i}] tool_use without id")
            if t == "image":
                src = b.get("source") or {}
                if src.get("type") not in ("base64", "url"):
                    errs.append(f"messages[{i}] image source type {src.get('type')!r}")
                if src.get("type") == "base64" and not src.get("data"):
                    errs.append(f"messages[{i}] base64 image without data")
                if src.get("type") == "url" and not src.get("url"):
                    errs.append(f"messages[{i}] url image without url")
    return errs


def anthropic_blocks(req):
    """按行为造响应内容块。"""
    b = behavior(req.get("model"))
    if b == "tool":
        return [{"type": "tool_use", "id": "toolu_stub_1", "name": "get_weather", "input": {"city": "上海"}}], "tool_use"
    if b == "two-tools":
        return [
            {"type": "tool_use", "id": "toolu_stub_1", "name": "get_weather", "input": {"city": "上海"}},
            {"type": "tool_use", "id": "toolu_stub_2", "name": "get_time", "input": {"tz": "Asia/Shanghai"}},
        ], "tool_use"
    if b == "thinking":
        return [
            {"type": "thinking", "thinking": "先想一下", "signature": "sig"},
            {"type": "text", "text": "想完了。"},
        ], "end_turn"
    if b == "pause":
        return [{"type": "text", "text": "任务太长，先停一下。"}], "pause_turn"
    if b == "refusal":
        return [{"type": "text", "text": "这个我不能答。"}], "refusal"
    if b == "filter":
        # OpenAI 的 content_filter 在 Anthropic 侧没有对应档，只能落到 refusal
        return [{"type": "text", "text": "（被过滤）"}], "refusal"
    return [{"type": "text", "text": TEXT.format(model=req.get("model"))}], "end_turn"


def anthropic_usage(req):
    # input_tokens 故意非零：收尾提前发生的那处 bug 会让它在 llmio 那边永远是 0
    n = 137
    return {"input_tokens": n, "output_tokens": 12}


def anthropic_sse(req, blocks, stop_reason):
    model = req.get("model")
    ev = []

    def emit(name, data):
        ev.append(f"event: {name}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n")

    emit(
        "message_start",
        {
            "type": "message_start",
            "message": {
                "id": "msg_stub",
                "type": "message",
                "role": "assistant",
                "model": model,
                "content": [],
                "stop_reason": None,
                "usage": {"input_tokens": 137, "output_tokens": 0},
            },
        },
    )
    for i, block in enumerate(blocks):
        if block["type"] == "text":
            emit("content_block_start", {"type": "content_block_start", "index": i, "content_block": {"type": "text", "text": ""}})
            for piece in [block["text"][:6], block["text"][6:]]:
                if not piece:
                    continue
                emit(
                    "content_block_delta",
                    {"type": "content_block_delta", "index": i, "delta": {"type": "text_delta", "text": piece}},
                )
        elif block["type"] == "thinking":
            emit(
                "content_block_start",
                {"type": "content_block_start", "index": i, "content_block": {"type": "thinking", "thinking": ""}},
            )
            emit(
                "content_block_delta",
                {"type": "content_block_delta", "index": i, "delta": {"type": "thinking_delta", "thinking": block["thinking"]}},
            )
        elif block["type"] == "tool_use":
            emit(
                "content_block_start",
                {"type": "content_block_start", "index": i, "content_block": {"type": "tool_use", "id": block["id"], "name": block["name"], "input": {}}},
            )
            # 参数分两段发：翻译层必须攒起来，不能边收边发
            raw = json.dumps(block["input"], ensure_ascii=False)
            half = max(1, len(raw) // 2)
            for piece in (raw[:half], raw[half:]):
                if not piece:
                    continue
                emit(
                    "content_block_delta",
                    {"type": "content_block_delta", "index": i, "delta": {"type": "input_json_delta", "partial_json": piece}},
                )
        emit("content_block_stop", {"type": "content_block_stop", "index": i})
    emit(
        "message_delta",
        {"type": "message_delta", "delta": {"stop_reason": stop_reason, "stop_sequence": None}, "usage": {"output_tokens": 12}},
    )
    emit("message_stop", {"type": "message_stop"})
    return "".join(ev).encode()


# ── OpenAI 侧的校验与响应 ──────────────────────────────────────────────────


def openai_validate(req):
    errs = []
    msgs = req.get("messages")
    if not isinstance(msgs, list) or not msgs:
        errs.append("messages must be a non-empty array")
        return errs
    for i, m in enumerate(msgs):
        role = m.get("role")
        if role not in ("system", "user", "assistant", "tool", "developer"):
            errs.append(f"messages[{i}].role is invalid: {role!r}")
        if role == "tool" and not m.get("tool_call_id"):
            errs.append(f"messages[{i}] tool message without tool_call_id")
        c = m.get("content")
        if role in ("system", "user") and c is None:
            errs.append(f"messages[{i}] content is missing")
        if isinstance(c, list):
            for b in c:
                if b.get("type") == "text" and not b.get("text"):
                    errs.append(f"messages[{i}] has an empty text part")
                if b.get("type") == "image_url":
                    u = (b.get("image_url") or {}).get("url", "")
                    if not u.startswith("data:") and not u.startswith("http"):
                        errs.append(f"messages[{i}] image_url is not a data URI or http url")
    return errs


def openai_message(req):
    b = behavior(req.get("model"))
    model = req.get("model")
    msg = {"role": "assistant", "content": TEXT.format(model=model)}
    finish = "stop"
    if b == "tool":
        msg = {
            "role": "assistant",
            "content": None,
            "tool_calls": [
                {
                    "id": "call_stub_1",
                    "type": "function",
                    "function": {"name": "get_weather", "arguments": json.dumps({"city": "上海"}, ensure_ascii=False)},
                }
            ],
        }
        finish = "tool_calls"
    elif b == "two-tools":
        msg = {
            "role": "assistant",
            "content": None,
            "tool_calls": [
                {"id": "call_stub_1", "type": "function", "function": {"name": "get_weather", "arguments": '{"city":"上海"}'}},
                {"id": "call_stub_2", "type": "function", "function": {"name": "get_time", "arguments": '{"tz":"Asia/Shanghai"}'}},
            ],
        }
        finish = "tool_calls"
    elif b == "bad-args":
        msg = {
            "role": "assistant",
            "content": None,
            "tool_calls": [{"id": "call_stub_1", "type": "function", "function": {"name": "get_weather", "arguments": "{半截"}}],
        }
        finish = "tool_calls"
    elif b == "filter":
        msg = {"role": "assistant", "content": "（被过滤）", "refusal": "涉及敏感内容"}
        finish = "content_filter"
    elif b == "reason":
        msg = {"role": "assistant", "content": "答案是 42。", "reasoning_content": "心里盘算了一下"}
    return msg, finish


def openai_stream_options(req):
    return (req.get("stream_options") or {}).get("include_usage") is True


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # 安静点，日志我们自己记
        pass

    def _read(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b""
        try:
            return json.loads(raw.decode("utf-8")) if raw else {}
        except Exception:
            return {"__unparsable__": raw.decode("utf-8", "replace")}

    def _json(self, code, payload):
        body = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _sse(self, chunks):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        for c in chunks:
            self.wfile.write(c.encode() if isinstance(c, str) else c)
            self.wfile.flush()
            time.sleep(0.01)  # 逼出真实的分包，翻译层得能扛住
        self.close_connection = True

    def do_GET(self):
        if self.path.endswith("/models"):
            if "anthropic" in self.path:
                self._json(200, {"data": [{"id": "stub-text", "type": "model", "display_name": "stub-text", "created_at": "2026-01-01T00:00:00Z"}], "has_more": False})
            else:
                self._json(200, {"object": "list", "data": [{"id": "stub-text", "object": "model", "created": 1, "owned_by": "stub"}]})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        req = self._read()
        is_anthropic = "/anthropic" in self.path
        log_request(req, self.headers, self.path)
        if req.get("__unparsable__") is not None:
            self._json(400, {"error": {"type": "invalid_request_error", "message": "body is not json"}})
            return

        model = req.get("model") or ""
        b = behavior(model)
        if b == "boom":
            # 让重试路径有得可试：第一次 400，第二次 400，第三次还是 400
            if is_anthropic:
                self._json(400, {"type": "error", "error": {"type": "invalid_request_error", "message": "桩上游：这个模型就是坏的"}})
            else:
                self._json(400, {"error": {"message": "桩上游：这个模型就是坏的", "type": "invalid_request_error"}})
            return

        errs = anthropic_validate(req) if is_anthropic else openai_validate(req)
        if errs:
            if is_anthropic:
                self._json(400, {"type": "error", "error": {"type": "invalid_request_error", "message": "; ".join(errs)}})
            else:
                self._json(400, {"error": {"message": "; ".join(errs), "type": "invalid_request_error"}})
            return

        if is_anthropic:
            blocks, stop_reason = anthropic_blocks(req)
            if req.get("stream"):
                self._sse([anthropic_sse(req, blocks, stop_reason)])
                return
            self._json(
                200,
                {
                    "id": "msg_stub",
                    "type": "message",
                    "role": "assistant",
                    "model": model,
                    "content": blocks,
                    "stop_reason": stop_reason,
                    "stop_sequence": None,
                    "usage": anthropic_usage(req),
                },
            )
            return

        msg, finish = openai_message(req)
        if req.get("stream"):
            chunks = []
            first = {"id": "cmpl_stub", "object": "chat.completion.chunk", "created": 1, "model": model,
                     "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}]}
            chunks.append(f"data: {json.dumps(first, ensure_ascii=False)}\n\n")
            if msg.get("content"):
                for piece in (msg["content"][:5], msg["content"][5:]):
                    if not piece:
                        continue
                    d = {"id": "cmpl_stub", "object": "chat.completion.chunk", "created": 1, "model": model,
                         "choices": [{"index": 0, "delta": {"content": piece}, "finish_reason": None}]}
                    chunks.append(f"data: {json.dumps(d, ensure_ascii=False)}\n\n")
            for i, tc in enumerate(msg.get("tool_calls") or []):
                # 两路交错发参数：每个 index 各发两段，翻译层按通道攒
                args = tc["function"]["arguments"]
                half = max(1, len(args) // 2)
                for piece in (args[:half], args[half:]):
                    d = {"id": "cmpl_stub", "object": "chat.completion.chunk", "created": 1, "model": model,
                         "choices": [{"index": 0, "delta": {"tool_calls": [
                             {"index": i, "id": tc["id"] if piece is args[:half] else None,
                              "type": "function",
                              "function": {"name": tc["function"]["name"] if piece is args[:half] else None,
                                           "arguments": piece}}]},
                             "finish_reason": None}]}
                    chunks.append(f"data: {json.dumps(d, ensure_ascii=False)}\n\n")
            if msg.get("reasoning_content"):
                d = {"id": "cmpl_stub", "object": "chat.completion.chunk", "created": 1, "model": model,
                     "choices": [{"index": 0, "delta": {"reasoning_content": msg["reasoning_content"]}, "finish_reason": None}]}
                chunks.append(f"data: {json.dumps(d, ensure_ascii=False)}\n\n")
            end = {"id": "cmpl_stub", "object": "chat.completion.chunk", "created": 1, "model": model,
                   "choices": [{"index": 0, "delta": {}, "finish_reason": finish}]}
            chunks.append(f"data: {json.dumps(end, ensure_ascii=False)}\n\n")
            if openai_stream_options(req):
                # 真上游在 include_usage 时的形状：先一条 finish_reason 的空 usage，
                # 再一条 choices 为空、带 usage 的收尾——收尾提前发生就会把它丢掉
                usage = {"id": "cmpl_stub", "object": "chat.completion.chunk", "created": 1, "model": model,
                         "choices": [], "usage": {"prompt_tokens": 137, "completion_tokens": 12, "total_tokens": 149}}
                chunks.append(f"data: {json.dumps(usage, ensure_ascii=False)}\n\n")
            chunks.append("data: [DONE]\n\n")
            self._sse(chunks)
            return

        choices = [{"index": 0, "message": msg, "finish_reason": finish, "logprobs": None}]
        if b == "choices":
            second = {"role": "assistant", "content": "第二条候选（不该出现在 Anthropic 客户端眼前）"}
            choices.append({"index": 1, "message": second, "finish_reason": finish, "logprobs": None})
        self._json(
            200,
            {
                "id": "cmpl_stub",
                "object": "chat.completion",
                "created": 1,
                "model": model,
                "choices": choices,
                "usage": {"prompt_tokens": 137, "completion_tokens": 12, "total_tokens": 149},
            },
        )


def main():
    port = int(os.environ.get("STUB_PORT", "7198"))
    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"stub upstream on 127.0.0.1:{port}", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    sys.exit(main())

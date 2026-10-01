#!/usr/bin/env python3
"""协议互转的端到端矩阵。

一辆不依赖任何外部服务的验收车：桩上游（`stub_upstream.py`）当"挑剔的真上游"，
另建二进制名在独立工作目录里起 llmio，用管理 API 建提供商/模型/关联，然后一发一发地
打进去，逐发核对三处——

1. **客户端拿到的**：是客户端协议的形状（OpenAI 客户端拿到 `choices`，Anthropic
   客户端拿到 `content` 块），或者在该拒绝时确实报错；
2. **上游收到的**（读桩的 jsonl）：路径对不对、`model` 有没有被换成"提供商模型"、
   `max_tokens` 补了没有、`seed` 这种翻不过去的参数是不是真的没发出去；
3. **llmio 记下的**（`/api/logs`）：`Style` / `upstream_style` / `bridge_notes` / tokens。

三处缺一不可：只看客户端响应的话，"翻译层把 seed 悄悄丢了"和"翻译层没错但上游不认"
长得一模一样；只看日志的话，日志本身错了也没人发现。

用法：
    python e2e/run_matrix.py --upstream stub        # 本地桩，全矩阵
    python e2e/run_matrix.py --upstream opencode    # 真实上游，小矩阵（省用量）
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(E2E_DIR)
RUN_DIR = os.path.join(E2E_DIR, ".run")
BIN = os.path.join(RUN_DIR, "llmio-e2e.exe")
KEY_FILE = os.path.join(RUN_DIR, "opencode.key")

LLMIO_PORT = 7200
STUB_PORT = 7199
TOKEN = "e2e-token"
BASE = f"http://127.0.0.1:{LLMIO_PORT}"

OPENCODE_BASE = "https://opencode.ai/zen/go/v1"
# opencode 强制要求会话头，缺了直接 400（见 docs/session-header-injection-design.md）
OPENCODE_HEADERS = {"x-opencode-session": "llmio-e2e-{{session}}", "user-agent": "llmio-e2e/1.0"}


# ── HTTP ───────────────────────────────────────────────────────────────────


def http(method, url, body=None, headers=None, timeout=60):
    data = None
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body, ensure_ascii=False).encode()
    req = urllib.request.Request(url, data=data, method=method)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return res.status, res.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")


def api(method, path, body=None):
    # 路径一律相对 /api 写。少写这一段的后果不是 404 而是拿到 SPA 的 index.html
    # （Gin 的 NoRoute 兜底），所以这里统一补上，让调用方不可能漏
    status, text = http(method, BASE + "/api" + path, body, {"Authorization": f"Bearer {TOKEN}"})
    payload = json.loads(text) if text else {}
    if payload.get("code") != 200:
        raise RuntimeError(f"{method} {path} -> {status} {text[:400]}")
    return payload.get("data")


def chat(client, path, body, headers, stream=False, timeout=120):
    """打客户端端点。流式时把整条流读完再返回——不读完，llmio 那边的记账与
    收尾事件都不会发生，核对的就是半成品。"""
    h = dict(headers)
    h["Content-Type"] = "application/json"
    if stream:
        h["Accept"] = "text/event-stream"
    req = urllib.request.Request(BASE + path, data=json.dumps(body, ensure_ascii=False).encode(), method="POST")
    for k, v in h.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            raw = res.read().decode("utf-8", "replace")
            return res.status, raw
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")


def client_headers(client):
    if client == "openai":
        return {"Authorization": f"Bearer {TOKEN}"}
    return {"x-api-key": TOKEN, "anthropic-version": "2023-06-01"}


CLIENT_PATH = {"openai": "/openai/v1/chat/completions", "anthropic": "/anthropic/v1/messages"}


# ── llmio 进程 ─────────────────────────────────────────────────────────────


class Llmio:
    def __init__(self, workdir):
        self.workdir = workdir
        self.proc = None

    def start(self):
        env = dict(os.environ, TOKEN=TOKEN, LLMIO_SERVER_PORT=str(LLMIO_PORT), GIN_MODE="release", TZ="Asia/Shanghai")
        self.proc = subprocess.Popen(
            [BIN], cwd=self.workdir, env=env,
            stdout=open(os.path.join(self.workdir, "llmio.log"), "ab"),
            stderr=subprocess.STDOUT,
        )
        for _ in range(100):
            try:
                api("GET", "/version")
                return
            except Exception:
                time.sleep(0.15)
        raise RuntimeError("llmio 没起来，看看 " + os.path.join(self.workdir, "llmio.log"))

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()


class Stub:
    def __init__(self, workdir):
        self.workdir = workdir
        self.log = os.path.join(workdir, "stub-received.jsonl")
        self.proc = None

    def start(self):
        open(self.log, "w").close()
        env = dict(os.environ, STUB_PORT=str(STUB_PORT), STUB_LOG=self.log)
        self.proc = subprocess.Popen(
            [sys.executable, os.path.join(E2E_DIR, "stub_upstream.py")],
            cwd=self.workdir, env=env,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        for _ in range(60):
            status, _ = http("GET", f"http://127.0.0.1:{STUB_PORT}/openai/models")
            if status == 200:
                return
            time.sleep(0.1)
        raise RuntimeError("桩上游没起来")

    def received(self):
        with open(self.log, encoding="utf-8") as f:
            return [json.loads(line) for line in f if line.strip()]

    def last(self):
        got = self.received()
        return got[-1] if got else None

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            self.proc.wait(timeout=10)


# ── 断言 ───────────────────────────────────────────────────────────────────


class Failure(Exception):
    pass


def need(cond, msg):
    if not cond:
        raise Failure(msg)


def text_of(client, raw):
    """把客户端拿到的回答抽成一段纯文本——两种协议各有各的形状。

    工具调用连名字一起收进来：只收参数的话，"工具名翻错了"这种错就看不出来。
    """
    if client == "openai":
        payload = json.loads(raw)
        msg = payload["choices"][0]["message"]
        return (msg.get("content") or "") + "".join(
            tc["function"]["name"] + tc["function"]["arguments"] for tc in (msg.get("tool_calls") or [])
        )
    payload = json.loads(raw)
    out = []
    for b in payload.get("content") or []:
        if b["type"] == "text":
            out.append(b["text"])
        elif b["type"] == "tool_use":
            out.append(b["name"] + json.dumps(b["input"], ensure_ascii=False, sort_keys=True))
    return "".join(out)


def check_log(log, spec, label):
    if "status" in spec:
        need(log["Status"] == spec["status"], f"{label}: 日志 status={log['Status']}，期望 {spec['status']}")
    if "style" in spec:
        need(log["Style"] == spec["style"], f"{label}: 日志 Style={log['Style']}，期望 {spec['style']}")
    if "upstream_style" in spec:
        got = log.get("upstream_style", "")
        need(got == spec["upstream_style"], f"{label}: 上游协议记成 {got!r}，期望 {spec['upstream_style']!r}")
    notes = [c for c in (log.get("bridge_notes") or "").split(",") if c]
    for code in spec.get("notes", []):
        need(code in notes, f"{label}: 记账里没有 {code}（实际 {notes}）")
    for code in spec.get("notes_absent", []):
        need(code not in notes, f"{label}: 记账里不该有 {code}（实际 {notes}）")
    if "notes_exact" in spec:
        need(sorted(notes) == sorted(spec["notes_exact"]), f"{label}: 记账是 {notes}，期望 {spec['notes_exact']}")
    if "prompt_tokens" in spec:
        need(log.get("prompt_tokens") == spec["prompt_tokens"],
             f"{label}: 输入 token 记成 {log.get('prompt_tokens')}，期望 {spec['prompt_tokens']}")
    if "completion_tokens" in spec:
        need(log.get("completion_tokens") == spec["completion_tokens"],
             f"{label}: 输出 token 记成 {log.get('completion_tokens')}，期望 {spec['completion_tokens']}")


def check_upstream(got, spec, label):
    if got is None:
        if spec.get("none"):
            return
        raise Failure(f"{label}: 上游一个请求都没收到")
    if "path" in spec:
        need(got["path"].endswith(spec["path"]), f"{label}: 上游收到 {got['path']}，期望以 {spec['path']} 结尾")
    body = got["body"]
    if "model" in spec:
        need(body.get("model") == spec["model"], f"{label}: 上游收到 model={body.get('model')!r}，期望 {spec['model']!r}")
    for key in spec.get("present", []):
        need(key in body, f"{label}: 发往上游的请求里没有 {key}")
    for key in spec.get("absent", []):
        need(key not in body, f"{label}: 发往上游的请求里不该有 {key}")
    for key, want in spec.get("equals", {}).items():
        need(body.get(key) == want, f"{label}: 上游收到 {key}={body.get(key)!r}，期望 {want!r}")
    if "auth" in spec:
        need(got["auth"] == spec["auth"], f"{label}: 上游收到的凭据不对：{got['auth']!r}")
    # 嵌套字段看不过来，就用整份请求体的紧凑串做子串检查
    dump = json.dumps(body, ensure_ascii=False, separators=(",", ":"))
    for frag in spec.get("dump", []):
        need(frag in dump, f"{label}: 上游收到的请求里找不到 {frag}")
    for frag in spec.get("dump_absent", []):
        need(frag not in dump, f"{label}: 上游收到的请求里不该出现 {frag}")
    for frag, count in spec.get("dump_count", {}).items():
        need(dump.count(frag) == count, f"{label}: 上游收到的请求里 {frag} 出现 {dump.count(frag)} 次，期望 {count} 次")


def wait_log(model, status=None, timeout=15):
    """日志先以 running 落库，响应读完才补上状态与记账，所以要等。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        page = api("GET", f"/logs?name={model}&page=1&page_size=10")
        rows = page.get("data") or []
        picked = None
        for row in rows:
            if status is None or row["Status"] == status:
                picked = row
                break
        if picked and picked["Status"] != "running":
            return picked
        time.sleep(0.2)
    raise Failure(f"{model}: 等不到一条 status={status or '任意'} 的日志")


# ── 建档 ───────────────────────────────────────────────────────────────────


def make_provider(name, ptype, config):
    return api("POST", "/providers", {
        "name": name, "type": ptype, "config": json.dumps(config, ensure_ascii=False),
        "console": "", "proxy": "", "error_matcher": "",
    })["ID"]


def make_model(name, prefer_direct=None, max_retry=2, strategy="lottery"):
    body = {"name": name, "remark": "e2e", "max_retry": max_retry, "time_out": 60,
            "strategy": strategy, "breaker": False}
    # 不传 prefer_direct 就是没配过（NULL），正是"老模型"那一档
    if prefer_direct is not None:
        body["prefer_direct"] = prefer_direct
    return api("POST", "/models", body)["ID"]


def link(model_id, provider_id, provider_model, weight=1, tool_call=True, structured_output=True,
         image=True, customer_headers=None, extra_body=None):
    return api("POST", "/model-providers", {
        "model_id": model_id, "provider_id": provider_id, "provider_name": provider_model,
        "tool_call": tool_call, "structured_output": structured_output, "image": image,
        "with_header": False, "customer_headers": customer_headers or {}, "extra_body": extra_body or {},
        "weight": weight, "input_price": 0, "cache_read_price": 0, "output_price": 0, "currency": "USD",
    })["ID"]


# ── 桩上游那一轮：全矩阵 ───────────────────────────────────────────────────


def stub_cases(providers):
    oc, oa = providers["openai"], providers["anthropic"]
    stub_oc = {"provider": oc, "provider_model": "stub-text"}
    stub_oa = {"provider": oa, "provider_model": "stub-text"}
    text = {"messages": [{"role": "user", "content": "你好"}]}
    return [
        # 1 同协议直连：一个字节都不该被改写。桩那边看到的应当与客户端发的一模一样
        dict(name="直连·OpenAI→OpenAI", client="openai", assoc=[stub_oc], body=dict(text, max_tokens=64),
             check=dict(content="stub-text",
                        upstream=dict(path="/openai/chat/completions", model="stub-text",
                                      present=["max_tokens", "messages"], absent=["seed"]),
                        log=dict(style="openai", upstream_style="openai", status="success",
                                 notes_exact=[], prompt_tokens=137, completion_tokens=12))),
        # 2 OpenAI 客户端 → Anthropic 上游。不填 max_tokens：Anthropic 必填，翻译层得补上并记账
        dict(name="转换·OpenAI客户端→Anthropic上游", client="openai", assoc=[stub_oa], body=dict(text),
             check=dict(content="stub-text",
                        upstream=dict(path="/anthropic/messages", model="stub-text",
                                      present=["max_tokens", "messages"], absent=["seed"],
                                      dump=['"content":[{"type":"text"']),
                        log=dict(style="openai", upstream_style="anthropic", status="success",
                                 notes=["defaulted_max_tokens"], prompt_tokens=137, completion_tokens=12))),
        # 3 Anthropic 客户端 → OpenAI 上游
        dict(name="转换·Anthropic客户端→OpenAI上游", client="anthropic", assoc=[stub_oc],
             body={"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64},
             check=dict(content="stub-text",
                        upstream=dict(path="/openai/chat/completions", model="stub-text",
                                      absent=["system", "stop_sequences"]),
                        log=dict(style="anthropic", upstream_style="openai", status="success",
                                 prompt_tokens=137, completion_tokens=12))),
        # 4 流式两个方向：内容增量、收尾恰好一次、用量落在收尾里
        dict(name="流式·OpenAI客户端→Anthropic上游", client="openai", assoc=[stub_oa], stream=True,
             body=dict(text, max_tokens=64, stream=True),
             check=dict(content="stub-text",
                        upstream=dict(path="/anthropic/messages", model="stub-text", present=["stream"]),
                        log=dict(style="openai", upstream_style="anthropic", status="success",
                                 prompt_tokens=137, completion_tokens=12))),
        dict(name="流式·Anthropic客户端→OpenAI上游", client="anthropic", assoc=[stub_oc], stream=True,
             body={"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64, "stream": True},
             check=dict(content="stub-text",
                        # Anthropic 的流自带 usage，OpenAI 的流默认不带：翻译层必须主动要，
                        # 否则这条链路的 token 全记成 0
                        upstream=dict(path="/openai/chat/completions", model="stub-text",
                                      equals={"stream_options": {"include_usage": True}}),
                        log=dict(style="anthropic", upstream_style="openai", status="success",
                                 prompt_tokens=137, completion_tokens=12))),
        # 5 工具调用：响应侧的 tool_calls ↔ tool_use
        dict(name="工具·OpenAI客户端→Anthropic上游", client="openai",
             assoc=[{"provider": oa, "provider_model": "stub-tool"}],
             body={"messages": [{"role": "user", "content": "上海天气"}],
                   "tools": [{"type": "function", "function": {"name": "get_weather", "description": "查天气",
                                                               "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}}],
                   "max_tokens": 64},
             check=dict(content="get_weather",
                        upstream=dict(path="/anthropic/messages", model="stub-tool",
                                      dump=['"input_schema"'], dump_absent=['"parameters"', '"function"']),
                        log=dict(style="openai", upstream_style="anthropic", status="success"))),
        dict(name="工具·Anthropic客户端→OpenAI上游", client="anthropic",
             assoc=[{"provider": oc, "provider_model": "stub-tool"}],
             body={"messages": [{"role": "user", "content": "上海天气"}],
                   "tools": [{"name": "get_weather", "description": "查天气",
                              "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}],
                   "max_tokens": 64},
             check=dict(content="get_weather",
                        upstream=dict(path="/openai/chat/completions", model="stub-tool",
                                      dump=['"type":"function"'], dump_absent=['"input_schema"']),
                        log=dict(style="anthropic", upstream_style="openai", status="success"))),
        # 6 多轮历史：两条 tool 结果必须并进同一条 user 消息（逐条转会被桩以"块关系不对"拒掉）
        dict(name="多轮·并发工具结果并成一条 user", client="openai",
             assoc=[{"provider": oa, "provider_model": "stub-text"}],
             body={"messages": [
                 {"role": "user", "content": "上海和北京的天气"},
                 {"role": "assistant", "content": None, "tool_calls": [
                     {"id": "call_a", "type": "function", "function": {"name": "get_weather", "arguments": '{"city":"上海"}'}},
                     {"id": "call_b", "type": "function", "function": {"name": "get_time", "arguments": '{"tz":"Asia/Shanghai"}'}}]},
                 {"role": "tool", "tool_call_id": "call_a", "content": "晴"},
                 {"role": "tool", "tool_call_id": "call_b", "content": "12:00"},
             ], "max_tokens": 64},
             check=dict(content="stub-text",
                        upstream=dict(path="/anthropic/messages", model="stub-text",
                                      dump_count={'"type":"tool_result"': 2, '"type":"tool_use"': 2},
                                      dump_absent=['"tool_calls"', '"tool_call_id"']),
                        log=dict(style="openai", upstream_style="anthropic", status="success",
                                 notes_absent=["dropped_orphan_tool_result", "filled_missing_tool_result"]))),
        # 7 图片：data URL 拆成 Anthropic 的 base64 source（桩会校验形状，拆错就 400）
        dict(name="图片·data URL 拆成 base64", client="openai",
             assoc=[{"provider": oa, "provider_model": "stub-text"}],
             body={"messages": [{"role": "user", "content": [
                 {"type": "text", "text": "这是什么"},
                 {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}}]}],
                 "max_tokens": 64},
             check=dict(content="stub-text",
                        upstream=dict(path="/anthropic/messages", model="stub-text",
                                      dump=['"type":"image"', '"type":"base64"',
                                            '"media_type":"image/png"', '"data":"iVBORw0KGgo="']),
                        log=dict(style="openai", upstream_style="anthropic", status="success"))),
        # 8 有损参数：发不出去，但必须记账（原则 3：不静默）
        dict(name="记账·seed/user 丢掉要留痕", client="openai",
             assoc=[{"provider": oa, "provider_model": "stub-text"}],
             body=dict(text, max_tokens=64, seed=7, user="u-1"),
             check=dict(content="stub-text",
                        upstream=dict(path="/anthropic/messages", model="stub-text", absent=["seed", "user"]),
                        log=dict(style="openai", upstream_style="anthropic", status="success",
                                 notes=["dropped_seed", "dropped_user"]))),
        # 9 反向的有损：Anthropic 的 top_k / thinking 在 OpenAI 侧没有对应物
        dict(name="记账·top_k/thinking 丢掉要留痕", client="anthropic",
             assoc=[{"provider": oc, "provider_model": "stub-text"}],
             body={"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64, "top_k": 5,
                   "thinking": {"type": "enabled", "budget_tokens": 1024}},
             check=dict(content="stub-text",
                        upstream=dict(path="/openai/chat/completions", model="stub-text",
                                      absent=["top_k", "thinking"]),
                        log=dict(style="anthropic", upstream_style="openai", status="success",
                                 notes=["dropped_top_k", "dropped_thinking"]))),
        # 10 翻不过去的：结构化输出在 Anthropic 侧没有表示法 —— 不能装作答应了
        dict(name="拒绝·结构化输出", client="openai", assoc=[{"provider": oa, "provider_model": "stub-text"}],
             body=dict(text, max_tokens=64, response_format={"type": "json_schema", "json_schema": {"name": "r", "schema": {"type": "object"}}}),
             check=dict(shape="error", upstream=dict(none=True),
                        log=dict(style="openai", status="error"))),
        # 11 响应侧的有损：content_filter 只能记成 refusal
        dict(name="记账·content_filter 降级成 refusal", client="openai",
             assoc=[{"provider": oa, "provider_model": "stub-filter"}],
             body=dict(text, max_tokens=64),
             check=dict(content="被过滤",
                        upstream=dict(path="/anthropic/messages", model="stub-filter"),
                        log=dict(style="openai", upstream_style="anthropic", status="success",
                                 notes=["content_filtered"]))),
        # 12 响应侧的丢弃：OpenAI 的 reasoning_content 在 Anthropic 侧没有对应块
        dict(name="记账·reasoning 丢弃", client="anthropic",
             assoc=[{"provider": oc, "provider_model": "stub-reason"}],
             body={"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64},
             check=dict(content="答案是 42",
                        upstream=dict(path="/openai/chat/completions", model="stub-reason"),
                        log=dict(style="anthropic", upstream_style="openai", status="success",
                                 notes=["dropped_reasoning"]))),
        # 13 认不出的 stop_reason 不能静默当成正常结束
        dict(name="记账·认不出的 stop_reason", client="openai",
             assoc=[{"provider": oa, "provider_model": "stub-pause"}],
             body=dict(text, max_tokens=64),
             check=dict(content="停一下",
                        upstream=dict(path="/anthropic/messages", model="stub-pause"),
                        log=dict(style="openai", upstream_style="anthropic", status="success",
                                 notes=["paused_turn"]))),
        # 14 上游给坏消息时换一家：第一家 400，第二家正常，两条日志都在。
        # 权重压倒性偏向坏的那家，是为了让"先撞上它"成为必然——否则这一发
        # 有一半概率直接命中好的那家，重试路径根本没被走到
        dict(name="重试·上游 400 换一家", client="openai", max_retry=3,
             assoc=[{"provider": oa, "provider_model": "stub-boom", "weight": 100000}, {"provider": oa, "provider_model": "stub-text"}],
             body=dict(text, max_tokens=64),
             check=dict(content="stub-text",
                        upstream=dict(path="/anthropic/messages"),
                        log=dict(style="openai", upstream_style="anthropic", status="success"),
                        also_error_log=True)),
    ]


def stub_topology_cases(providers):
    """候选池那三条：直连优先、老模型按开、关掉之后整池按权重。"""
    oc, oa = providers["openai"], providers["anthropic"]
    both = [{"provider": oc, "provider_model": "stub-text"}, {"provider": oa, "provider_model": "stub-text"}]
    body = {"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64}
    return [
        dict(name="候选池·直连优先（开着）", client="openai", prefer_direct=True, assoc=both, body=dict(body),
             check=dict(content="stub-text",
                        upstream=dict(path="/openai/chat/completions", model="stub-text"),
                        log=dict(style="openai", upstream_style="openai", status="success"))),
        dict(name="候选池·老模型（没配过）按开处理", client="openai", prefer_direct=None, assoc=both, body=dict(body),
             check=dict(content="stub-text",
                        upstream=dict(path="/openai/chat/completions", model="stub-text"),
                        log=dict(style="openai", upstream_style="openai", status="success"))),
        # 关掉开关：本协议与可转换协议的上游一起参与。单发撞上谁都是合法的，
        # 所以连打多次看两边是不是都露过面
        dict(name="候选池·关掉开关后整池按权重", client="openai", prefer_direct=False, assoc=both,
             body=dict(body), repeat=40,
             check=dict(shape="either", log=dict(style="openai", status="success"))),
    ]


MARK = {"ok": "✓", "fail": "✗", "skip": "-"}


def record(results, label, verdict, detail=""):
    """verdict 取 ok / fail / skip。

    skip 单独算：真上游不支持某件事（比如某个模型不认工具）不等于 llmio 翻译错了，
    但也不能悄悄算成通过——跳过要看得见。
    """
    results.append((label, verdict, detail))
    print(f"  {MARK[verdict]} {label}" + ("" if verdict == "ok" else f"\n      {detail}"))


def run_stub_matrix(llmio, stub):
    results = []

    providers = {
        "openai": make_provider("stub-openai", "openai", {"base_url": f"http://127.0.0.1:{STUB_PORT}/openai", "api_key": "stub-key"}),
        "anthropic": make_provider("stub-anthropic", "anthropic",
                                   {"base_url": f"http://127.0.0.1:{STUB_PORT}/anthropic", "api_key": "stub-key",
                                    "version": "2023-06-01"}),
    }

    cases = stub_cases(providers) + stub_topology_cases(providers)

    for i, case in enumerate(cases, 1):
        label = f"{i:02d} {case['name']}"
        model = f"e2e-{i:02d}"
        try:
            model_id = make_model(model, prefer_direct=case.get("prefer_direct", True), max_retry=case.get("max_retry", 2))
            for assoc in case["assoc"]:
                link(model_id, assoc["provider"], assoc["provider_model"], weight=assoc.get("weight", 1),
                     tool_call=assoc.get("tool_call", True), structured_output=assoc.get("structured_output", True),
                     image=assoc.get("image", True))
            body = dict(case["body"])
            body["model"] = model

            repeats = case.get("repeat", 1)
            landed = set()
            status = raw = None
            for _ in range(repeats):
                status, raw = chat(case["client"], CLIENT_PATH[case["client"]], body,
                                   client_headers(case["client"]), stream=case.get("stream", False))
                got = stub.last()
                if got:
                    landed.add(got["path"].rsplit("/", 1)[-1] + "|" + str(got["body"].get("model")))

            check_shape(case["client"], case["check"], status, raw, case.get("stream", False), label)
            if repeats > 1:
                # 开关关掉之后，本协议与可转换协议的上游都得有机会被选中
                need(len(landed) == 2, f"{label}: {repeats} 发只落在 {landed}，整池并没有一起参与")
            elif case["check"].get("upstream"):
                check_upstream(stub.last(), case["check"]["upstream"], label)
            if case["check"].get("log"):
                spec = case["check"]["log"]
                check_log(wait_log(model, spec.get("status")), spec, label)
            if case["check"].get("also_error_log"):
                # 失败的那一次得留下自己的记录：排查重试时看的就是它
                check_log(wait_log(model, "error"), dict(status="error", style=case["client"]), label)
                need(len(api("GET", f"/logs?name={model}&page=1&page_size=10")["data"]) == 2,
                     f"{label}: 第一次尝试的失败记录没落库")
            record(results, label, "ok")
        except Failure as e:
            record(results, label, "fail", str(e))
        except Exception as e:  # noqa: BLE001
            record(results, label, "fail", f"{type(e).__name__}: {e}")

    return results


def check_shape(client, spec, status, raw, stream, label):
    # 客户端拿到什么形状，取决于**客户端**协议，与上游无关——这正是这条链路的承诺。
    # 所以形状是推出来的而不是逐条写死的：写死过一次，十二条用例的方向全写反了
    shape = spec.get("shape") or (f"sse-{client}" if stream else client)
    if shape == "error":
        need(status != 200, f"{label}: 该拒绝的请求却成功了：{raw[:200]}")
        return
    need(status == 200, f"{label}: HTTP {status}：{raw[:400]}")
    if shape == "either":
        return
    if shape == "openai":
        payload = json.loads(raw)
        need("choices" in payload, f"{label}: OpenAI 客户端没拿到 choices：{raw[:200]}")
        need("content" not in payload, f"{label}: OpenAI 客户端拿到了 Anthropic 的 content 块")
    elif shape == "anthropic":
        payload = json.loads(raw)
        need(payload.get("type") == "message", f"{label}: Anthropic 客户端没拿到 message：{raw[:200]}")
        need(isinstance(payload.get("content"), list), f"{label}: Anthropic 客户端拿到的 content 不是块数组")
        need(payload.get("stop_reason"), f"{label}: Anthropic 客户端拿到的响应没有 stop_reason")
        need("choices" not in payload, f"{label}: Anthropic 客户端拿到了 OpenAI 的 choices")
    elif shape in ("sse-openai", "sse-anthropic"):
        # 翻译层会把 JSON 重新编码，空格全没了，比对前先压平
        compact = re.sub(r"\s+", "", raw)
        need("data:" in raw, f"{label}: 流式响应里没有 data: 行：{raw[:200]}")
        if shape == "sse-openai":
            need('"choices"' in compact, f"{label}: OpenAI 客户端没拿到 choices 增量")
            need("data:[DONE]" in compact, f"{label}: 流没有以 [DONE] 收尾")
            need('"role":"assistant"' in compact, f"{label}: 流里没有开头的 role 增量")
            need('"prompt_tokens":137' in compact, f"{label}: 收尾 chunk 没带上用量")
        else:
            need("event:message_start" in compact, f"{label}: 流里没有 message_start")
            need("event:content_block_delta" in compact, f"{label}: 流里没有内容增量")
            need('"input_tokens":137' in compact, f"{label}: message_delta 没带上用量")
            need("event:message_stop" in compact, f"{label}: 流没有以 message_stop 收尾")
            need(compact.count("event:message_stop") == 1, f"{label}: message_stop 发了不止一次")
            need(compact.count("event:message_delta") == 1, f"{label}: message_delta 发了不止一次")
        if spec.get("content"):
            need(spec["content"] in compact, f"{label}: 流里没有 {spec['content']}")
        return
    if spec.get("content"):
        need(spec["content"] in text_of(client, raw), f"{label}: 客户端拿到的回答里没有 {spec['content']}：{raw[:200]}")


def main():
    # 控制台是 GBK 时中文输出会变成乱码，先把它掰成 UTF-8
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    ap = argparse.ArgumentParser()
    ap.add_argument("--upstream", choices=["stub", "opencode"], default="stub")
    args = ap.parse_args()

    workdir = os.path.join(RUN_DIR, f"work-{args.upstream}")
    shutil.rmtree(workdir, ignore_errors=True)
    os.makedirs(workdir, exist_ok=True)

    print("→ 构建二进制")
    subprocess.run(["go", "build", "-o", BIN, "."], cwd=REPO, check=True)

    stub = Stub(workdir) if args.upstream == "stub" else None
    if stub:
        stub.start()
        print(f"→ 桩上游 127.0.0.1:{STUB_PORT}")
    else:
        need(os.path.exists(KEY_FILE), f"缺少 {KEY_FILE}")
        print(f"→ 真实上游 {OPENCODE_BASE}")

    llmio = Llmio(workdir)
    try:
        llmio.start()
        print(f"→ llmio {BASE}（工作目录 {workdir}）")
        if args.upstream == "stub":
            results = run_stub_matrix(llmio, stub)
        else:
            results = run_opencode_matrix(llmio)
    finally:
        llmio.stop()
        if stub:
            stub.stop()

    ok = sum(1 for _, verdict, _ in results if verdict == "ok")
    skipped = sum(1 for _, verdict, _ in results if verdict == "skip")
    total = len(results) - skipped
    print(f"\n{ok}/{total} 通过" + (f"（另有 {skipped} 条跳过）" if skipped else ""))
    return 0 if ok == total else 1


# ── 真机那一轮：Opencode 小矩阵 ────────────────────────────────────────────
#
# 与桩矩阵的分工：桩负责把翻译层的每一种花样都走一遍（两个方向、流式、工具、
# 图片、拒答、重试、候选池），这里只回答一个问题——**真上游收不收我们发出去的东西**。
# 所以断言集中在三处：客户端拿到的是客户端协议的形状、日志把方向记对了、上游报的
# 用量真的被记了下来。字段级的花样留给桩。

# 免费且不限量（见 docs 链接里的 Go 端点定价），两个端点都收这个模型
FREE_MODEL = "space-bunny-free"
# 真上游不爱等人：提示语要短，max_tokens 给够但要封顶，免得一发跑出几千 token
REAL_MAX_TOKENS = 256
REAL_PROMPT = "只回两个字：收到"


def opencode_key():
    # 凭据只从文件读，不进源码、不进命令行参数、不回显
    with open(KEY_FILE, encoding="utf-8") as f:
        return f.read().strip()


def check_real_response(client, raw, label):
    """真上游那一轮的形状检查：只看**客户端协议**的形状是否成立。

    桩矩阵里有 `content` 之类的确定性断言可用；真上游的回答是模型现编的，能断言的
    只有"这确实是一份客户端协议的响应，而且回答不是空的"。
    """
    payload = json.loads(raw)
    if client == "anthropic":
        need(payload.get("type") == "message", f"{label}: Anthropic 客户端没拿到 message：{raw[:300]}")
        need(isinstance(payload.get("content"), list) and payload["content"],
             f"{label}: Anthropic 客户端拿到的 content 不是块数组或为空：{raw[:300]}")
        need(payload.get("stop_reason"), f"{label}: Anthropic 客户端拿到的响应没有 stop_reason：{raw[:300]}")
        need("choices" not in payload, f"{label}: Anthropic 客户端拿到了 OpenAI 的 choices")
    else:
        need("choices" in payload, f"{label}: OpenAI 客户端没拿到 choices：{raw[:300]}")
        need(payload["choices"][0].get("finish_reason"), f"{label}: 响应里没有 finish_reason：{raw[:300]}")
        need("content" not in payload, f"{label}: OpenAI 客户端拿到了 Anthropic 的 content 块")
    need(text_of(client, raw).strip(), f"{label}: 客户端拿到的是空回答：{raw[:300]}")


def check_real_sse(client, raw, label):
    compact = re.sub(r"\s+", "", raw)
    need("data:" in raw, f"{label}: 流式响应里没有 data: 行：{raw[:200]}")
    if client == "anthropic":
        need("event:message_start" in compact, f"{label}: 流里没有 message_start")
        need("event:content_block_delta" in compact, f"{label}: 流里没有内容增量（真上游的内容没被翻过来）")
        need("event:message_stop" in compact, f"{label}: 流没有以 message_stop 收尾")
        need(compact.count("event:message_stop") == 1, f"{label}: message_stop 发了不止一次")
        need('"stop_reason"' in compact, f"{label}: message_delta 里没有 stop_reason")
    else:
        need('"choices"' in compact, f"{label}: OpenAI 客户端没拿到 choices 增量")
        need("data:[DONE]" in compact, f"{label}: 流没有以 [DONE] 收尾")


def check_log_tokens(log, label):
    """上游报的用量有没有被记下来。

    这一条只有真上游能验：桩里的 usage 是我们自己编的常数，记成 0 也看不出来；
    真上游的 usage 得靠 llmio 从响应（流式的收尾事件）里解析出来。
    """
    for key in ("prompt_tokens", "completion_tokens"):
        got = log.get(key) or 0
        need(got > 0, f"{label}: 日志里的 {key}={got}，上游报的用量没记下来")


def opencode_cases(providers):
    oc, oa = providers["openai"], providers["anthropic"]
    to_oc = [{"provider": oc, "provider_model": FREE_MODEL}]
    to_oa = [{"provider": oa, "provider_model": FREE_MODEL}]
    text = {"messages": [{"role": "user", "content": REAL_PROMPT}], "max_tokens": REAL_MAX_TOKENS}
    return [
        # 1 直连：同一个协议，翻译层不该插手。真上游的响应形状与 llmio 的不完全一样
        # （多出来的字段会被原样透传），这里只确认客户端拿到了自己认识的东西
        dict(name="直连·OpenAI 客户端→Opencode OpenAI 端点", client="openai", assoc=to_oc,
             body=dict(text), want_text=True,
             log=dict(style="openai", upstream_style="openai", status="success")),
        # 2 非流式·Anthropic 客户端→OpenAI 端点：请求与响应都得翻
        dict(name="转换·Anthropic 客户端→Opencode OpenAI 端点", client="anthropic", assoc=to_oc,
             body=dict(text), want_text=True,
             log=dict(style="anthropic", upstream_style="openai", status="success")),
        # 3 流式同上：收尾事件与用量只有读完流才会发生，所以 chat() 会把整条流读干
        dict(name="流式·Anthropic 客户端→Opencode OpenAI 端点", client="anthropic", assoc=to_oc,
             body=dict(text), stream=True, want_text=False,
             log=dict(style="anthropic", upstream_style="openai", status="success")),
        # 4 反方向，且**故意不填 max_tokens**：OpenAI 客户端可以省，Anthropic 必填，
        # 翻译层补默认值这件事只有真上游拒收才算证伪。上游收下（200）就说明补上了
        dict(name="转换·OpenAI 客户端→Opencode Anthropic 端点（max_tokens 由翻译层补）",
             client="openai", assoc=to_oa,
             body={"messages": [{"role": "user", "content": REAL_PROMPT}]}, want_text=True,
             log=dict(style="openai", upstream_style="anthropic", status="success")),
        # 5 拒绝：结构化输出在 Anthropic 协议里表达不了，该在翻译层被拦下——一个字节都
        # 不该发给上游。这一发是零成本的，因为它根本到不了上游
        dict(name="拒绝·结构化输出在 Anthropic 端点上翻不过去", client="openai", assoc=to_oa,
             body={"messages": [{"role": "user", "content": REAL_PROMPT}], "max_tokens": 32,
                   "response_format": {"type": "json_schema"}},
             shape="error", log=dict(status="error")),
    ]


def run_opencode_matrix(llmio):
    results = []
    # 真上游强制会话头，缺了直接 400（见 docs/session-header-injection-design.md）。
    # 关联行上配一次，两个方向的请求都会带上——这也顺带验了自定义头的模板求值
    headers = dict(OPENCODE_HEADERS)
    providers = {
        "openai": make_provider("opencode-openai", "openai",
                                {"base_url": OPENCODE_BASE, "api_key": opencode_key()}),
        "anthropic": make_provider("opencode-anthropic", "anthropic",
                                   {"base_url": OPENCODE_BASE, "api_key": opencode_key(),
                                    "version": "2023-06-01"}),
    }

    for i, case in enumerate(opencode_cases(providers), 1):
        label = f"{i:02d} {case['name']}"
        model = f"e2e-real-{i:02d}"
        try:
            # 真上游只试一次：重试等于再花一次钱，而这里要验的是"发出去的东西对不对"，
            # 一次就够（max_retry 是尝试次数，1 = 只发一发）
            model_id = make_model(model, prefer_direct=case.get("prefer_direct", True), max_retry=1)
            for assoc in case["assoc"]:
                link(model_id, assoc["provider"], assoc["provider_model"], weight=assoc.get("weight", 1),
                     customer_headers=headers)
            body = dict(case["body"])
            body["model"] = model
            # stream 是**请求体**里的开关；HTTP 头上的 Accept 只是给中间层的提示，两者
            # 各写一遍迟早会写岔（漏写体里那一个，客户端就会拿到完整 JSON 而不是流）。
            # 所以以用例的 stream 为准，体里的这一份由它推出来
            if case.get("stream"):
                body["stream"] = True
            status, raw = chat(case["client"], CLIENT_PATH[case["client"]], body,
                               client_headers(case["client"]), stream=case.get("stream", False), timeout=180)

            if case.get("shape") == "error":
                need(status != 200, f"{label}: 该拒绝的请求却成功了：{raw[:300]}")
            else:
                need(status == 200, f"{label}: HTTP {status}：{raw[:400]}")
                if case.get("stream"):
                    check_real_sse(case["client"], raw, label)
                elif case.get("want_text"):
                    check_real_response(case["client"], raw, label)

            spec = case["log"]
            log = wait_log(model, spec.get("status"))
            check_log(log, spec, label)
            if spec.get("status") == "success":
                check_log_tokens(log, label)
                # 转过协议就得在日志里看得见：直连时两者相同，是它们的差把"这次转了"说出来
                need(log.get("upstream_style") == spec["upstream_style"],
                     f"{label}: 上游协议记成 {log.get('upstream_style')!r}，期望 {spec['upstream_style']!r}")
            record(results, label, "ok")
        except Failure as e:
            record(results, label, "fail", str(e))
        except Exception as e:  # noqa: BLE001
            record(results, label, "fail", f"{type(e).__name__}: {e}")

    return results


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""请求体参数自愈的验收车。

复用 run_matrix 的机器（桩上游、独立二进制、管理 API 建档、三处核对），用例全部
围绕桩的 picky 家族（见 stub_upstream.py）：

- **S 组（应自愈）**：断言的是 docs/plans/usage-insights-plan.md §3 **实施后**的行为
  ——同家删参重发成功、失败行与成功行成对出现、bridge_notes 带 self_heal_dropped
  短码、Retry 把自愈次数计入。实现落地之前这组是红的——红就是"待办"本身，不是
  脚本写错了；落地之后必须全绿。
- **N 组（判据边界）**：类型错 / 点名没发过的参数 / 保护字段 / 上下文超长，一概
  **不该**自愈，行为与没有这个功能时完全一致（客户端 500、上游只收到一发、一条
  400 失败行、记账里没有 self_heal 短码）。这组在实现前后都必须绿——它绿着的时候
  防的是"把不该删的也删了"。

三处核对与 run_matrix 同一纪律：客户端拿到什么 / 上游收到什么 / 日志记了什么，
缺一不可。只看客户端 200 的话，"删了参数重发成功"与"换了一家凑巧成功"长得一样。

用法：
    python e2e/selfheal_check.py
"""

import json
import os
import re
import shutil
import subprocess
import sys
import time

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, E2E_DIR)
import run_matrix as rm  # noqa: E402

WORKDIR = os.path.join(rm.RUN_DIR, "selfheal")
BIN = os.path.join(rm.RUN_DIR, "llmio-selfheal.exe")


# ── 查询小件 ───────────────────────────────────────────────────────────────


def wait_rows(alias, min_count, timeout=10):
    """等某请求名的日志至少落满 min_count 行。失败行经 channel 异步落库，
    success 行要等响应读完才补状态，所以只能轮询。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        rows = fetch_rows(alias)
        if len(rows) >= min_count:
            break
        time.sleep(0.2)
    # 行数只会增：刚到 min_count 就断言可能漏掉还在路上的那行，等半秒读终值
    time.sleep(0.5)
    return fetch_rows(alias)


def fetch_rows(alias):
    return rm.api("GET", f"/logs?name={alias}&page=1&page_size=20")["data"] or []


def sent_bodies(stub, provider_model):
    """桩收到的、发给指定"提供商模型"的全部请求。"""
    return [r for r in stub.received() if r["body"].get("model") == provider_model]


def notes_of(log):
    return [c for c in (log.get("bridge_notes") or "").split(",") if c]


# ── 用例 ───────────────────────────────────────────────────────────────────


def build_cases(oc, oa):
    text = {"messages": [{"role": "user", "content": "你好"}]}

    # provider_model 不写死：用例别名做前缀 + 行为后缀（sh-01-picky-openai）。
    # 桩按后缀识别行为，jsonl 按全名过滤——两家用例共用同一种行为也不会互相混入
    def oc_a(suffix, weight=1):
        return {"provider": oc, "suffix": suffix, "weight": weight}

    def oa_a(suffix, weight=1):
        return {"provider": oa, "suffix": suffix, "weight": weight}

    return [
        # ── S 组：应自愈 ──
        dict(
            name="S1 同协议透传·OpenAI 兼容短语",
            client="openai", assoc=[oc_a("picky-openai")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(
                client_ok=True,
                upstream=dict(path="/openai/chat/completions", count=2,
                              first_has=["top_k"], last_absent=["top_k"],
                              last_has=["messages", "max_tokens"]),
                rows=2, err400=1, error_phrase="Unrecognized request argument",
                success_notes=["self_heal_dropped:top_k"], success_retry=1,
            ),
        ),
        dict(
            name="S2 同协议透传·Pydantic 短语",
            client="openai", assoc=[oc_a("picky-py")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(
                client_ok=True,
                upstream=dict(path="/openai/chat/completions", count=2, last_absent=["top_k"]),
                rows=2, err400=1, error_phrase="Extra inputs are not permitted",
                success_notes=["self_heal_dropped:top_k"], success_retry=1,
            ),
        ),
        dict(
            name="S3 同协议透传·Unknown parameter 短语",
            client="openai", assoc=[oc_a("picky-unknown")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(
                client_ok=True,
                upstream=dict(path="/openai/chat/completions", count=2, last_absent=["top_k"]),
                rows=2, err400=1, error_phrase="Unknown parameter",
                success_notes=["self_heal_dropped:top_k"], success_retry=1,
            ),
        ),
        dict(
            name="S4 桥接·转换器搬过去的 top_k（Anthropic 短语）",
            client="openai", assoc=[oa_a("picky-unsupported")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(
                client_ok=True,
                upstream=dict(path="/anthropic/messages", count=2,
                              first_has=["top_k"], last_absent=["top_k"],
                              last_has=["messages", "max_tokens"]),
                rows=2, err400=1, error_phrase="Unsupported parameter",
                success_notes=["self_heal_dropped:top_k"], success_retry=1,
                upstream_style="anthropic", prompt_tokens=137,
            ),
        ),
        dict(
            name="S5 桥接·转换器注入的 stream_options（流式）",
            client="anthropic", assoc=[oc_a("picky-injected")], stream=True,
            body={"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64, "stream": True},
            expect=dict(
                client_ok=True,
                upstream=dict(path="/openai/chat/completions", count=2,
                              first_has=["stream_options"], last_absent=["stream_options"],
                              last_has=["stream"]),
                rows=2, err400=1, error_phrase="stream_options",
                success_notes=["self_heal_dropped:stream_options"], success_retry=1,
                upstream_style="openai",
                # 自愈的代价在这里可见：stream_options 被删后上游不再报用量，
                # 这条成功行的 token 记 0——比整单失败好，但不是免费的
                prompt_tokens=0,
            ),
        ),
        dict(
            name="S6 链式两参·连续自愈",
            client="openai", assoc=[oc_a("picky-chain")],
            body=dict(text, max_tokens=64, top_k=40, logit_bias={"a": 1}),
            expect=dict(
                client_ok=True,
                upstream=dict(path="/openai/chat/completions", count=3,
                              last_absent=["top_k", "logit_bias"]),
                rows=3, err400=2, error_phrase="Unrecognized request argument",
                success_notes=["self_heal_dropped:top_k", "self_heal_dropped:logit_bias"],
                success_retry=2,
            ),
        ),
        dict(
            name="S7 预算耗尽·四个参数只删得动三个",
            client="openai", assoc=[oc_a("picky-chain")],
            body=dict(text, max_tokens=64, top_k=40, logit_bias={"a": 1},
                      frequency_penalty=0.5, presence_penalty=0.5),
            expect=dict(
                client_ok=False,
                upstream=dict(path="/openai/chat/completions", count=4,
                              last_absent=["top_k", "logit_bias", "frequency_penalty"],
                              last_has=["presence_penalty"]),
                rows=4, err400=4, error_phrase="Unrecognized request argument",
                any_error_note=["self_heal_dropped:top_k"],
            ),
        ),
        dict(
            name="S8 换家对照·自愈优先在同家完成",
            client="openai",
            assoc=[oc_a("picky-openai", weight=100000),
                   {"provider": oc, "provider_model": "stub-text", "weight": 1}],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(
                client_ok=True,
                upstream=dict(path="/openai/chat/completions", count=2, last_absent=["top_k"]),
                absent_pm=["stub-text"],
                rows=2, err400=1, error_phrase="Unrecognized request argument",
                success_notes=["self_heal_dropped:top_k"], success_retry=1,
            ),
        ),
        # ── N 组：判据边界，一律不自愈，实现前后都必须绿 ──
        dict(
            name="N1 边界·值类型错不删参",
            client="openai", assoc=[oc_a("picky-type")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(client_ok=False, no_heal=True,
                        upstream=dict(path="/openai/chat/completions", count=1),
                        rows=2, err400=1, error_phrase="Invalid type"),
        ),
        dict(
            name="N2 边界·点名没发过的参数不删参",
            client="openai", assoc=[oc_a("picky-ghost")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(client_ok=False, no_heal=True,
                        upstream=dict(path="/openai/chat/completions", count=1),
                        rows=2, err400=1, error_phrase="logit_bias"),
        ),
        dict(
            name="N3 边界·保护字段（messages）不删参",
            client="openai", assoc=[oc_a("picky-protect")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(client_ok=False, no_heal=True,
                        upstream=dict(path="/openai/chat/completions", count=1),
                        rows=2, err400=1, error_phrase="'messages'"),
        ),
        dict(
            name="N4 边界·上下文超长不删参",
            client="openai", assoc=[oc_a("picky-ctx")],
            body=dict(text, max_tokens=64, top_k=40),
            expect=dict(client_ok=False, no_heal=True,
                        upstream=dict(path="/openai/chat/completions", count=1),
                        rows=2, err400=1, error_phrase="maximum context length"),
        ),
    ]


# ── 三处核对 ───────────────────────────────────────────────────────────────


def check_client(case, exp, status, raw):
    client = case["client"]
    if exp.get("client_ok"):
        rm.need(status == 200, f"HTTP {status}：{raw[:300]}")
        if case.get("stream"):
            compact = re.sub(r"\s+", "", raw)
            if client == "anthropic":
                rm.need("event:message_start" in compact, "Anthropic 客户端没拿到 message_start")
                rm.need("event:message_stop" in compact, "流没有以 message_stop 收尾")
            else:
                rm.need('"choices"' in compact, "OpenAI 客户端没拿到 choices 增量")
                rm.need("data:[DONE]" in compact, "流没有以 [DONE] 收尾")
        else:
            payload = json.loads(raw)
            if client == "openai":
                rm.need("choices" in payload, f"OpenAI 客户端没拿到 choices：{raw[:200]}")
            else:
                rm.need(payload.get("type") == "message", f"Anthropic 客户端没拿到 message：{raw[:200]}")
    else:
        rm.need(status != 200, f"该失败的请求却成功了：{raw[:200]}")


def check_upstream(exp, stub, primary_pm):
    up = exp.get("upstream")
    if not up:
        return
    got = sent_bodies(stub, up.get("pm", primary_pm))
    rm.need(len(got) == up["count"], f"上游收到 {len(got)} 发（{up.get('pm', primary_pm)}），期望 {up['count']}")
    for r in got:
        rm.need(r["path"].endswith(up["path"]), f"上游路径 {r['path']}，期望以 {up['path']} 结尾")
    for key in up.get("first_has", []):
        rm.need(key in got[0]["body"], f"第一发上游请求里没有 {key}——用例前提不成立")
    last = got[-1]["body"]
    for key in up.get("last_has", []):
        rm.need(key in last, f"最后一发上游请求里没有 {key}")
    for key in up.get("last_absent", []):
        rm.need(key not in last, f"最后一发上游请求里仍带着 {key}——参数没有被删掉")
    for pm in exp.get("absent_pm", []):
        n = len(sent_bodies(stub, pm))
        rm.need(n == 0, f"{pm} 被打了 {n} 发——自愈本该在同一家完成，不该落到备胎上")


def check_logs(exp, alias):
    if "rows" not in exp:
        return
    rows = wait_rows(alias, exp["rows"])
    succ = [r for r in rows if r["Status"] == "success"]
    errs = [r for r in rows if r["Status"] == "error"]
    if exp.get("client_ok"):
        rm.need(len(succ) == 1, f"成功行 {len(succ)} 条（共 {len(rows)} 行），期望恰 1 条")
        s = succ[0]
        for code in exp.get("success_notes", []):
            rm.need(code in notes_of(s), f"成功行记账缺 {code}（实际 {notes_of(s)}）")
        if "success_retry" in exp:
            rm.need(s.get("Retry") == exp["success_retry"],
                    f"成功行 Retry={s.get('Retry')}，期望 {exp['success_retry']}（自愈次数应计入）")
        if "upstream_style" in exp:
            rm.need(s.get("upstream_style") == exp["upstream_style"],
                    f"上游协议记成 {s.get('upstream_style')!r}，期望 {exp['upstream_style']!r}")
        if "prompt_tokens" in exp:
            rm.need(s.get("prompt_tokens") == exp["prompt_tokens"],
                    f"成功行 prompt_tokens={s.get('prompt_tokens')}，期望 {exp['prompt_tokens']}")
    else:
        rm.need(not succ, "不该出现成功行")
    n400 = sum(1 for r in errs if "status: 400" in (r.get("Error") or ""))
    if "err400" in exp:
        rm.need(n400 == exp["err400"], f"400 失败行 {n400} 条，期望 {exp['err400']}")
    if "error_phrase" in exp:
        rm.need(any(exp["error_phrase"] in (r.get("Error") or "") for r in errs),
                f"失败行原文里没有 {exp['error_phrase']!r}——上游报错必须原样落库")
    for code in exp.get("any_error_note", []):
        rm.need(any(code in notes_of(r) for r in errs), f"失败行记账里没有 {code}")
    if exp.get("no_heal"):
        for r in rows:
            codes = notes_of(r)
            rm.need(not any(c.startswith("self_heal_dropped") for c in codes),
                    f"不该自愈却记了 {codes}")


# ── 主流程 ─────────────────────────────────────────────────────────────────


def run_all(stub, oc, oa):
    results = []
    for i, case in enumerate(build_cases(oc, oa), 1):
        label = f"{case['name']}"
        alias = f"sh-{i:02d}"
        try:
            model_id = rm.make_model(alias, prefer_direct=True, max_retry=case.get("max_retry", 2))
            primary_pm = None
            for a in case["assoc"]:
                pm = a.get("provider_model") or f"{alias}-{a['suffix']}"
                rm.link(model_id, a["provider"], pm, weight=a.get("weight", 1))
                if primary_pm is None:
                    primary_pm = pm
            body = dict(case["body"])
            body["model"] = alias
            status, raw = rm.chat(case["client"], rm.CLIENT_PATH[case["client"]], body,
                                  rm.client_headers(case["client"]), stream=case.get("stream", False))
            exp = case["expect"]
            check_client(case, exp, status, raw)
            check_upstream(exp, stub, primary_pm)
            check_logs(exp, alias)
            rm.record(results, label, "ok")
        except rm.Failure as e:
            rm.record(results, label, "fail", str(e))
        except Exception as e:  # noqa: BLE001
            rm.record(results, label, "fail", f"{type(e).__name__}: {e}")
    return results


def main():
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    shutil.rmtree(WORKDIR, ignore_errors=True)
    os.makedirs(WORKDIR, exist_ok=True)

    print("→ 构建二进制")
    rm.BIN = BIN
    subprocess.run(["go", "build", "-o", BIN, "."], cwd=rm.REPO, check=True)

    stub = rm.Stub(WORKDIR)
    stub.start()
    print(f"→ 桩上游 127.0.0.1:{rm.STUB_PORT}（picky 家族）")
    llmio = rm.Llmio(WORKDIR)
    results = []
    try:
        llmio.start()
        print(f"→ llmio {rm.BASE}（工作目录 {WORKDIR}）")
        oc = rm.make_provider("selfheal-openai", "openai",
                              {"base_url": f"http://127.0.0.1:{rm.STUB_PORT}/openai", "api_key": "stub-key"})
        oa = rm.make_provider("selfheal-anthropic", "anthropic",
                              {"base_url": f"http://127.0.0.1:{rm.STUB_PORT}/anthropic", "api_key": "stub-key",
                               "version": "2023-06-01"})
        results = run_all(stub, oc, oa)
    finally:
        llmio.stop()
        stub.stop()

    s = [r for r in results if r[0].startswith("S")]
    n = [r for r in results if r[0].startswith("N")]
    s_ok = sum(1 for _, v, _ in s if v == "ok")
    n_ok = sum(1 for _, v, _ in n if v == "ok")
    print(f"\n应自愈（S 组，实现落地前预期为红）：{s_ok}/{len(s)}")
    print(f"判据边界（N 组，任何时候都必须绿）：{n_ok}/{len(n)}")
    return 0 if s_ok + n_ok == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())

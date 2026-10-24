#!/usr/bin/env python3
"""备用关联降级的验收车。

复用 run_matrix 的机器（桩上游、独立二进制、管理 API 建档、三处核对），用例：

- **B1 主挂备好**：主关联（boom，每发必 400 被移出候选）打完一轮后由备用
  （text）接管——客户端拿到 200、成功行的 ProviderModel 是备用的、
  chat_logs.Backup=1（日志页必须看得出这次降级）。
- **B2 主好备零请求**：主关联健康时备用一次都不该被碰——"备用"的全部语义
  就在这一条里。
- **B3 主备全挂**：连备用也挂掉时客户端错误与落库的收尾错误都要带上
  "backup providers exhausted"，否则和「主关联本身就不够用」无从区分。

三处核对与 run_matrix 同一纪律：客户端拿到什么 / 上游收到什么 / 日志记了什么。
只看客户端 200 的话，"备用接管成功"与"凑巧命中另一家"长得一样。

用法：
    python e2e/backup_check.py
"""

import json
import os
import shutil
import subprocess
import sys
import time

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, E2E_DIR)
import run_matrix as rm  # noqa: E402

WORKDIR = os.path.join(rm.RUN_DIR, "backup")
BIN = os.path.join(rm.RUN_DIR, "llmio-backup.exe")


# ── 建档与查询小件 ─────────────────────────────────────────────────────────


def link_assoc(model_id, provider_id, provider_model, weight=1, backup=False):
    """建一条关联；backup 只在需要时发送——不传即 false（老客户端形态）。"""
    body = {
        "model_id": model_id, "provider_id": provider_id, "provider_name": provider_model,
        "tool_call": True, "structured_output": True, "image": True,
        "with_header": False, "customer_headers": {}, "extra_body": {},
        "weight": weight, "input_price": 0, "cache_read_price": 0, "output_price": 0, "currency": "USD",
    }
    if backup:
        body["backup"] = True
    return rm.api("POST", "/model-providers", body)["ID"]


def wait_rows(alias, min_count, timeout=10):
    """失败行经 channel 异步落库，success 行要等响应读完才补状态，只能轮询。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        rows = fetch_rows(alias)
        if len(rows) >= min_count:
            break
        time.sleep(0.2)
    time.sleep(0.5)
    return fetch_rows(alias)


def fetch_rows(alias):
    return rm.api("GET", f"/logs?name={alias}&page=1&page_size=20")["data"] or []


def hits(stub, provider_model):
    """桩收到的、发给指定"提供商模型"的全部请求。"""
    return [r for r in stub.received() if r["body"].get("model") == provider_model]


# ── 用例 ───────────────────────────────────────────────────────────────────


def case_b1(stub, oc, text):
    alias = "bk-01"
    mid = rm.make_model(alias, prefer_direct=True, max_retry=3)
    link_assoc(mid, oc, f"{alias}-boom", weight=1)
    link_assoc(mid, oc, f"{alias}-text", weight=1, backup=True)

    status, raw = rm.chat("openai", rm.CLIENT_PATH["openai"],
                          dict(text, model=alias), rm.client_headers("openai"))
    rm.need(status == 200, f"主挂备好应 200，实得 HTTP {status}：{raw[:300]}")
    payload = json.loads(raw)
    rm.need("choices" in payload, f"OpenAI 客户端没拿到 choices：{raw[:200]}")

    rm.need(len(hits(stub, f"{alias}-boom")) == 1, "主上游应恰好被打 1 发")
    rm.need(len(hits(stub, f"{alias}-text")) == 1, "备用上游应恰好被打 1 发")

    rows = wait_rows(alias, 2)
    succ = [r for r in rows if r["Status"] == "success"]
    errs = [r for r in rows if r["Status"] == "error"]
    rm.need(len(succ) == 1 and len(errs) == 1,
            f"应 1 成功 1 失败，实得 {len(succ)} 成功 {len(errs)} 失败")
    rm.need(succ[0].get("ProviderModel") == f"{alias}-text",
            f"成功行 ProviderModel={succ[0].get('ProviderModel')}，应来自备用关联")
    rm.need(succ[0].get("Backup") is True,
            f"成功行 Backup={succ[0].get('Backup')}，期望 true（降级必须能在日志页看出来）")
    rm.need(errs[0].get("Error", "").startswith("status: 400"),
            f"失败行应是主关联的 400 原文，实得 {errs[0].get('Error')!r}")


def case_b2(stub, oc, text):
    alias = "bk-02"
    mid = rm.make_model(alias, prefer_direct=True, max_retry=3)
    link_assoc(mid, oc, f"{alias}-text", weight=1)
    link_assoc(mid, oc, f"{alias}-boom", weight=1, backup=True)

    status, raw = rm.chat("openai", rm.CLIENT_PATH["openai"],
                          dict(text, model=alias), rm.client_headers("openai"))
    rm.need(status == 200, f"主健康应 200，实得 HTTP {status}：{raw[:300]}")

    rm.need(len(hits(stub, f"{alias}-text")) == 1, "主上游应恰好被打 1 发")
    rm.need(len(hits(stub, f"{alias}-boom")) == 0,
            f"备用上游被打了 {len(hits(stub, f'{alias}-boom'))} 发——主关联健康时备用不该被触碰")

    rows = wait_rows(alias, 1)
    succ = [r for r in rows if r["Status"] == "success"]
    rm.need(len(succ) == 1, f"应恰 1 条成功行，实得 {len(rows)} 行")
    rm.need(not succ[0].get("Backup"),
            f"成功行 Backup={succ[0].get('Backup')}，主关联服务时不应标备用")


def case_b3(stub, oc, text):
    alias = "bk-03"
    mid = rm.make_model(alias, prefer_direct=True, max_retry=3)
    # 桩按 provider_model 的后缀识别行为，两个名字都以 boom 结尾、又彼此可区分
    link_assoc(mid, oc, f"{alias}-a-boom", weight=1)
    link_assoc(mid, oc, f"{alias}-b-boom", weight=1, backup=True)

    status, raw = rm.chat("openai", rm.CLIENT_PATH["openai"],
                          dict(text, model=alias), rm.client_headers("openai"))
    rm.need(status != 200, "主备全挂时应失败")
    rm.need("backup providers exhausted" in raw,
            f"客户端错误应带降级标记：{raw[:300]}")

    rows = wait_rows(alias, 3)
    succ = [r for r in rows if r["Status"] == "success"]
    rm.need(not succ, "不该出现成功行")
    # 重试 0/1 各落一条主/备用的 400 失败行；重试 2 池空，pop 收尾错误落第三条
    pops = [r for r in rows if (r.get("Error") or "").startswith("balancer pop err")]
    rm.need(len(pops) == 1, f"应有 1 条池空收尾行，实得 {len(pops)}")
    rm.need("backup providers exhausted" in pops[0].get("Error", ""),
            f"收尾行应带降级标记：{pops[0].get('Error')!r}")


# ── 主流程 ─────────────────────────────────────────────────────────────────


def main():
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    shutil.rmtree(WORKDIR, ignore_errors=True)
    os.makedirs(WORKDIR, exist_ok=True)

    print("→ 构建二进制")
    rm.BIN = BIN
    subprocess.run(["go", "build", "-o", BIN, "."], cwd=rm.REPO, check=True)

    stub = rm.Stub(WORKDIR)
    stub.start()
    print(f"→ 桩上游 127.0.0.1:{rm.STUB_PORT}")
    llmio = rm.Llmio(WORKDIR)
    results = []
    try:
        llmio.start()
        print(f"→ llmio {rm.BASE}（工作目录 {WORKDIR}）")
        oc = rm.make_provider("backup-openai", "openai",
                              {"base_url": f"http://127.0.0.1:{rm.STUB_PORT}/openai", "api_key": "stub-key"})
        text = {"messages": [{"role": "user", "content": "你好"}], "max_tokens": 64}

        cases = [
            ("B1 主挂备好·备用接管并在日志标出", lambda: case_b1(stub, oc, text)),
            ("B2 主好备零请求·备用不被触碰", lambda: case_b2(stub, oc, text)),
            ("B3 主备全挂·错误带降级标记", lambda: case_b3(stub, oc, text)),
        ]
        for label, fn in cases:
            try:
                fn()
                rm.record(results, label, "ok")
            except rm.Failure as e:
                rm.record(results, label, "fail", str(e))
            except Exception as e:  # noqa: BLE001
                rm.record(results, label, "fail", f"{type(e).__name__}: {e}")
    finally:
        llmio.stop()
        stub.stop()

    ok = sum(1 for _, v, _ in results if v == "ok")
    return 0 if ok == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())

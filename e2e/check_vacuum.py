#!/usr/bin/env python3
"""整库重整（VACUUM）的端到端验收车：量的是**那几十秒里写请求会怎样**。

`check_compression.py` 验的是"库里到底存了什么"，这辆车验的是**代价**。重整这条路
不是"慢一点"，它是**真正的服务不可用**：一条 `VACUUM` 语句全程持 EXCLUSIVE，
读与写都进不来。界面在按下之前就写着这句话（`compression.vacuum.warn`），
而这句话必须是真的——所以这辆车就干一件事：**一边重整，一边按生产节奏打聊天请求，
数一数哪些 500 了、各等了多久。**

其余几处交叉验证：

1. **文件真的缩了**：重整前后各量一次文件大小与 `freelist_count`。
   接口自报的 `freed_bytes` 与文件实际缩掉的量必须对得上——差得远说明有一边在说谎。
2. **回执如实**：`kind=vacuum` / `source=manual` / `stop_reason=vacuumed`。
   不是的话，翻记录的人会以为跑的是增量回收。
3. **跑完能恢复**：重整之后再打一串请求，必须全绿。收工即恢复是这条路可接受的前提。

跑在一份**副本**上，而且这份副本得先有货可收：源库的 freelist 是 0，直接重整
只会得到一次"整库重写、文件几乎没变"。所以先按真实成因（日志清理）删掉大部分
`chat_ios` 行，把 freelist 造出来——真机上那个 6 GiB 的洞就是这么来的。

用法：

    python e2e/check_vacuum.py                                   # 默认源库 llmio-repaired.db
    python e2e/check_vacuum.py --db D:\\llmio-test\\llmio-repaired.db
    python e2e/check_vacuum.py --leave                            # 备好实例交人手点

源库**只读打开**，任何写入都落在 `--workdir` 下的副本上。
"""

import argparse
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(E2E_DIR)

PORT = 7203
STUB_PORT = 7198
TOKEN = "e2e-vacuum"
BASE = f"http://127.0.0.1:{PORT}"

# 生产上那个洞是日志清理删出来的。这里保留最新的 2000 行（约 16%），
# 剩下的删掉——12,483 行 / 7.05 GiB 的库上，这个比例能造出 5 GiB 量级的 freelist。
KEEP_ROWS = 2000

# 写请求的节奏：每 500 毫秒一发、3 个并发。真机上的聊天请求比这更密，
# 但这里要的是"能不能穿过那几十秒"，密度再高也只会得到同一组数字。
WRITE_WORKERS = 3
WRITE_INTERVAL = 0.5


class Failure(Exception):
    pass


def need(cond, msg):
    if not cond:
        raise Failure(msg)


def human(n):
    step = 1024.0
    v = float(n)
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if v < step or unit == "TiB":
            return f"{v:.2f} {unit}" if unit != "B" else f"{int(v)} B"
        v /= step


# ── HTTP ───────────────────────────────────────────────────────────────────


def http(method, url, body=None, headers=None, timeout=120):
    """返回 (状态码, 文本, 耗时)。4xx/5xx 也是结果，不当异常抛。"""
    data = None
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body, ensure_ascii=False).encode()
    req = urllib.request.Request(url, data=data, method=method)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    started = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return res.status, res.read().decode("utf-8", "replace"), time.time() - started
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace"), time.time() - started


def api(method, path, body=None, timeout=120):
    """管理接口。路径相对 /api 写——漏了不会 404，会拿到 SPA 的 index.html。"""
    status, text, _ = http(method, BASE + "/api" + path, body,
                           {"Authorization": f"Bearer {TOKEN}"}, timeout=timeout)
    payload = json.loads(text) if text else {}
    if payload.get("code") != 200:
        raise RuntimeError(f"{method} {path} -> {status} {text[:400]}")
    return payload.get("data")


def chat():
    """打一发普通聊天请求。这是**唯一会写库**的客户端路径。

    落库那一步（`service.SaveChatLog`）是**同步的**，失败就直接 500——所以
    重整期间量到的 500 不是"流断了"，而是写请求在锁上等满 busy_timeout。
    上游那一步走桩，与库无关，正好把变量收到一处：只有写在变。
    """
    return http("POST", BASE + "/openai/v1/chat/completions",
                {"model": "vacuum-01", "messages": [{"role": "user", "content": "只回两个字：收到"}],
                 "max_tokens": 16},
                {"Authorization": f"Bearer {TOKEN}", "Content-Type": "application/json"})


# ── 进程 ───────────────────────────────────────────────────────────────────


class Proc:
    def __init__(self, argv, workdir, env=None, log=None):
        self.argv, self.workdir, self.env = argv, workdir, env
        self.log = log
        self.proc = None

    def start(self):
        out = open(self.log, "ab") if self.log else subprocess.DEVNULL
        self.proc = subprocess.Popen(self.argv, cwd=self.workdir, env=self.env,
                                     stdout=out, stderr=subprocess.STDOUT)

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.proc.kill()


# ── 造 fixture：把 freelist 删出来 ─────────────────────────────────────────


def file_page_stats(path):
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        fl = con.execute("PRAGMA freelist_count").fetchone()[0]
        pc = con.execute("PRAGMA page_count").fetchone()[0]
        ps = con.execute("PRAGMA page_size").fetchone()[0]
    finally:
        con.close()
    return {"freelist_pages": fl, "page_count": pc, "page_size": ps,
            "freelist_bytes": fl * ps, "file_bytes": os.path.getsize(path)}


def make_freelist(path, keep):
    """删掉较早的 chat_ios 行，把页还进 freelist。

    **`journal_mode=OFF` 只为快**：这是一份一次性的副本，且这一步要重写
    5 GiB 量级的页——开着回滚日志等于把这份量再写一遍。风险（半路挂掉会留下
    坏库）由紧接着的 `quick_check` 兜住：不 ok 就直接退出，绝不拿一份坏库
    去量 VACUUM 的耗时。
    """
    con = sqlite3.connect(path)
    try:
        con.execute("PRAGMA journal_mode=OFF")
        before = con.execute("SELECT count(*) FROM chat_ios").fetchone()[0]
        started = time.time()
        con.execute(
            "DELETE FROM chat_ios WHERE id NOT IN "
            "(SELECT id FROM chat_ios ORDER BY id DESC LIMIT ?)", (keep,))
        con.commit()
        took = time.time() - started
        after = con.execute("SELECT count(*) FROM chat_ios").fetchone()[0]
        check = con.execute("PRAGMA quick_check").fetchone()[0]
    finally:
        con.close()
    need(check == "ok", f"造完 freelist 后副本自检不通过：{check}")
    return {"rows_before": before, "rows_after": after, "took": took}


# ── 写请求探测 ─────────────────────────────────────────────────────────────


class WriteProbe:
    """按生产节奏打聊天请求，记下每一发的状态码与耗时。

    重整期间每一发都会等满 `busy_timeout`（5 秒）再失败——所以这个探测器的
    采样密度天然被限制在 5 秒一发上下，并发三个只是为了让"哪一发撞上了"
    不至于变成一个抛硬币的问题。
    """

    def __init__(self):
        self.lock = threading.Lock()
        self.samples = []
        self.stop_flag = threading.Event()
        self.threads = []

    def _loop(self):
        while not self.stop_flag.is_set():
            started = time.time()
            status, text, took = chat()
            with self.lock:
                self.samples.append({"t": started, "status": status,
                                     "seconds": round(took, 3), "body": text[:200]})
            self.stop_flag.wait(WRITE_INTERVAL)

    def start(self):
        self.t0 = time.time()
        for _ in range(WRITE_WORKERS):
            th = threading.Thread(target=self._loop, daemon=True)
            th.start()
            self.threads.append(th)

    def stop(self):
        self.stop_flag.set()
        for th in self.threads:
            th.join(timeout=30)
        return self.samples

    def summary(self):
        with self.lock:
            got = list(self.samples)
        ok = [s for s in got if s["status"] == 200]
        bad = [s for s in got if s["status"] != 200]
        worst = max((s["seconds"] for s in ok), default=0.0)
        # 失败那一发的耗时也要报：它就是写请求的耐心（busy_timeout）在真机上的实测值。
        # 界面上有一句话写着"最长等待 N 秒后放弃"，N 只能靠这里量出来——猜的话
        # 就会写成一个没人验证过的数（原先那句写的 60 秒就是这么来的）。
        worst_bad = max((s["seconds"] for s in bad), default=0.0)
        # "被挡住的那一段有多长"：从第一发明显变慢的请求，到最后一发。
        # 它比"重整耗时"更接近用户体感——VACUUM 前面那段复制是可以正常读写的。
        slow = [s for s in got if s["seconds"] >= 0.5]
        span = round(slow[-1]["t"] - slow[0]["t"], 1) if slow else 0.0
        return {"total": len(got), "ok": len(ok), "failed": len(bad),
                "worst_ok_seconds": round(worst, 3),
                "worst_failed_seconds": round(worst_bad, 3),
                "slow_count": len(slow), "slow_span_seconds": span,
                "codes": sorted({s["status"] for s in bad}),
                "example": (bad[0]["body"] if bad else "")}


def wait_writes(probe, count, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        with probe.lock:
            if len(probe.samples) >= count:
                return
        time.sleep(0.1)


# ── 主流程 ─────────────────────────────────────────────────────────────────


def main():
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=r"D:\llmio-test\llmio-repaired.db",
                    help="源库副本的路径（只读打开）")
    ap.add_argument("--workdir", default=r"D:\llmio-test\work-vacuum")
    ap.add_argument("--keep", action="store_true", help="跑完留下工作副本（默认删掉）")
    ap.add_argument("--leave", action="store_true",
                    help="备好一份带大洞的库与活实例就收手，不自己跑重整（交人手点）")
    args = ap.parse_args()

    src = os.path.abspath(args.db)
    need(not src.lower().startswith("e:\\projects\\"),
         f"拒绝在源目录上跑：{src}——那里是原始数据，只能指向副本")
    need(os.path.exists(src), f"源库不存在：{src}")

    work = os.path.abspath(args.workdir)
    dbdir = os.path.join(work, "db")
    dst = os.path.join(dbdir, "llmio.db")
    binpath = os.path.join(work, "llmio-vacuum.exe")
    os.makedirs(dbdir, exist_ok=True)

    # 每次都重建：上一次跑剩下来的库已经收干净了，拿它接着跑只会得到一次
    # "文件几乎没变"的假绿灯。
    if os.path.exists(dst):
        os.remove(dst)
    print(f"→ 复制源库 {src}（{human(os.path.getsize(src))}）")
    started = time.time()
    shutil.copyfile(src, dst)
    print(f"  副本 {dst}｜用了 {time.time() - started:.1f} 秒｜{human(os.path.getsize(dst))}")

    print(f"→ 造 freelist（保留最新 {KEEP_ROWS} 行 chat_ios）")
    made = make_freelist(dst, KEEP_ROWS)
    pre = file_page_stats(dst)
    print(f"  行 {made['rows_before']} → {made['rows_after']}（{made['took']:.1f} 秒）｜"
          f"freelist {pre['freelist_pages']:,} 页 = {human(pre['freelist_bytes'])}｜"
          f"文件 {human(pre['file_bytes'])}")
    need(pre["freelist_bytes"] > pre["file_bytes"] // 2,
         f"造出来的 freelist 只有 {human(pre['freelist_bytes'])}，占文件 "
         f"{100.0 * pre['freelist_bytes'] / pre['file_bytes']:.0f}%——"
         "这个洞不够大，量不出「要有几十秒写不进去」这件事")

    print("→ 构建二进制")
    subprocess.run(["go", "build", "-o", binpath, "."], cwd=REPO, check=True)

    env = dict(os.environ, TOKEN=TOKEN, LLMIO_SERVER_PORT=str(PORT), GIN_MODE="release",
               TZ="Asia/Shanghai")
    stub = Proc([sys.executable, os.path.join(E2E_DIR, "stub_upstream.py")], work,
                env=dict(env, STUB_PORT=str(STUB_PORT), STUB_LOG=os.path.join(work, "stub.jsonl")))
    # **工作目录必须是有 `db/` 的那一层，不能是 `db/` 本身**：库路径是写死的
    # `./db/llmio.db`，相对**进程工作目录**解析。指错了不会报错——它会老老实实
    # 建一个空库，于是重整"0.0 秒收工、文件 152 KiB"，跑出一整车假绿灯。
    app = Proc([binpath], work, env=env, log=os.path.join(work, "llmio.log"))
    stub.start()
    time.sleep(1.0)
    app.start()
    try:
        for _ in range(200):
            try:
                api("GET", "/version")
                break
            except Exception:
                time.sleep(0.15)
        else:
            raise Failure("llmio 没起来，看看 " + os.path.join(work, "llmio.log"))
        print(f"→ llmio {BASE}（工作目录 {work}，库 {dst}）")

        # 第 0 步：先确认它真的在用我们复制的那份库。
        # 这一条是**踩过坑才加的**：库路径相对进程工作目录，指错一层它就会另起一个
        # 空库，而空库上 VACUUM "0.0 秒收工"、写请求一发不失败——每一条判据都绿，
        # 结论全错。所以宁可在这里硬碰一次。
        looking_at = api("GET", "/logs/compression")["db"]["file_size"]
        copied = os.path.getsize(dst)
        need(abs(looking_at - copied) < 8 << 20,
             f"服务报的库是 {human(looking_at)}，复制出来的那份是 {human(copied)}——"
             "它用的不是这份副本，接着跑只会得到一车空库上的假绿灯")

        # 桩上游 + 一个模型：聊天请求要真的走完全程，才会写 ChatLog + ChatIO。
        pid = api("POST", "/providers", {
            "name": "stub-openai", "type": "openai",
            "config": json.dumps({"base_url": f"http://127.0.0.1:{STUB_PORT}/openai",
                                  "api_key": "stub-key"}), "console": "", "proxy": "",
            "error_matcher": ""})["ID"]
        mid = api("POST", "/models", {
            "name": "vacuum-01", "remark": "e2e-vacuum", "max_retry": 1, "time_out": 60,
            "strategy": "lottery", "breaker": False, "prefer_direct": True})["ID"]
        api("POST", "/model-providers", {
            "model_id": mid, "provider_id": pid, "provider_name": "stub-text",
            "tool_call": True, "structured_output": True, "image": True,
            "with_header": False, "customer_headers": {}, "extra_body": {},
            "weight": 1, "input_price": 0, "cache_read_price": 0, "output_price": 0,
            "currency": "USD"})

        if args.leave:
            # `--leave` 是给「交人手点」用的：备好一份带大洞的库和一个活实例就收手，
            # **不自己跑那一趟重整**。跑过就没得点了——洞是这份 fixture 唯一的戏。
            print("\n" + "=" * 72)
            print("实例已备好，交人手点")
            print("=" * 72)
            print(f"地址          {BASE}")
            print(f"库            {human(os.path.getsize(dst))}（{made['rows_after']:,} 行 chat_ios）"
                  f"｜freelist {pre['freelist_pages']:,} 页 = {human(pre['freelist_bytes'])}")
            print(f"空闲磁盘      {human(api('GET', '/logs/compression')['db']['disk_free_bytes'])}")
            print(f"工作目录      {work}｜日志 {work}\\llmio.log")
            print("看点：控制台 → 系统设置 → 数据库压缩 → 「立即重整」")
            print("=" * 72)
            print("按 Ctrl+C 收工（本脚本不会删这份副本，也不会替你跑重整）")
            while True:
                time.sleep(3600)

        # ── 一、空载基线 ──
        probe = WriteProbe()
        probe.start()
        wait_writes(probe, 6, timeout=30)
        probe.stop()
        base = probe.summary()
        need(base["failed"] == 0,
             f"空载时就有 {base['failed']} 发聊天请求失败（{base['example']}）——"
             "基线不成立，后面量到的失败就说不清是谁造成的")
        print(f"【基线】空载 {base['total']} 发：全 200，最长 {base['worst_ok_seconds']} 秒")

        before = api("GET", "/logs/compression")
        print(f"【重整前】文件 {human(before['db']['file_size'])}｜"
              f"freelist {before['db']['freelist_count']} 页 = "
              f"{human(before['db']['freelist_count'] * before['db']['page_size'])}｜"
              f"空闲磁盘 {human(before['db']['disk_free_bytes'])}")
        # ── 二、一边重整一边打 ──
        status, text, _ = http("POST", BASE + "/api/logs/compression/vacuum", b"",
                               {"Authorization": f"Bearer {TOKEN}"})
        payload = json.loads(text)
        need(status == 200 and payload.get("code") == 200,
             f"重整没能开始：{status} {text[:400]}")
        plan = payload["data"]["plan"]
        print(f"【重整】已受理｜预估需要 {human(plan['need_bytes'])}，当前可用 "
              f"{human(plan['free_bytes'])}")

        probe = WriteProbe()
        probe.start()
        vac_t0 = time.time()
        seen_kind = set()
        while time.time() - vac_t0 < 600:
            st = api("GET", "/logs/compression")
            seen_kind.add(st["reclaiming_kind"])
            if not st["reclaiming"]:
                break
            time.sleep(1.0)
        else:
            raise Failure("重整跑过 10 分钟还没收工")
        vac_seconds = time.time() - vac_t0
        # 收工之后再打 3 秒：要看到"恢复"，而不是"最后一发恰好撞上收工那一刻"。
        time.sleep(3.0)
        samples = probe.stop()
        dur = probe.summary()
        print(f"【重整】收工｜耗时 {vac_seconds:.1f} 秒｜期间 {dur['total']} 发聊天请求："
              f"成功 {dur['ok']} / 失败 {dur['failed']}{dur['codes'] or ''}")

        after = api("GET", "/logs/compression")
        tally = file_page_stats(dst)
        rec = after["reclaim"]

        # ── 三、跑完能不能恢复 ──
        probe = WriteProbe()
        probe.start()
        wait_writes(probe, 6, timeout=30)
        probe.stop()
        tail = probe.summary()
        # ── 判据 ──
        #
        # 每一条都对应一句**界面上已经写出去的话**。红了不是"VACUUM 要改"，
        # 而是那句话该改。
        need("vacuum" in seen_kind,
             f"状态接口从头到尾没报过 reclaiming_kind=vacuum（只见过 {seen_kind}）——"
             "界面据此说'重整中'并且不画「停止」，报错了那一趟会被当成回收")
        need(after["reclaiming"] is False, "重整收工了但状态还报 running")
        need(rec["kind"] == "vacuum",
             f"回执记的 kind={rec['kind']!r}，期望 vacuum——翻记录的人会以为跑的是增量回收")
        need(rec["stop_reason"] == "vacuumed", f"收工理由记的是 {rec['stop_reason']!r}")
        need(rec["status"] == "done", f"回执状态是 {rec['status']!r}")
        need(rec["source"] == "manual", f"来源记的是 {rec['source']!r}")

        need(after["db"]["freelist_count"] == 0,
             f"重整完还留着 {after['db']['freelist_count']} 页空闲——VACUUM 应当把它清空")
        shrink = before["db"]["file_size"] - tally["file_bytes"]
        need(shrink > before["db"]["file_size"] // 2,
             f"文件只缩了 {human(shrink)}（{human(before['db']['file_size'])} → "
             f"{human(tally['file_bytes'])}）——洞占了大半，重整后文件也该缩掉大半")
        # 接口自报的与文件实际缩掉的量必须对得上：差得远说明有一边在说谎。
        drift = abs(rec["freed_bytes"] - shrink)
        need(drift < 64 << 20,
             f"回执说放掉 {human(rec['freed_bytes'])}，文件实际缩了 {human(shrink)}，"
             f"差了 {human(drift)}——两者说的应当是同一件事")

        # 窗口里的写请求：**判据不预设"一定失败"**。
        #
        # 这条是给界面那句「写入请求会直接失败」当实测依据的：真机上到底是"等满
        # 5 秒然后 500"，还是"排队慢一点但都成了"，只能量。所以判据取一个析取——
        # 要么有失败，要么最长等待明显盖过空载；两个都不成立才说明窗口是假的。
        # 一旦真机上量到"全都成了"，那是好消息，但**界面那句话就成了假话**，
        # 这条红要的正是有人去看一眼那句话。
        worst = dur["worst_ok_seconds"]
        need(dur["failed"] > 0 or worst >= max(0.5, 10 * base["worst_ok_seconds"]),
             f"重整期间写请求既一发没失败、最长也只等了 {worst} 秒"
             f"（空载 {base['worst_ok_seconds']} 秒）——"
             "「这段时间写请求进不来」在真机上不成立，界面那句警告要改口径")
        need(tail["failed"] == 0,
             f"重整收工后仍有 {tail['failed']} 发聊天请求失败（{tail['example']}）——"
             "收工即恢复是这条路可接受的前提")

        # ── 回执 ──
        print("\n" + "=" * 72)
        print("整库重整 · 真机回执")
        print("=" * 72)
        print(f"库            {human(before['db']['file_size'])} / "
              f"{before['db']['rows']:,} 行 chat_ios")
        print(f"freelist      {before['db']['freelist_count']:,} 页 = "
              f"{human(before['db']['freelist_count'] * before['db']['page_size'])}")
        print(f"文件          {human(before['db']['file_size'])} → {human(tally['file_bytes'])}"
              f"（缩掉 {human(shrink)}）")
        print(f"耗时          {vac_seconds:.1f} 秒（{human(tally['file_bytes'])} / "
              f"{vac_seconds:.1f} 秒 = {human(tally['file_bytes'] / vac_seconds)}/秒）")
        print(f"回执          kind={rec['kind']} source={rec['source']} "
              f"status={rec['status']} reason={rec['stop_reason']} "
              f"freed={human(rec['freed_bytes'])} duration_ms={rec['duration_ms']}")
        print(f"期间写请求     共 {dur['total']} 发：成功 {dur['ok']} / **失败 {dur['failed']}**"
              f"（状态码 {dur['codes']}）")
        print(f"                其中 {dur['slow_count']} 发明显变慢（≥0.5 秒），"
              f"这一段持续 {dur['slow_span_seconds']} 秒")
        print(f"                失败样例：{(dur['example'] or '').strip()[:160]}")
        if dur["failed"]:
            print(f"                失败那一发的耗时最长 {dur['worst_failed_seconds']} 秒"
                  f"——这就是写请求的耐心（busy_timeout）在真机上的值，"
                  "界面上「最长等待 N 秒后放弃」的 N 由它回答")
        print(f"空载最长等待   {base['worst_ok_seconds']} 秒｜收工后 {tail['worst_ok_seconds']} 秒"
              f"（{tail['total']} 发全 200）")
        print("=" * 72)

    finally:
        if args.leave:
            # 留给人手点的实例不关：副本也不删（那正是要点的东西）。
            print(f"\n实例仍在跑：{BASE}｜工作目录 {work}")
        else:
            app.stop()
            stub.stop()
            if not args.keep:
                for suffix in ("", "-journal", "-wal", "-shm"):
                    try:
                        os.remove(dst + suffix)
                    except OSError:
                        pass
                print(f"（工作副本已删，留着的二进制在 {binpath}）")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Failure as e:
        print(f"\n✗ 没通过：{e}")
        sys.exit(1)

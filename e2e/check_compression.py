#!/usr/bin/env python3
"""压缩与空间回收的端到端验收车：**直接量库文件**，不信任接口自报的数。

`run_matrix.py` 验的是"翻译对不对"，这辆车验的是另一件事——**库里到底存了什么**。
两者不能互相替代：接口说"已压缩"而库里其实还是明文，只有把库文件打开来看才知道；
反过来，库里的帧解不回原文，接口自己也不会报错。

三处交叉：

1. **文件头**（偏移 52/64，绕过一切 SQL 层直接读字节）——auto_vacuum 是不是真落下了。
   `PRAGMA auto_vacuum` 问的是**当前连接**看到的库，而开过这个库的进程会缓存它；
   文件头是任何连接、任何进程都得认的那一份。
2. **行形态**（`typeof(input)` + `hex(substr(input,1,4))`）——明文是 TEXT、帧是 BLOB
   且以 `\\x00LCZ` 开头。接口报的 `framed_rows` 走的是另一条路（带缓存），
   它和文件不一致时，以文件为准。
3. **HTTP 读回来**（`GET /api/logs/:id/chat-io`）——走的是生产读路径（AfterFind 解帧），
   拿到的必须与种下去的**逐字节相同**。

两条路各起一次 llmio：

    新库  空目录 → 白拿 auto_vacuum=2 → 种明文 → 迁移 → 回收，文件真的变小
    老库  非空且 auto_vacuum=0 → 默认启动**一个字节都不动** → 要回收就明说
          "没开 auto_vacuum" → 显式 on 才转换

用法：
    python e2e/check_compression.py
"""

import hashlib
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(E2E_DIR)
RUN_DIR = os.path.join(E2E_DIR, ".run-compress")
BIN = os.path.join(RUN_DIR, "llmio-compress.exe")

TOKEN = "e2e-compress"
NEW_PORT, OLD_PORT = 7201, 7202

# 帧魔数：00 'L' 'C' 'Z'。明文 JSON 的首字节不可能是 0x00（RFC 8259），
# 所以这一个判断就够区分两种形态，不需要额外的标志列。
FRAME_MAGIC = "004C435A"

SEED_ROWS = 40


# ── 断言 ───────────────────────────────────────────────────────────────────


class Failure(Exception):
    pass


def need(cond, msg):
    if not cond:
        raise Failure(msg)


MARK = {"ok": "✓", "fail": "✗"}
RESULTS = []


def record(label, verdict, detail=""):
    RESULTS.append((label, verdict, detail))
    print(f"  {MARK[verdict]} {label}" + ("" if verdict == "ok" else f"\n      {detail}"))


def case(label, fn):
    """跑一条断言。异常一律算失败但**不中断**——一次跑完看到全部问题，
    比修一个跑一次快得多。"""
    try:
        detail = fn() or ""
        record(label, "ok", detail)
    except Failure as e:
        record(label, "fail", str(e))
    except Exception as e:  # noqa: BLE001 —— 驱动层的错也要算失败，不能把车带崩
        record(label, "fail", f"{type(e).__name__}: {e}")


# ── 直接量文件 ─────────────────────────────────────────────────────────────


def header_flag(path):
    """读文件头：返回 (offset52 最大根页, offset64 增量标志)。

    增量模式的标志位在这里，而不是 offset52——offset52 是"最大根 b-tree 页号"，
    空库上它是 0、有表之后是 36 之类的数。只看 52 会把"有表"误判成"没开"。
    """
    with open(path, "rb") as f:
        head = f.read(100)
    return (int.from_bytes(head[52:56], "big"), int.from_bytes(head[64:68], "big"))


def ro(path):
    """只读连接。**必须只读**：验收工具自己去改被测对象，测出来的东西就不算数了。"""
    return sqlite3.connect(f"file:{path.replace(os.sep, '/')}?mode=ro", uri=True)


def rw(path):
    return sqlite3.connect(path)


def q1(conn, sql, args=()):
    return conn.execute(sql, args).fetchone()


def pragma_vacuum(path):
    with ro(path) as c:
        return q1(c, "PRAGMA auto_vacuum")[0]


def file_sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 22), b""):
            h.update(chunk)
    return h.hexdigest()


# ── HTTP ───────────────────────────────────────────────────────────────────


def http(port, method, path, body=None, timeout=120):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(f"http://127.0.0.1:{port}{path}", data=data, method=method)
    req.add_header("Authorization", "Bearer " + TOKEN)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return json.loads(res.read())
    except urllib.error.HTTPError as e:
        return json.loads(e.read())


def status(port):
    return http(port, "GET", "/api/logs/compression")["data"]


def wait_until(cond, what, timeout=180):
    """等到 cond() 为真。**不要用 `running` 标志等迁移**：迁移的占位是在
    goroutine 里抢的，一个 40 行的小库可能在第一次轮询之前就跑完了——
    那时 `running` 读到的是 False，车会当成"已经跑完"然后去读一份还没迁完的库。
    等**终态**（state.status 落到 done/failed）就没有这个缝。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if cond():
            return
        time.sleep(0.3)
    raise Failure(f"{what} 超过 {timeout}s 还没结束")


def wait_idle(port, key, timeout=180):
    """回收那条路可以用 in-flight 标志等：它是在写响应之前**同步**占位的。"""
    wait_until(lambda: not status(port)[key], key, timeout)


# ── llmio 进程 ─────────────────────────────────────────────────────────────


class Llmio:
    def __init__(self, workdir, port, env_extra=None):
        self.workdir = workdir
        self.port = port
        self.env_extra = env_extra or {}
        self.proc = None

    @property
    def db(self):
        return os.path.join(self.workdir, "db", "llmio.db")

    def start(self):
        env = dict(os.environ, TOKEN=TOKEN, LLMIO_SERVER_PORT=str(self.port),
                   GIN_MODE="release", TZ="Asia/Shanghai", **self.env_extra)
        self.proc = subprocess.Popen(
            [BIN], cwd=self.workdir, env=env,
            stdout=open(os.path.join(self.workdir, "llmio.log"), "ab"),
            stderr=subprocess.STDOUT,
        )
        # 探的是 `/api/version`，不是 `/version`。后者会落到 SPA 回退上，**回一份
        # index.html 和 200**——于是"等它起来"这件事会以"永远起不来"的样子失败
        # （200 到手、json 解不开、一直重试到超时），而日志里看起来一切正常。
        for _ in range(200):
            try:
                http(self.port, "GET", "/api/version", timeout=5)
                return
            except Exception:  # noqa: BLE001 —— 还没起来，接着等
                time.sleep(0.15)
        # 起不来的话**先把进程收掉**再报错：调用方是在 try 之外调 start() 的，
        # 漏一个进程在这儿会一直占着端口，下一次跑会以另一副面孔失败。
        self.stop()
        raise Failure(f"llmio 没起来，看 {self.workdir}/llmio.log")

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.proc.kill()


# ── 种子 ───────────────────────────────────────────────────────────────────


def seed_bodies():
    """造 SEED_ROWS 份正文：**共享一大段公共前缀**，尾部各不同。

    这样每行的块大部分是同一批内容——方案要省的就是这部分。全用随机字节的话，
    块表去重率是 0，于是"压缩有没有生效"这件事在测试里比重放还难看出来。
    单行也要够大：小于 4 KiB 的行走的是逐行帧那条路，根本不进块表。
    """
    out = []
    for i in range(SEED_ROWS):
        msgs = [{"role": "user", "content": "A" * 4000 + f"-common-{j}"} for j in range(3)]
        msgs.append({"role": "assistant", "content": f"row-{i:04d}-" + "B" * 2000})
        out.append(json.dumps({"model": "e2e", "messages": msgs}, ensure_ascii=False))
    return out


def seed_plaintext(db, bodies):
    """绕过 llmio 直接种**明文行**（模拟历史数据）。

    `updated_at` 必须推到过去：迁移的候选过滤带一个冷静期（默认 60 秒），
    挡的是"正在被写、响应体还没补上"的那一行。种成"刚刚"的话这一批一行都选不上，
    迁移会**正常地跑完、一行都不迁**——检查会以"库里还是明文"失败，
    而那个失败与压缩无关。
    """
    with rw(db) as c:
        for i, body in enumerate(bodies, 1):
            c.execute(
                "INSERT INTO chat_ios (created_at, updated_at, log_id, input)"
                " VALUES (datetime('now','-2 hours'), datetime('now','-2 hours'), ?, ?)",
                (i, body),
            )
        c.commit()


# ── 新库那条路 ─────────────────────────────────────────────────────────────


def check_new(work):
    print("\n【新库】空目录起 llmio")
    llmio = Llmio(work, NEW_PORT)
    llmio.start()
    try:
        db = llmio.db
        case("空库白拿 auto_vacuum=INCREMENTAL（独立连接 + 文件头两处都对）", lambda: (
            need(pragma_vacuum(db) == 2, f"PRAGMA auto_vacuum={pragma_vacuum(db)}，期望 2"),
            need(header_flag(db)[1] == 1, f"文件头 offset64={header_flag(db)[1]}，期望 1（增量标志）"),
            "pragma=2，文件头增量标志=1",
        ))

        bodies = seed_bodies()
        seed_plaintext(db, bodies)
        sha_before = [hashlib.sha256(b.encode()).hexdigest() for b in bodies]
        size_before = os.path.getsize(db)

        case("种下的 40 行是明文 TEXT（迁移前）", lambda: (
            need(q1(ro(db), "SELECT count(*) FROM chat_ios WHERE typeof(input)='text'")[0] == SEED_ROWS,
                 "种完不是 40 行明文"),
            f"{SEED_ROWS} 行 TEXT，库 {size_before / 1024:.0f} KiB",
        ))

        http(NEW_PORT, "POST", "/api/logs/compression/run", {"acknowledge_no_backup": True})
        wait_until(lambda: status(NEW_PORT)["state"]["status"] in ("done", "failed", "paused"), "迁移")
        st = status(NEW_PORT)
        case("迁移自报成功且无错误", lambda: (
            need(st["state"]["status"] == "done", f"status={st['state']['status']}"),
            need(st["state"]["last_error"] == "", f"last_error={st['state']['last_error']}"),
            need(st["state"]["packed"] == SEED_ROWS, f"packed={st['state']['packed']}，期望 {SEED_ROWS}"),
            f"扫 {st['state']['scanned']} 行、迁 {st['state']['packed']} 行",
        ))

        case("迁移后库里是帧：BLOB 且以 \\x00LCZ 开头", lambda: (
            need(q1(ro(db), "SELECT count(*) FROM chat_ios WHERE typeof(input)='blob'")[0] == SEED_ROWS,
                 f"BLOB 行数={q1(ro(db), 'SELECT count(*) FROM chat_ios')[0]}"),
            need(q1(ro(db), f"SELECT count(*) FROM chat_ios WHERE hex(substr(input,1,4))='{FRAME_MAGIC}'")[0] == SEED_ROWS,
                 "有一行的帧头不是 00LCZ"),
            need(st["db"]["auto_vacuum"] == 2, "迁移把 auto_vacuum 改了"),
            "40 行全是 00LCZ 帧",
        ))

        def read_back():
            bad = []
            for i, want in enumerate(bodies, 1):
                got = http(NEW_PORT, "GET", f"/api/logs/{i}/chat-io")["data"]["Input"]
                if hashlib.sha256(got.encode()).hexdigest() != sha_before[i - 1]:
                    bad.append(i)
            need(not bad, f"第 {bad[:5]} 行从接口读回来与种下去的不是同一份字节")
            return "40 行经 HTTP 读回，逐字节与原文相同"

        case("读回来逐字节相同（走生产读路径 AfterFind）", read_back)

        # 回收：删行造 freelist，再看文件是不是**真的**变小。
        free_before = None

        def make_holes():
            nonlocal free_before
            with rw(db) as c:
                c.execute("DELETE FROM chat_ios")
                c.commit()
                free_before = q1(c, "PRAGMA freelist_count")[0]
            need(free_before > 0, "删光了行，freelist 却是空的")
            return f"freelist={free_before} 页，文件仍是 {os.path.getsize(db) / 1024:.0f} KiB（一字节没缩）"

        case("删行只把页还进 freelist，文件不缩", make_holes)

        size_before_reclaim = os.path.getsize(db)
        http(NEW_PORT, "POST", "/api/logs/compression/reclaim")
        wait_idle(NEW_PORT, "reclaiming")
        r = status(NEW_PORT)["reclaim"]

        def reclaim_verdict():
            size_after = os.path.getsize(db)
            page_count = q1(ro(db), "PRAGMA page_count")[0]
            page_size = q1(ro(db), "PRAGMA page_size")[0]
            need(r["stop_reason"] == "empty", f"停下来的原因是 {r['stop_reason']}，期望 empty")
            need(r["freed_pages"] == free_before,
                 f"接口报放掉 {r['freed_pages']} 页，独立量到的是 {free_before} 页")
            need(size_after < size_before_reclaim, f"文件没缩：{size_before_reclaim} → {size_after}")
            need(size_after == page_count * page_size,
                 f"文件 {size_after} 不等于 page_count×page_size={page_count * page_size}——没真截断")
            need(q1(ro(db), "PRAGMA freelist_count")[0] == 0, "回收完 freelist 还非空")
            return (f"{size_before_reclaim / 1024:.0f} KiB → {size_after / 1024:.0f} KiB，"
                    f"放掉 {r['freed_pages']} 页 / {r['duration_ms']}ms")
    finally:
        llmio.stop()


# ── 老库那条路 ─────────────────────────────────────────────────────────────


def make_old_db(db):
    """造一份**老库**：非空、auto_vacuum=0。

    关键在于**不经过 llmio**——走它的话空库会被设上 pragma，那就不是老库了。
    这里用驱动自己建表塞行，文件里从头到尾没出现过 auto_vacuum。
    """
    os.makedirs(os.path.dirname(db), exist_ok=True)
    with rw(db) as c:
        c.execute("CREATE TABLE legacy (id INTEGER PRIMARY KEY, pad BLOB)")
        for i in range(200):
            c.execute("INSERT INTO legacy (pad) VALUES (?)", (bytes([i % 251]) * 8192,))
        c.commit()


def check_old(work):
    print("\n【老库】非空 + auto_vacuum=0 起 llmio（默认开关）")
    db = os.path.join(work, "db", "llmio.db")
    make_old_db(db)

    case("造出来的确实是一份老库", lambda: (
        need(pragma_vacuum(db) == 0, f"PRAGMA auto_vacuum={pragma_vacuum(db)}"),
        need(header_flag(db)[1] == 0, f"文件头增量标志={header_flag(db)[1]}"),
        f"{os.path.getsize(db) / 1024 / 1024:.1f} MiB，auto_vacuum=0",
    ))

    llmio = Llmio(work, OLD_PORT)
    llmio.start()
    try:
        case("默认启动不动老库的 auto_vacuum", lambda: (
            need(pragma_vacuum(db) == 0, f"被改成了 {pragma_vacuum(db)}"),
            need(header_flag(db)[1] == 0, f"文件头增量标志变成了 {header_flag(db)[1]}"),
            "pragma=0，文件头标志=0",
        ))
        case("老库的历史行一行没动", lambda: (
            need(q1(ro(db), "SELECT count(*), sum(length(pad)) FROM legacy") == (200, 200 * 8192),
                 "legacy 表的行数或字节数变了"),
            "200 行 / 1.6 MiB 原样",
        ))
        case("默认启动没留下任何存储层记录（idle 才是实话）", lambda: (
            need(status(OLD_PORT)["reclaim"]["status"] == "idle",
                 f"记了一条 {status(OLD_PORT)['reclaim']['status']}"),
            "reclaim.status=idle",
        ))

        # 造一个真实的 freelist，再让用户点回收——老库上它必须是**空操作**，
        # 而且要把这件事说出来，而不是"点了、秒回、什么都没变"。
        with rw(db) as c:
            c.execute("DELETE FROM legacy WHERE id % 2 = 0")
            c.commit()
            free_before = q1(c, "PRAGMA freelist_count")[0]
        need(free_before > 0, "现场没造对：freelist 是空的")
        size_before = os.path.getsize(db)

        http(OLD_PORT, "POST", "/api/logs/compression/reclaim")
        wait_idle(OLD_PORT, "reclaiming")
        r = status(OLD_PORT)["reclaim"]

        case("老库上点回收：如实报 no_auto_vacuum，且文件一字节不变", lambda: (
            need(r["stop_reason"] == "no_auto_vacuum", f"报的是 {r['stop_reason']}"),
            need(r["freed_pages"] == 0, f"说放掉了 {r['freed_pages']} 页"),
            need(os.path.getsize(db) == size_before, "文件大小变了"),
            f"freelist={free_before} 页仍在，文件 {size_before / 1024 / 1024:.1f} MiB 未变",
        ))
    finally:
        llmio.stop()

    # 默认启动的**幂等性**：同一份库连起两次，第二次必须一个字节都不改。
    # 这条比"哈希等于原始文件"更能说明问题——llmio 启动本来就会建它自己的表、
    # 补它自己的配置行，那些是既有行为；这里要钉的是"存储层不再多写任何东西"。
    sha_first = file_sha(db)

    again = Llmio(work, OLD_PORT)
    again.start()
    again.stop()
    case("默认启动是幂等的（连起两次，文件哈希相同）", lambda: (
        need(file_sha(db) == sha_first, "第二次启动改了库文件"),
        f"sha256={sha_first[:16]}…",
    ))

    # 显式要求时才转换。
    conv = Llmio(work, OLD_PORT, env_extra={"DB_AUTO_VACUUM_REBUILD": "on"})
    conv.start()
    # 抄在**服务还开着**的时候：下面那条"拼错的开关不写新记录"要用它当基线，
    # 而这段 try 一退出去服务就没了。原先这一行写在 stop 之后，车直接崩在
    # "连接被拒绝"上——那不是被测对象的问题，是车自己的问题。
    record_before_typo = status(OLD_PORT)["reclaim"]
    try:
        st = status(OLD_PORT)
        case("显式 on 才转换，且转换落进文件头（新连接也读得到）", lambda: (
            need(pragma_vacuum(db) == 2, f"PRAGMA auto_vacuum={pragma_vacuum(db)}"),
            need(header_flag(db)[1] == 1, f"文件头增量标志={header_flag(db)[1]}"),
            need(st["reclaim"]["stop_reason"] == "converted", f"记录是 {st['reclaim']['stop_reason']}"),
            need(st["reclaim"]["source"] == "startup", f"来源是 {st['reclaim']['source']}"),
            f"converted / {st['reclaim']['duration_ms']}ms",
        ))
        case("转换（VACUUM）之后老数据完好，而且页已归位", lambda: (
            need(q1(ro(db), "SELECT count(*), sum(length(pad)) FROM legacy") == (100, 100 * 8192),
                 "转换后 legacy 表的内容对不上"),
            need(q1(ro(db), "PRAGMA freelist_count")[0] == 0, "VACUUM 之后 freelist 还非空"),
            "100 行 / 819200 字节原样，freelist=0",
        ))
    finally:
        conv.stop()

    # 拼错的值一律当 auto（保守）。这条不能用"文件哈希没变"来验：库这时已经
    # 转换过了，freelist 也是 0，就算真跑一次 VACUUM，出来的文件也是逐字节一样的
    # ——那样这条断言会永远为真，等于没测。要钉的是**没写新记录**这件事。
    sha_before_typo = file_sha(db)

    typo = Llmio(work, OLD_PORT, env_extra={"DB_AUTO_VACUUM_REBUILD": "YES"})
    typo.start()
    try:
        typo_record = status(OLD_PORT)["reclaim"]
        case("拼错的 DB_AUTO_VACUUM_REBUILD 不会触发任何动作", lambda: (
            need(typo_record == record_before_typo,
                 f"记录变了：{record_before_typo['finished_at']} → {typo_record['finished_at']}"),
            need(file_sha(db) == sha_before_typo, "文件哈希变了"),
            "值 'YES' 走 auto 分支：只认 on/off 两个字面量，没写新记录",
        ))
    finally:
        typo.stop()


# ── 主流程 ─────────────────────────────────────────────────────────────────


def main():
    # 控制台是 GBK 时中文和 ✓/✗ 都会炸在 print 上，先把它掰成 UTF-8（同 run_matrix.py）
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    shutil.rmtree(RUN_DIR, ignore_errors=True)
    os.makedirs(os.path.join(RUN_DIR, "new"), exist_ok=True)
    os.makedirs(os.path.join(RUN_DIR, "old"), exist_ok=True)

    print("构建 llmio …")
    subprocess.run(["go", "build", "-o", BIN, "."], cwd=REPO, check=True)

    check_new(os.path.join(RUN_DIR, "new"))
    check_old(os.path.join(RUN_DIR, "old"))

    failed = [r for r in RESULTS if r[1] == "fail"]
    print(f"\n{len(RESULTS) - len(failed)}/{len(RESULTS)} 通过")
    for label, _, detail in failed:
        print(f"  ✗ {label}\n      {detail}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())

/**
 * 数据库压缩卡片用到的实测基准与估算。
 *
 * 放在 lib/ 而非卡片组件里，理由与 `format.ts` 一样：这些是**能被算错而不会报错**
 * 的纯函数（两档估算的账完全不同），要能直接被测试按住；而且组件文件里导出函数
 * 会破坏 Fast Refresh（react-refresh/only-export-components）。
 */

/**
 * 四个动作的耗时基准，全部来自真机实测，出处写在每个常数后面。
 *
 * 它们只用来给一个**量级**，不是承诺：同一件事的逐轮差异能到 7 倍（增量回收
 * 实测 616–4,152 页/秒）。所以文案里一律写"约"，并把折算依据一并说出来——
 * 一个不说来源的"预计 30 分钟"和没说一样，用户没法判断该不该现在点。
 *
 * 导出成常量是为了让测试能直接断言它们与 `docs/` 里的实测值一致：四个数散在
 * 四段文案里，改错一个不会有任何东西报错。
 */
export const MIGRATE_ROWS_PER_SEC = 346 // 阶段 4：12,480 行 / 36.052s
export const ROLLBACK_ROWS_PER_SEC = 254 // 真机回滚：12,469 行 / 49s
export const RECLAIM_PAGES_PER_SEC = 1500 // 阶段 5：十轮 1,345,736 页 / 881s
/** 一轮回收的时间预算，与后端 reclaimBudget 同值。 */
export const RECLAIM_BUDGET_SEC = 90
/**
 * 一个事务里连打几条语句的**上限**，与后端 reclaimBatchMax 同值（一条恰好放一页）。
 *
 * 后端**不是**定死这个数：它从 64 起步、按每批的实测耗时自己走，落在 16–512
 * 之间（快库上顶着上限，慢库上收到 100 上下）。这里估算取上限是有意的，
 * 但要知道偏在哪一侧——它让批数偏少、松手的时间也算得偏少，而在慢库上
 * `RECLAIM_PAGES_PER_SEC` 同样偏乐观。两边偏的是同一个方向，所以整个估算是
 * **下限**。这个方向是对的：说了"约 15 分钟"而实际拖到 25 分钟，比反过来强。
 */
export const RECLAIM_PAGES_PER_BATCH = 512
/** 持续模式下每批之间松手多久，与后端 reclaimContinuousPause 同值。 */
export const RECLAIM_PAUSE_SEC = 0.1

/**
 * 重整（VACUUM）的耗时基准，同样来自真机实测：7.06 GiB 的库 55–59 秒（取中 57 s），
 * 折约 133 MB/s。它比增量回收快一个数量级（同一份库 881 秒），因为它是顺序重写整库，
 * 而增量回收是"把文件尾部的页搬进洞里"的海量随机 I/O。
 *
 * **这个数比回收那两个可靠**：顺序重写的吞吐不取决于空洞散不散，而那正是
 * RECLAIM_PAGES_PER_SEC 抖 7 倍的原因。它仍然只是个量级——文案里照旧写"约"。
 */
export const VACUUM_BYTES_PER_SEC = 133_000_000

/**
 * 重整要的空闲磁盘：2 × 库大小 + 余量，与后端 enoughDisk / reclaimDiskMargin 同值。
 *
 * 2 × 是因为 VACUUM 全程持有一份和库等大的临时副本（实测峰值额外占用 1.00× 库），
 * 余量是给日志与别的一点点写入留的。**这条判据错了的后果是 VACUUM 中途磁盘满**
 * ——库被留在一个说不清的状态，所以宁可算紧一点（宁可误禁，不可误放）。
 */
export const VACUUM_NEED_FACTOR = 2
export const VACUUM_DISK_MARGIN = 64 << 20

/** 估算重整要跑多久（秒）。至少有 1 秒，好在界面上给个非零的数。 */
export function estimateVacuumSec(fileSize: number): number {
  if (!Number.isFinite(fileSize) || fileSize <= 0) return 0
  return Math.max(1, Math.round(fileSize / VACUUM_BYTES_PER_SEC))
}

/** 这一次重整要的空闲字节。 */
export function vacuumNeedBytes(fileSize: number): number {
  if (!Number.isFinite(fileSize) || fileSize <= 0) return 0
  return VACUUM_NEED_FACTOR * fileSize + VACUUM_DISK_MARGIN
}

/**
 * 空闲磁盘够不够跑这一次重整。
 *
 * `diskFree <= 0` 一律算不够：那表示后端**量不到**（statfs 失败），而不是"空间无限"。
 * 把量不到当充足，用户就会白点一次、甚至写盘写到一半满；反过来误禁的代价只是
 * "这一条路暂时不能用"，而增量回收那条路还在。
 */
export function hasRoomForVacuum(fileSize: number, diskFree: number): boolean {
  if (!Number.isFinite(diskFree) || diskFree <= 0) return false
  return diskFree >= vacuumNeedBytes(fileSize)
}

/** 估算迁移/回滚要跑多久（秒）。至少有 1 秒，好在界面上给个非零的数。 */
export function estimateRowsSec(rows: number, rowsPerSec: number): number {
  if (!Number.isFinite(rows) || rows <= 0) return 0
  return Math.max(1, Math.round(rows / rowsPerSec))
}

/**
 * 估算回收要跑多少、累计多久。两种模式的账**不是一回事**，所以分开算。
 *
 *   - **单轮**：一轮封顶 90 秒，一个 5 GiB 的洞要十轮。用户想收干净就得点十次
 *     （或者改点持续）。不把这个数写出来，他点一次、看到"到点收工、还剩一大截"，
 *     只会以为它坏了——真机上就是这么问上来的。
 *   - **持续**：一轮恒等于一批（一批放多少页 = 一个事务里连打几条语句），
 *     所以"跑多少"是**批数**。耗时 = 搬页的时间 + 每批歇的那 0.1 秒。后者不是
 *     零头：真机那趟 2,628 批，光松手就 4 分多钟。
 */
export function estimateReclaim(
  pages: number,
  continuous: boolean
): { rounds: number; seconds: number } {
  if (!Number.isFinite(pages) || pages <= 0) return { rounds: 0, seconds: 0 }
  if (!continuous) {
    const rounds = Math.max(1, Math.ceil(pages / (RECLAIM_PAGES_PER_SEC * RECLAIM_BUDGET_SEC)))
    return { rounds, seconds: rounds * RECLAIM_BUDGET_SEC }
  }
  const rounds = Math.max(1, Math.ceil(pages / RECLAIM_PAGES_PER_BATCH))
  const seconds = pages / RECLAIM_PAGES_PER_SEC + rounds * RECLAIM_PAUSE_SEC
  return { rounds, seconds: Math.round(seconds) }
}

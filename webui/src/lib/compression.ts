/**
 * 数据库压缩卡片用到的实测基准与估算。
 *
 * 放在 lib/ 而非卡片组件里，理由与 `format.ts` 一样：这些是**能被算错而不会报错**
 * 的纯函数（两档估算的账完全不同），要能直接被测试按住；而且组件文件里导出函数
 * 会破坏 Fast Refresh（react-refresh/only-export-components）。
 */

/**
 * 三个动作的耗时基准，全部来自真机实测，出处写在每个常数后面。
 *
 * 它们只用来给一个**量级**，不是承诺：同一件事的逐轮差异能到 7 倍（增量回收
 * 实测 616–4,152 页/秒）。所以文案里一律写"约"，并把折算依据一并说出来——
 * 一个不说来源的"预计 30 分钟"和没说一样，用户没法判断该不该现在点。
 *
 * 导出成常量是为了让测试能直接断言它们与 `docs/` 里的实测值一致：三个数散在
 * 三段文案里，改错一个不会有任何东西报错。
 */
export const MIGRATE_ROWS_PER_SEC = 346 // 阶段 4：12,480 行 / 36.052s
export const ROLLBACK_ROWS_PER_SEC = 254 // 真机回滚：12,469 行 / 49s
export const RECLAIM_PAGES_PER_SEC = 1500 // 阶段 5：十轮 1,345,736 页 / 881s
/** 一轮回收的时间预算，与后端 reclaimBudget 同值。 */
export const RECLAIM_BUDGET_SEC = 90
/** 一个事务里连打几条语句，与后端 reclaimPagesPerTx 同值（一条恰好放一页）。 */
export const RECLAIM_PAGES_PER_BATCH = 512
/** 持续模式下每批之间松手多久，与后端 reclaimContinuousPause 同值。 */
export const RECLAIM_PAUSE_SEC = 0.1

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
 *   - **持续**：一轮恒等于一批（512 条语句恰好放 512 页），所以"跑多少"是**批数**。
 *     耗时 = 搬页的时间 + 每批歇的那 0.1 秒。后者不是零头：真机那趟 2,628 批，
 *     光松手就 4 分多钟。
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

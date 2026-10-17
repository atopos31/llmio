import { useEffect, useRef } from "react"

/**
 * 定时刷新。
 *
 * 总览、日志、配额三页都要轮询，原先是两份复制粘贴的实现——而轮询最容易在
 * 复制中走样，因此收敛成这一份。四条语义约定：
 *
 * - **隐藏页不轮询。** 标签页切走后后台照发请求，既浪费上游配额，也刷出
 *   没人看的数据。`document.hidden` 是唯一可靠的信号（不依赖 focus/blur，
 *   后者在切窗口而非切标签时不可靠）。
 * - **回前台立即补一次。** 切走期间错过的 N 次不会补发，但回到前台不等下一个
 *   整点——否则用户会盯着上一次的旧数字多等一个间隔。
 * - **回调走 ref。** 调用方传入的回调通常闭包着当前状态。若把回调放进 effect
 *   依赖，每次渲染都会销毁并重建计时器，间隔被无限重置，用户设了 60 秒却
 *   永远等不到那一次。依赖只留 `sec`。
 * - **卸载即清理。** 否则组件已销毁，定时器还会去调用 setState。
 *
 * `sec <= 0` 表示关闭：不创建任何定时器、不监听可见性。
 *
 * 用递归 setTimeout 而不是 setInterval：setInterval 在前一次回调还在跑时
 * 会排队堆叠（一次慢请求就能把回调攒出好几份），而递归计时天然串行。
 */
export function useAutoRefresh(sec: number, tick: () => void): void {
  const tickRef = useRef(tick)
  tickRef.current = tick

  useEffect(() => {
    if (!Number.isFinite(sec) || sec <= 0) return

    let timer: ReturnType<typeof setTimeout> | undefined

    const stop = () => {
      if (timer !== undefined) {
        clearTimeout(timer)
        timer = undefined
      }
    }
    const schedule = () => {
      stop()
      timer = setTimeout(() => {
        timer = undefined
        tickRef.current()
        schedule()
      }, sec * 1000)
    }

    const onVisibilityChange = () => {
      if (document.hidden) {
        stop()
      } else {
        // 回前台：先补一次数据，再重新排下一个间隔
        stop()
        tickRef.current()
        schedule()
      }
    }

    document.addEventListener("visibilitychange", onVisibilityChange)
    // 首次挂载时就已可见才启动；隐藏状态下等 visibilitychange 触发
    if (!document.hidden) schedule()

    return () => {
      document.removeEventListener("visibilitychange", onVisibilityChange)
      stop()
    }
  }, [sec])
}

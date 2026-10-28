/**
 * 版本号是否像一次「正式发布」。
 *
 * 更新检查的做法是拿 GitHub 的 latest release 标签和本地版本号比。能比出
 * 结论的前提是两者由同一套命名产生：上游的发布标签是 v0.10.1 这种形式，
 * 而自建构建的版本号来自镜像 build-arg——beta 镜像注入
 * beta-20261027-f4a96c6，本地 `go run` 是 dev。
 *
 * 这两种版本号永远不可能等于任何发布标签，比下去只会得出「有新版本
 * v0.10.1 可用」这种把旧标签当成新版本的结论：beta 分支永远领先于最新
 * 发布，界面上的提示却一直挂着，永不消失（用户点了「稍后提醒」也只是
 * 24 小时后再来一次）。
 *
 * 因此更新检查只在正式发布构建上做，见 routes/layout.tsx 的 checkForUpdates。
 */
const RELEASE_VERSION = /^v\d+\.\d+\.\d+$/

/** 只认发布标签形态 vMAJOR.MINOR.PATCH；dev、beta-<日期>-<sha> 一律不算。 */
export function isReleaseVersion(version: string): boolean {
  return RELEASE_VERSION.test(version.trim())
}

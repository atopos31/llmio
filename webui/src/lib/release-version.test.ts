import { describe, expect, it } from "vitest"

import { isReleaseVersion } from "@/lib/release-version"

describe("isReleaseVersion", () => {
  it("认可发布标签形态", () => {
    for (const version of ["v0.10.1", "v1.0.0", "v0.0.0", " v0.10.1 "]) {
      expect(isReleaseVersion(version), version).toBe(true)
    }
  })

  it("拒绝自建构建与开发版本的版本号", () => {
    for (const version of [
      "dev",
      "",
      "beta-20261027-f4a96c6",
      "beta",
      "pinned",
      "0.10.1",
      "v0.10",
      "v0.10.1-rc1",
      "v0.10.1+meta",
    ]) {
      expect(isReleaseVersion(version), version).toBe(false)
    }
  })
})

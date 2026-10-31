import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip";

function Tip() {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button">帮助</button>
      </TooltipTrigger>
      <TooltipContent>说明文字</TooltipContent>
    </Tooltip>
  );
}

describe("Tooltip 聚焦行为", () => {
  it("程序或鼠标聚焦不自动展开帮助文案", async () => {
    const realMatches = Element.prototype.matches;
    const spy = vi
      .spyOn(Element.prototype, "matches")
      .mockImplementation(function (this: Element, selector: string) {
        if (selector === ":focus-visible") return false;
        return realMatches.call(this, selector);
      });

    try {
      render(<Tip />);
      screen.getByRole("button", { name: "帮助" }).focus();
      await waitFor(() =>
        expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()
      );
    } finally {
      spy.mockRestore();
    }
  });

  it("键盘聚焦仍然展开帮助文案", async () => {
    const realMatches = Element.prototype.matches;
    const spy = vi
      .spyOn(Element.prototype, "matches")
      .mockImplementation(function (this: Element, selector: string) {
        if (selector === ":focus-visible") return true;
        return realMatches.call(this, selector);
      });

    try {
      render(<Tip />);
      screen.getByRole("button", { name: "帮助" }).focus();
      expect(await screen.findByRole("tooltip")).toHaveTextContent("说明文字");
    } finally {
      spy.mockRestore();
    }
  });
});

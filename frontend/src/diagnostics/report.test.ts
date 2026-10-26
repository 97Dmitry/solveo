// @vitest-environment jsdom
import { expect, it, vi } from "vite-plus/test";
import { createReporter, installGlobalHandlers, type RendererErrorReport } from "./report";

it("bounds report size and rate and survives a failing sink", async () => {
  const reports: RendererErrorReport[] = [];
  let now = 0;
  const report = createReporter(
    async (r) => {
      reports.push(r);
      throw new Error("bridge down");
    },
    () => now,
  );
  report("render", new TypeError("x".repeat(5000)), "at Component");
  for (let i = 0; i < 20; i++) report("error", `message ${i}`);
  expect(reports).toHaveLength(10);
  expect(reports[0]?.kind).toBe("render");
  expect(reports[0]?.message.startsWith("TypeError: x")).toBe(true);
  expect(reports[0]?.message.length).toBeLessThanOrEqual(2049);
  expect(reports[0]?.stack.length).toBeLessThanOrEqual(4096 + 2 + "at Component".length);
  expect(reports[0]?.stack).toContain("at Component");
  now = 10_000;
  report("rejection", { reason: "late" });
  expect(reports.at(-1)).toEqual({ kind: "rejection", message: '{"reason":"late"}', stack: "" });
  await Promise.resolve();
});

it("reports window errors and unhandled rejections until cleanup", () => {
  const report = vi.fn();
  // A separate target: errors dispatched on window would reach the test runner.
  const target = new EventTarget();
  const cleanup = installGlobalHandlers(report, target);
  const error = new Error("boom");
  target.dispatchEvent(new ErrorEvent("error", { error, message: "boom" }));
  const rejection = new Event("unhandledrejection");
  Object.assign(rejection, { reason: "denied" });
  target.dispatchEvent(rejection);
  expect(report).toHaveBeenNthCalledWith(1, "error", error);
  expect(report).toHaveBeenNthCalledWith(2, "rejection", "denied");
  cleanup();
  target.dispatchEvent(new ErrorEvent("error", { error, message: "boom" }));
  expect(report).toHaveBeenCalledTimes(2);
});

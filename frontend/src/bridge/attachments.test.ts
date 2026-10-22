// @vitest-environment jsdom
import { describe, expect, it } from "vite-plus/test";
import { imageBase64, maxImageBytes } from "./attachments";

describe("image file bytes", () => {
  it("reads content without trusting name or MIME", async () => {
    expect(await imageBase64(new File([new Uint8Array([137, 80, 78, 71])], "test.txt"))).toBe(
      "iVBORw==",
    );
  });
  it("rejects size before reading", async () => {
    const file = new File(["x"], "large.png");
    Object.defineProperty(file, "size", { value: maxImageBytes + 1 });
    await expect(imageBase64(file)).rejects.toThrow("32 МиБ");
    await expect(imageBase64(new File([], "empty.png"))).rejects.toThrow("пуст");
  });
});

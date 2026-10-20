// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vite-plus/test";
import { Markdown } from "./Markdown";
afterEach(cleanup);
it("blocks remote images, raw HTML and unsafe links", () => {
  const { container } = render(
    <Markdown
      text={
        "![track](https://example.com/pixel.png)\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))"
      }
      onCopy={async () => true}
      onOpenLink={() => {}}
    />,
  );
  expect(container.querySelector("img,script,iframe")).toBeNull();
  expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
});

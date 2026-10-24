// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { hotkeyActions } from "../bridge/state";
import { snapshotFixture } from "../bridge/test-fixture";
import { hotkeyLabels } from "../settings/hotkeyLabels";
import { HelpPanel } from "./HelpPanel";
afterEach(cleanup);

it("documents every global shortcut with its current binding", async () => {
  const hotkeys = { ...snapshotFixture().settings.hotkeys, "toggle-listening": "Ctrl+Alt+L" };
  const onEdit = vi.fn();
  render(
    <HelpPanel hotkeys={hotkeys} errors={{ "quick-region": "занято" }} onEditHotkeys={onEdit} />,
  );
  for (const action of hotkeyActions) {
    const row = screen.getByText(hotkeyLabels[action]).closest(".help-key");
    expect(row, action).toBeTruthy();
    const binding = hotkeys[action];
    if (row instanceof HTMLElement) {
      if (binding) expect(within(row).getByText(binding).tagName).toBe("KBD");
      else expect(within(row).getByText("не назначено")).toBeTruthy();
    }
  }
  expect(screen.getAllByText(hotkeyLabels["send-transcript"])).toHaveLength(1);
  expect(screen.getByText("Не работает: занято")).toBeTruthy();
  expect(screen.getByText(/Нажмите Ctrl\+Alt\+Space или кнопку «Новые реплики»/)).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "Изменить сочетания" }));
  expect(onEdit).toHaveBeenCalledTimes(1);
});

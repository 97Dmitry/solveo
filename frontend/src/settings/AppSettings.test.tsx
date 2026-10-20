// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { AppSettings } from "./AppSettings";
import { snapshotFixture } from "../bridge/test-fixture";
afterEach(cleanup);
it("changes interface scale independently of text size and restores the saved value", async () => {
  const snapshot = snapshotFixture();
  snapshot.settings.ui_scale = 125;
  const save = vi.fn();
  render(
    <AppSettings
      snapshot={snapshot}
      onSettings={save}
      onCheck={vi.fn()}
      onLog={vi.fn()}
      onRetrySave={vi.fn()}
      onDiscard={vi.fn()}
      onExit={vi.fn()}
    />,
  );
  expect(screen.getByLabelText<HTMLSelectElement>("Масштаб интерфейса").value).toBe("125");
  await userEvent.selectOptions(screen.getByLabelText("Масштаб интерфейса"), "150");
  expect(save).toHaveBeenCalledWith({ ...snapshot.settings, ui_scale: 150 });
});

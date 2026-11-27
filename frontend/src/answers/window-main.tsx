import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AnswersWindow } from "./AnswersWindow";
import { reportAnswersError } from "../bridge/wails";
import { RenderBoundary } from "../diagnostics/Boundary";
import { installGlobalHandlers } from "../diagnostics/report";
import "../app/theme.css";
import "./window.css";

const root = document.getElementById("root");
if (!root) throw new Error("Missing answers window root");
installGlobalHandlers(reportAnswersError);
createRoot(root).render(
  <StrictMode>
    <RenderBoundary report={reportAnswersError}>
      <AnswersWindow />
    </RenderBoundary>
  </StrictMode>,
);

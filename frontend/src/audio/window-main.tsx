import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AudioWindow } from "./AudioWindow";
import { reportAudioError } from "../bridge/wails";
import { RenderBoundary } from "../diagnostics/Boundary";
import { installGlobalHandlers } from "../diagnostics/report";
import "../app/theme.css";
import "./window.css";
const root = document.getElementById("root");
if (!root) throw new Error("Missing audio window root");
installGlobalHandlers(reportAudioError);
createRoot(root).render(
  <StrictMode>
    <RenderBoundary report={reportAudioError}>
      <AudioWindow />
    </RenderBoundary>
  </StrictMode>,
);

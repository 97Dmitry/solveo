import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.tsx";
import { reportMainError } from "./bridge/wails";
import { RenderBoundary } from "./diagnostics/Boundary";
import { installGlobalHandlers } from "./diagnostics/report";

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
installGlobalHandlers(reportMainError);
createRoot(root).render(
  <StrictMode>
    <RenderBoundary report={reportMainError}>
      <App />
    </RenderBoundary>
  </StrictMode>,
);

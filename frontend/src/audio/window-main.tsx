import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AudioWindow } from "./AudioWindow";
import "../app/theme.css";
import "./window.css";
const root = document.getElementById("root");
if (!root) throw new Error("Missing audio window root");
createRoot(root).render(
  <StrictMode>
    <AudioWindow />
  </StrictMode>,
);

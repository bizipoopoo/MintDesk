import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import App from "./App";

const root = document.getElementById("root")!;

const showFatalError = (error: unknown) => {
  const message = error instanceof Error ? error.stack ?? error.message : String(error);
  root.innerHTML = `<pre style="white-space:pre-wrap;color:#ffb4b4;font:13px ui-monospace,monospace;padding:28px">Mint Desk could not start:\n\n${message.replace(/</g, "&lt;")}</pre>`;
};

window.addEventListener("error", (event) => showFatalError(event.error ?? event.message));
window.addEventListener("unhandledrejection", (event) => showFatalError(event.reason));

try {
  createRoot(root).render(<StrictMode><App /></StrictMode>);
} catch (error) {
  showFatalError(error);
}

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { installClarity } from "./clarity";
import "./index.css";

installClarity();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

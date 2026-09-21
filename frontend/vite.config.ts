import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // Vite's default 5173 sits in a port range Windows reserves for Hyper-V/WSL
  // after some reboots (EACCES on listen). Wails reads the dev URL from Vite's
  // output ("frontend:dev:serverUrl": "auto"), so any free port works.
  server: { port: 3000 },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});

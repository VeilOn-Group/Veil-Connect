import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The UI calls /veilond/* (proxied to veilond by the Wails bridge). In
// browser development, run with ?mock=1 or point VEILOND_DEV at a veilond
// TCP fallback (requires the token header, see README).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "./",
  build: { outDir: "dist", sourcemap: false, assetsInlineLimit: 0 },
  server: { fs: { allow: ["..", "../../../veil-shared/design/dist"] } },
});

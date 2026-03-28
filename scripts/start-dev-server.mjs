import net from "node:net";
import { spawn } from "node:child_process";
import process from "node:process";

const host = process.env.TAURI_DEV_HOST || "127.0.0.1";
const port = 5173;

if (await isPortOpen(host, port)) {
  console.log(`[start-dev-server] reuse existing Vite server at http://${host}:${port}`);
  process.exit(0);
}

const child = spawn(npmCommand(), ["run", "dev"], {
  stdio: "inherit",
  shell: process.platform === "win32",
  env: process.env,
});

child.on("exit", (code) => {
  process.exit(code ?? 0);
});

child.on("error", (error) => {
  console.error(`[start-dev-server] failed to start dev server: ${error}`);
  process.exit(1);
});

function npmCommand() {
  return process.platform === "win32" ? "npm.cmd" : "npm";
}

function isPortOpen(host, port) {
  return new Promise((resolve) => {
    const socket = net.createConnection({ host, port });

    socket.once("connect", () => {
      socket.destroy();
      resolve(true);
    });

    socket.once("error", () => {
      socket.destroy();
      resolve(false);
    });

    socket.setTimeout(800, () => {
      socket.destroy();
      resolve(false);
    });
  });
}

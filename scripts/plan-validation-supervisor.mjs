#!/usr/bin/env node

import { spawn } from "node:child_process";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const watcher = path.join(here, "plan-validation-watch.mjs");
const args = process.argv.slice(2);
let stopping = false;
let child = null;
let restart = null;

function watcherArgs() {
  const forwarded = [];
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === "--drop-dir" && args[index + 1]) forwarded.push(args[index], path.resolve(args[++index]));
    else throw new TypeError(`unknown argument: ${args[index]}`);
  }
  return [watcher, ...forwarded];
}

function launch() {
  child = spawn(process.execPath, watcherArgs(), {
    cwd: path.resolve(here, ".."),
    stdio: "inherit",
    windowsHide: true,
  });
  child.once("exit", (code, signal) => {
    child = null;
    if (stopping) process.exit(0);
    console.error(`plan validation watcher exited (${signal || code}); restarting`);
    restart = setTimeout(launch, 1000);
  });
}

function stop(signal) {
  if (stopping) return;
  stopping = true;
  if (restart) clearTimeout(restart);
  if (child) child.kill(signal);
  else process.exit(0);
}

try {
  watcherArgs();
  launch();
  process.once("SIGINT", () => stop("SIGINT"));
  process.once("SIGTERM", () => stop("SIGTERM"));
} catch (error) {
  console.error(`plan validation supervisor failed: ${error.message}`);
  process.exit(2);
}

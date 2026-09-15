import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function readJSONL(paths) {
  const records = [];
  for (const source of [...paths].sort()) {
    const text = fs.readFileSync(source, "utf8");
    for (const [index, line] of text.split(/\r?\n/).entries()) {
      if (!line.trim()) continue;
      try { records.push(JSON.parse(line)); }
      catch (error) { throw new Error(`${source}:${index + 1}: ${error.message}`); }
    }
  }
  return records.sort((left, right) => String(left.ts || "").localeCompare(String(right.ts || "")) || Number(left.seq || 0) - Number(right.seq || 0));
}

function toolArguments(event) {
  const value = event.data?.arguments ?? event.data?.args ?? event.data?.tool_call?.arguments;
  if (typeof value === "object" && value) return value;
  try { return JSON.parse(value || "{}"); } catch { return {}; }
}

export function selectRun(records, { sessionID, runID } = {}) {
  return records.filter((event) => (!sessionID || event.session_id === sessionID) && (!runID || event.run_id === runID));
}

export function extractRun(records, filter = {}) {
  const selected = selectRun(records, filter);
  const stopped = [...selected].reverse().find((event) => event.type === "run.stopped");
  const started = selected.find((event) => event.type === "run.started");
  const modelResponses = selected.filter((event) => event.type === "model.response");
  const toolCalls = selected.filter((event) => event.type === "tool.call");
  const toolResults = selected.filter((event) => event.type === "tool.result");
  const seenReads = new Set();
  let rereads = 0;
  for (const event of toolCalls) {
    const name = event.data?.name ?? event.data?.tool_call?.name;
    if (name !== "read_file") continue;
    const target = String(toolArguments(event).path || "").replaceAll("\\", "/").toLowerCase();
    if (!target) continue;
    if (seenReads.has(target)) rereads += 1;
    else seenReads.add(target);
  }
  const promptTokens = modelResponses.reduce((sum, event) => sum + Number(event.data?.usage?.prompt_tokens || 0), 0);
  const completionTokens = modelResponses.reduce((sum, event) => sum + Number(event.data?.usage?.completion_tokens || 0), 0);
  const cachedTokens = modelResponses.reduce((sum, event) => sum + Number(event.data?.usage?.cached_tokens || 0), 0);
  const firstMS = Date.parse(started?.ts || selected[0]?.ts || "");
  const lastMS = Date.parse(stopped?.ts || selected.at(-1)?.ts || "");
  return {
    session_id: filter.sessionID || selected[0]?.session_id || "",
    run_id: filter.runID || stopped?.run_id || selected.find((event) => event.run_id)?.run_id || "",
    completion: stopped?.data?.reason === "done",
    stop_reason: stopped?.data?.reason || "missing",
    turns: Number(stopped?.data?.turns ?? Math.max(0, ...selected.filter((event) => event.type === "model.request").map((event) => Number(event.data?.turn || 0)))),
    tool_calls: toolCalls.length,
    tool_errors: toolResults.filter((event) => event.data?.ok === false).length,
    rereads,
    elapsed_ms: Number.isFinite(firstMS) && Number.isFinite(lastMS) ? Math.max(0, lastMS - firstMS) : null,
    prompt_tokens: promptTokens,
    completion_tokens: completionTokens,
    tokens: promptTokens + completionTokens,
    cached_tokens: cachedTokens,
    cache_hit: promptTokens > 0 ? cachedTokens / promptTokens : 0,
    records: selected.length,
  };
}

function parseCLI(values) {
  const result = { paths: [] };
  for (let index = 0; index < values.length; index += 1) {
    const value = values[index];
    if (value === "--session") result.sessionID = values[++index];
    else if (value === "--run") result.runID = values[++index];
    else result.paths.push(path.resolve(value));
  }
  return result;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const options = parseCLI(process.argv.slice(2));
  if (!options.paths.length) throw new Error("usage: node scripts/jsonl-extract.mjs <tape.jsonl> [more.jsonl] [--session id] [--run id]");
  process.stdout.write(`${JSON.stringify(extractRun(readJSONL(options.paths), options), null, 2)}\n`);
}

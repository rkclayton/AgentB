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

function stableValue(value) {
  if (Array.isArray(value)) return value.map(stableValue);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.keys(value).sort().map((key) => [key, stableValue(value[key])]));
}

function sameCall(left, right) {
  if (!left || !right) return false;
  const leftName = left.data?.name ?? left.data?.tool_call?.name;
  const rightName = right.data?.name ?? right.data?.tool_call?.name;
  return leftName === rightName && JSON.stringify(stableValue(toolArguments(left))) === JSON.stringify(stableValue(toolArguments(right)));
}

export function classifyToolError(value) {
  const text = String(value || "");
  if (/outside (?:the )?(?:workspace|folder|allowed root|jail)|workspace boundary|jail (?:refused|denied)|path is not under/i.test(text)) return "outside jail";
  if (/timed? out|timeout|deadline exceeded/i.test(text)) return "timeout";
  if (/syntax|parse error|parsererror|unexpected token|unterminated|invalid character/i.test(text)) return "syntax";
  if (/invalid (?:argument|arguments|input)|missing (?:argument|required)|required (?:argument|field)|unknown (?:argument|field)|cannot unmarshal|expects?\b|must (?:be|include|provide)|unsupported (?:argument|field|shape)/i.test(text)) return "bad argument";
  if (/no match|not found|does not exist|cannot find|could not find|is not recognized as (?:the name of )?a/i.test(text)) return "no match";
  return "other";
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
  const resultByCall = new Map(toolResults.map((event) => [event.data?.call_id, event]));
  const toolErrorDetails = toolResults.filter((event) => event.data?.ok === false).map((event) => {
    const call = toolCalls.find((candidate) => candidate.data?.call_id === event.data?.call_id);
    const nextCall = selected.find((candidate) => candidate.type === "tool.call" && Number(candidate.seq) > Number(event.seq));
    const nextResult = nextCall ? resultByCall.get(nextCall.data?.call_id) : undefined;
    const preview = String(event.data?.preview || event.data?.error || "");
    return {
      call_id: event.data?.call_id || "",
      tool: event.data?.name ?? call?.data?.name ?? call?.data?.tool_call?.name ?? "",
      turn: Number(event.data?.turn ?? call?.data?.turn ?? 0),
      class: classifyToolError(preview),
      arguments: toolArguments(call || {}),
      preview,
      protected_refusal: /\"refused\"\s*:\s*true|outside (?:the )?(?:workspace|folder|allowed root|jail)|workspace boundary|jail (?:refused|denied)/i.test(preview),
      repeated_next: nextResult?.data?.ok === false,
      identical_next: sameCall(call, nextCall),
      recovered_next: nextResult?.data?.ok === true,
      next_tool: nextCall?.data?.name ?? nextCall?.data?.tool_call?.name ?? "",
      next_call_id: nextCall?.data?.call_id || "",
    };
  });
  const errorClassCounts = Object.fromEntries([...new Set(toolErrorDetails.map((entry) => entry.class))].sort().map((name) => [name, toolErrorDetails.filter((entry) => entry.class === name).length]));
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
    stop: {
      reason: stopped?.data?.reason || "missing",
      detail: stopped?.data?.detail || "",
      turn: Number(stopped?.data?.turns || 0),
    },
    turns: Number(stopped?.data?.turns ?? Math.max(0, ...selected.filter((event) => event.type === "model.request").map((event) => Number(event.data?.turn || 0)))),
    tool_calls: toolCalls.length,
    tool_errors: toolResults.filter((event) => event.data?.ok === false).length,
    error_class_counts: errorClassCounts,
    tool_error_details: toolErrorDetails,
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

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const args = process.argv.slice(2);
const outputIndex = args.indexOf("--out");
const output = outputIndex >= 0 ? path.resolve(args[outputIndex + 1]) : "";
if (outputIndex >= 0) args.splice(outputIndex, 2);
if (!args.length) throw new Error("usage: node scripts/measure-read-window-cost.mjs <tape.jsonl> [more tapes...] [--out result.json]");

const samples = [];
const groups = new Map();
const tapes = [];
for (const input of args) {
  const full = path.resolve(input);
  const bytes = fs.readFileSync(full);
  tapes.push({ path: path.relative(process.cwd(), full).replaceAll("\\", "/"), bytes: bytes.length, sha256: crypto.createHash("sha256").update(bytes).digest("hex") });
  const calls = new Map();
  const successfulCalls = new Set();
  for (const line of bytes.toString("utf8").split(/\r?\n/)) {
    if (!line) continue;
    const event = JSON.parse(line);
    if (event.type === "tool.result" && event.data?.name === "read_file" && event.data?.ok === true) successfulCalls.add(event.data.call_id);
    if (event.type !== "message.appended") continue;
    const message = event.data?.message || {};
    for (const call of message.tool_calls || []) {
      if (call.name !== "read_file") continue;
      try { calls.set(call.id, JSON.parse(call.arguments || "{}")); } catch {}
    }
    if (message.role !== "tool" || message.name !== "read_file" || (message.ok !== true && !successfulCalls.has(message.tool_call_id)) || message.elided) continue;
    const header = String(message.content || "").split("\n", 1)[0];
    const offset = Number(/\boffset=(\d+)/.exec(header)?.[1]);
    const sourceBytes = Number(/\bbytes=(\d+)/.exec(header)?.[1]);
    const call = calls.get(message.tool_call_id);
    if (!call?.path || !Number.isFinite(offset) || !Number.isFinite(sourceBytes) || sourceBytes < 1 || !Number.isFinite(message.tokens)) continue;
    samples.push({ bytes: sourceBytes, tokens: Number(message.tokens) });
    const key = `${tapes.length - 1}\0${event.run_id || ""}\0${String(call.path).toLowerCase()}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push([offset - 1, offset - 1 + sourceBytes]);
  }
}
if (!samples.length) throw new Error("no successful read_file message results found");

const n = samples.length;
const sumX = samples.reduce((sum, item) => sum + item.bytes, 0);
const sumY = samples.reduce((sum, item) => sum + item.tokens, 0);
const meanX = sumX / n;
const meanY = sumY / n;
const variance = samples.reduce((sum, item) => sum + (item.bytes - meanX) ** 2, 0);
const covariance = samples.reduce((sum, item) => sum + (item.bytes - meanX) * (item.tokens - meanY), 0);
const tokensPerByte = variance ? Math.max(0, covariance / variance) : sumY / sumX;
const overheadTokens = Math.max(0, meanY - tokensPerByte * meanX);

const segments = [];
for (const ranges of groups.values()) {
  ranges.sort((a, b) => a[0] - b[0]);
  for (const range of ranges) {
    const prior = segments.at(-1);
    if (prior && prior.key === ranges && range[0] <= prior.end) prior.end = Math.max(prior.end, range[1]);
    else segments.push({ key: ranges, start: range[0], end: range[1] });
  }
}
const coveredBytes = segments.reduce((sum, item) => sum + item.end - item.start, 0);
const candidates = [16 << 10, 32 << 10, 64 << 10].map((windowBytes) => {
  const results = segments.reduce((sum, item) => sum + Math.ceil((item.end - item.start) / windowBytes), 0);
  const totalTokens = Math.round(coveredBytes * tokensPerByte + results * overheadTokens);
  return { window_bytes: windowBytes, results, tokens_per_result: Number((totalTokens / results).toFixed(1)), total_tokens: totalTokens };
});
const report = {
  method: "OLS over retained successful read_file result tokens versus source bytes; candidate totals preserve the union of observed per-run/path byte ranges and vary only result count/header overhead",
  config_changed: false,
  tapes,
  observed: { results: n, source_bytes: sumX, tokens: sumY, covered_unique_bytes: coveredBytes, fitted_tokens_per_source_byte: Number(tokensPerByte.toFixed(6)), fitted_overhead_tokens_per_result: Number(overheadTokens.toFixed(2)) },
  candidates,
};
const rendered = JSON.stringify(report, null, 2) + "\n";
if (output) {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  fs.writeFileSync(output, rendered, "utf8");
}
process.stdout.write(rendered);

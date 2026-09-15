import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { extractRun, readJSONL, selectRun } from "./jsonl-extract.mjs";

export function summarize(results, evaluateTape, tapesRoot) {
  const runs = results.map((result) => {
    const tapePath = path.resolve(tapesRoot, result.tape);
    const events = readJSONL([tapePath]);
    const selected = selectRun(events, { sessionID: result.session_id, runID: result.run_id });
    const extracted = extractRun(events, { sessionID: result.session_id, runID: result.run_id });
    const detectors = evaluateTape(selected);
    return {
      task_id: result.task_id,
      form: result.form,
      trial: result.trial,
      tape: result.tape,
      pass: result.pass === true,
      stop_reason: result.stop_reason,
      turns: extracted.turns,
      tool_errors: extracted.tool_errors,
      detectors,
    };
  });
  return aggregateRuns(runs);
}

export function aggregateRuns(runs) {
  const detectorNames = runs[0]?.detectors.map((record) => record.detector) || [];
  const detectorTable = detectorNames.map((detector) => {
    const available = runs.filter((run) => run.detectors.find((record) => record.detector === detector)?.available);
    const fired = available.filter((run) => run.detectors.find((record) => record.detector === detector)?.would_fire);
    const failedFires = fired.filter((run) => !run.pass).length;
    return {
      detector,
      available: available.length,
      passed_fires: fired.filter((run) => run.pass).length,
      failed_fires: failedFires,
      total_fires: fired.length,
      implied_precision: fired.length ? failedFires / fired.length : null,
    };
  });
  return {
    runs: runs.length,
    passed: runs.filter((run) => run.pass).length,
    failed: runs.filter((run) => !run.pass).length,
    detector_table: detectorTable,
    tool_error_stops: runs.filter((run) => run.stop_reason === "tool_errors"),
    completed_runs_with_fires: runs.filter((run) => run.stop_reason === "done" && run.detectors.some((record) => record.would_fire)),
  };
}

export function markdown(report) {
  const percent = (value) => value == null ? "n/a" : `${(value * 100).toFixed(1)}%`;
  const fired = (run) => run.detectors.filter((record) => record.would_fire).map((record) => `${record.detector}@${record.values.first_fire_turn ?? "end"}`).join(", ") || "none";
  const lines = [
    "# v0.59.0 W5 — EVAL-1 shadow-detector replay",
    "",
    `Replayed ${report.runs} tapes through \`scripts/jsonl-extract.mjs\` and the production \`internal/progress\` evaluators: ${report.passed} passed, ${report.failed} failed. New detectors remained shadow-only.`,
    "",
    "| Detector | Available | Fires on passed | Fires on failed | Total fires | Implied precision |",
    "|---|---:|---:|---:|---:|---:|",
    ...report.detector_table.map((row) => `| ${row.detector} | ${row.available} | ${row.passed_fires} | ${row.failed_fires} | ${row.total_fires} | ${percent(row.implied_precision)} |`),
    "",
    "Aux progress is unavailable because the tapes contain no aux-profile verdict. Precision treats a failed run as the positive class; these guessed thresholds are descriptive tuning evidence, not an enforcement decision.",
    "",
    `## Tool-error stops (${report.tool_error_stops.length})`,
    "",
    "| Tape | Turns | Tool errors | Detectors that would have fired earlier |",
    "|---|---:|---:|---|",
    ...report.tool_error_stops.map((run) => `| ${run.tape} | ${run.turns} | ${run.tool_errors} | ${fired(run)} |`),
    "",
    `## Completed runs with at least one shadow fire (${report.completed_runs_with_fires.length})`,
    "",
    "| Tape | Turns | Tool errors | Detectors that would have fired |",
    "|---|---:|---:|---|",
    ...report.completed_runs_with_fires.map((run) => `| ${run.tape} | ${run.turns} | ${run.tool_errors} | ${fired(run)} |`),
    "",
  ];
  return `${lines.join("\n")}\n`;
}

function parseArgs(values) {
  const options = {};
  for (let index = 0; index < values.length; index += 1) {
    const value = values[index];
    if (value === "--results") options.results = path.resolve(values[++index]);
    else if (value === "--evaluator") options.evaluator = path.resolve(values[++index]);
    else if (value === "--out") options.out = path.resolve(values[++index]);
    else throw new Error(`unknown argument: ${value}`);
  }
  if (!options.results || !options.evaluator || !options.out) throw new Error("usage: progress-replay --results results.jsonl --evaluator progress-replay.exe --out evidence-dir");
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const options = parseArgs(process.argv.slice(2));
  const results = readJSONL([options.results]);
  const tapesRoot = path.dirname(options.results);
  const evaluateTape = (events) => {
    const child = spawnSync(options.evaluator, [], { input: JSON.stringify(events), encoding: "utf8", maxBuffer: 16 * 1024 * 1024 });
    if (child.error || child.status !== 0) throw child.error || new Error(child.stderr || `evaluator exited ${child.status}`);
    return JSON.parse(child.stdout);
  };
  const report = summarize(results, evaluateTape, tapesRoot);
  fs.mkdirSync(options.out, { recursive: true });
  fs.writeFileSync(path.join(options.out, "replay.json"), `${JSON.stringify(report, null, 2)}\n`);
  fs.writeFileSync(path.join(options.out, "replay.md"), markdown(report));
  process.stdout.write(`${JSON.stringify({ runs: report.runs, passed: report.passed, failed: report.failed, tool_error_stops: report.tool_error_stops.length, completed_runs_with_fires: report.completed_runs_with_fires.length })}\n`);
}

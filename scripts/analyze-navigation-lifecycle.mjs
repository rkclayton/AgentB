import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(process.argv[2] || "logs/evidence/2026-09-11-v0320-lifecycle");
const raw = JSON.parse(await readFile(join(root, "raw.json"), "utf8"));
assert.equal(raw.schema, 2);

function classify(item) {
  if (item.navigation_committed && item.page_dom_content_loaded && item.page_load_event) return "candidate-1-telemetry-missing";
  if (item.network_loading_finished && !item.navigation_committed) return "candidate-2-finished-response-never-commits";
  if (item.navigation_committed && (!item.page_dom_content_loaded || !item.page_load_event)) return "candidate-3-commit-lifecycle-stalls";
  return "unclassified";
}

const responsePaths = raw.runs.flatMap((run) => run.correlations
  .filter((item) => item.network_response)
  .map((item) => ({ trial: run.trial, ...item })));
const noAppCompletion = responsePaths.filter((item) => !item.app_completed).map((item) => ({
  trial: item.trial,
  navigation_id: item.navigation_id,
  classification: classify(item),
  network_request_id: item.network_request?.request_id || "",
  network_loading_finished: Boolean(item.network_loading_finished),
  navigation_committed: item.navigation_committed,
  frame_navigated: item.page_frame_navigated,
  dom_content_loaded: item.page_dom_content_loaded,
  load_event: item.page_load_event,
  server_completed: Boolean(item.server_completed),
}));
assert.ok(noAppCompletion.length > 0, "run produced no response-without-app-completion cases");
assert.ok(noAppCompletion.every((item) => item.classification !== "unclassified"), "a response-without-app-completion case was not attributable");

const classifications = Object.fromEntries([
  "candidate-1-telemetry-missing",
  "candidate-2-finished-response-never-commits",
  "candidate-3-commit-lifecycle-stalls",
].map((name) => [name, noAppCompletion.filter((item) => item.classification === name).length]));
const result = {
  schema: 1,
  measured_at: new Date().toISOString(),
  build: raw.build,
  historical_class: { response_without_app_completion: 28, loading_finished: 27, note: "Historical cases have no Page-domain tape and cannot be individually backfilled." },
  repeated_measurement: {
    trials: raw.runs.length,
    navigation_starts: raw.runs.reduce((sum, run) => sum + run.app.started_count, 0),
    app_completions: raw.runs.reduce((sum, run) => sum + run.app.completed_count, 0),
    network_responses: responsePaths.length,
    response_without_app_completion: noAppCompletion.length,
    response_committed_and_loaded: responsePaths.filter((item) => item.navigation_committed && item.page_dom_content_loaded && item.page_load_event).length,
    classifications,
  },
  class_attribution: "candidate-1-telemetry-missing",
  operator_visible_stall: false,
  conclusion: "Every repeated response-without-app-completion case committed and completed DOM/load lifecycle. This class is missing app telemetry, not the visible stall. The visible stall remains within the separately parked cancellation or pre-network-queueing phenomena.",
  no_app_completion: noAppCompletion,
};
assert.equal(classifications["candidate-1-telemetry-missing"], noAppCompletion.length);
assert.equal(classifications["candidate-2-finished-response-never-commits"], 0);
assert.equal(classifications["candidate-3-commit-lifecycle-stalls"], 0);

const lines = [
  "# v0.32.0 lifecycle attribution",
  "",
  `Build: ${result.build.tag} ${result.build.commit}, dirty=${result.build.dirty}.`,
  `Repeated run: ${result.repeated_measurement.navigation_starts} starts, ${result.repeated_measurement.app_completions} app completions, ${result.repeated_measurement.network_responses} responses.`,
  `Response paths without app completion: ${noAppCompletion.length}.`,
  `Candidate 1: ${classifications["candidate-1-telemetry-missing"]}; candidate 2: ${classifications["candidate-2-finished-response-never-commits"]}; candidate 3: ${classifications["candidate-3-commit-lifecycle-stalls"]}.`,
  "",
  result.conclusion,
  "",
  "## Per-navigation attribution",
  "",
  ...noAppCompletion.map((item) => `- trial ${item.trial} ${item.navigation_id}: ${item.classification}; loadingFinished=${item.network_loading_finished}; committed=${item.navigation_committed}; DOMContentLoaded=${Boolean(item.dom_content_loaded)}; load=${Boolean(item.load_event)}.`),
  "",
  "The 28 historical IDs cannot be backfilled because their retained tape contains no Page-domain events. Their class is attributed by the repeated equivalent measurement; no historical event is invented.",
  "",
];
await writeFile(join(root, "analysis.json"), JSON.stringify(result, null, 2), { flag: "wx" });
await writeFile(join(root, "result.md"), lines.join("\n"), { flag: "wx" });
process.stdout.write(`${JSON.stringify({ build: result.build, historical_class: result.historical_class, repeated_measurement: result.repeated_measurement, class_attribution: result.class_attribution, operator_visible_stall: result.operator_visible_stall, conclusion: result.conclusion }, null, 2)}\n`);

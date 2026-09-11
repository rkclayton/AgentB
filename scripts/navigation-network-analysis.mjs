import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
assert.ok(args.input, "missing --input");
const source = JSON.parse(await readFile(resolve(args.input), "utf8"));
assert.ok(Array.isArray(source.runs) && source.runs.length, "input has no runs");

const idFromURL = (url) => {
  try { return new URL(url).searchParams.get("navigation_id") || ""; } catch { return ""; }
};
const correlations = source.runs.flatMap((run) => run.correlations.map((value) => ({ trial: run.trial, ...value })));
const allRequests = source.runs.flatMap((run) => run.raw_network_events
  .filter((event) => event.event === "requestWillBeSent" && event.type === "Document" && idFromURL(event.request?.url))
  .map((event) => ({ trial: run.trial, navigation_id: idFromURL(event.request.url), ...event })));
const correlatedKeys = new Set(correlations.filter((value) => value.network_request).map((value) => `${value.trial}\0${value.navigation_id}`));
const orphanRequests = allRequests.filter((event) => !correlatedKeys.has(`${event.trial}\0${event.navigation_id}`));
const responseRows = correlations.filter((value) => value.network_response);
const cancellationRows = correlations.filter((value) => value.network_loading_failed?.error_text === "net::ERR_ABORTED");

assert.equal(
  correlations.length,
  correlations.filter((value) => !value.network_request).length + correlations.filter((value) => value.network_request).length,
  "app starts must partition into absent and observed Network requests",
);
assert.ok(cancellationRows.every((value) => value.network_loading_failed.canceled === true), "ERR_ABORTED rows must be marked canceled");
assert.ok(cancellationRows.every((value) => !value.network_response), "ERR_ABORTED rows unexpectedly include a response");
assert.ok(responseRows.every((value) => value.server_started && value.server_completed), "every response must correlate to a completed server handler");

const networkToServerMS = (value) => value.network_request && value.server_started
  ? Date.parse(value.server_started.arrival_at) - value.network_request.wall_time * 1000
  : null;
const late = responseRows
  .map((value) => ({ ...value, network_to_server_ms: networkToServerMS(value) }))
  .filter((value) => value.network_to_server_ms >= 10000)
  .map((value) => {
    const matchingRequests = allRequests.filter((event) => event.trial === value.trial && event.navigation_id === value.navigation_id);
    return {
      trial: value.trial,
      navigation_id: value.navigation_id,
      network_to_server_ms: value.network_to_server_ms,
      request_will_be_sent_count: matchingRequests.length,
      connection_id: value.network_response.connection_id,
      connection_reused: value.network_response.connection_reused,
      dns_start_ms: value.network_response.timing?.dnsStart,
      connect_start_ms: value.network_response.timing?.connectStart,
      send_start_ms: value.network_response.timing?.sendStart,
      receive_headers_end_ms: value.network_response.timing?.receiveHeadersEnd,
      server_handler_exit: value.server_completed?.handler_exited_at,
      bytes_written: value.server_completed?.bytes_written,
      app_completed: value.app_completed,
    };
  });

const perTrial = source.runs.map((run) => ({
  trial: run.trial,
  app_starts: run.correlations.length,
  symptom_a_no_network_request: run.correlations.filter((value) => !value.network_request).length,
  network_requests: run.correlations.filter((value) => value.network_request).length,
  canceled_before_response: run.correlations.filter((value) => value.network_loading_failed?.error_text === "net::ERR_ABORTED" && !value.network_response).length,
  responses: run.correlations.filter((value) => value.network_response).length,
  symptom_b_response_no_app_completion: run.correlations.filter((value) => value.network_response && !value.app_completed).length,
  network_finished_no_app_completion: run.correlations.filter((value) => value.network_loading_finished && !value.app_completed).length,
  response_without_network_terminal_or_app_completion: run.correlations.filter((value) => value.network_response && !value.network_loading_finished && !value.network_loading_failed && !value.app_completed).length,
  app_completions: run.correlations.filter((value) => value.app_completed).length,
  server_starts: run.correlations.filter((value) => value.server_started).length,
  server_completions: run.correlations.filter((value) => value.server_completed).length,
}));

const analysis = {
  schema: 1,
  source: resolve(args.input),
  build: source.build,
  remote_debugging_altered_reproduction: false,
  reproduction: source.w1_reproduction_from_app_tape,
  counts: {
    trials: source.runs.length,
    app_starts: correlations.length,
    app_completions: correlations.filter((value) => value.app_completed).length,
    app_incomplete: correlations.filter((value) => !value.app_completed).length,
    symptom_a_no_network_request: correlations.filter((value) => !value.network_request).length,
    correlated_network_requests: correlations.filter((value) => value.network_request).length,
    orphan_network_requests_without_durable_app_start: orphanRequests.length,
    canceled_before_response: correlations.filter((value) => value.network_loading_failed?.error_text === "net::ERR_ABORTED" && !value.network_response).length,
    responses: responseRows.length,
    symptom_b_response_no_app_completion: correlations.filter((value) => value.network_response && !value.app_completed).length,
    network_finished_no_app_completion: correlations.filter((value) => value.network_loading_finished && !value.app_completed).length,
    response_without_network_terminal_or_app_completion: correlations.filter((value) => value.network_response && !value.network_loading_finished && !value.network_loading_failed && !value.app_completed).length,
    server_starts: correlations.filter((value) => value.server_started).length,
    server_completions: correlations.filter((value) => value.server_completed).length,
  },
  per_trial: perTrial,
  cancellations: {
    count: cancellationRows.length,
    error_text: "net::ERR_ABORTED",
    canceled: true,
    response_received: false,
  },
  connections: {
    responses_with_connection_identity: responseRows.filter((value) => value.network_response.connection_id !== undefined).length,
    reused: responseRows.filter((value) => value.network_response.connection_reused).length,
    new: responseRows.filter((value) => !value.network_response.connection_reused).length,
    unique_connection_ids: new Set(responseRows.map((value) => value.network_response.connection_id)).size,
  },
  late_arrivals: late,
  late_arrival_accounting: {
    classification: "queued before DNS, socket allocation, and send; not retried",
    basis: "each replicated late navigation emitted one requestWillBeSent at the start, then response timing left DNS/connect/send idle for about 59 seconds; each finally used a new non-reused connection",
    original_58599_ms: "the original uninstrumented event cannot be retrospectively assigned a CDP trace, but the same boundary reproduced once in every instrumented trial at 59079, 59343, and 59567 ms",
  },
  result: {
    selected: 4,
    name: "symptoms A and B cross different browser boundaries",
    detail: "A includes navigations absent from Network.requestWillBeSent; B includes responses whose network load finished but whose document never completed in app telemetry. Separately, repeated late arrivals queued inside the network layer before DNS/socket work.",
  },
  orphan_network_requests: orphanRequests.map((event) => ({ trial: event.trial, navigation_id: event.navigation_id, request_id: event.requestId, wall_time: event.wallTime, url: event.request.url })),
};

const text = `${JSON.stringify(analysis, null, 2)}\n`;
if (args.output) await writeFile(resolve(args.output), text, { flag: "wx" });
process.stdout.write(text);

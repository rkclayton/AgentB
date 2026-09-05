import { modelTurnKey } from "./reasoning.js";

const CHARS_PER_TOKEN = 3.6;
const RATE_WINDOW_MS = 5000;

export function applyStreamEvent(session, event, receivedAt = Date.now()) {
  const data = event?.data || {};
  if (event?.type === "model.request") {
    session._streamTelemetry = {
      key: modelTurnKey(event),
      startedAt: receivedAt,
      lastChunkAt: receivedAt,
      hasChunk: false,
      reasoningChars: 0,
      samples: [],
      done: false,
    };
    return;
  }
  if (!["model.progress", "model.delta", "model.response"].includes(event?.type)) return;
  const key = modelTurnKey(event);
  let telemetry = session._streamTelemetry;
  if (!telemetry || telemetry.key !== key) {
    telemetry = {
      key,
      startedAt: receivedAt,
      lastChunkAt: receivedAt,
      hasChunk: false,
      reasoningChars: 0,
      samples: [],
      done: false,
    };
    session._streamTelemetry = telemetry;
  }
  telemetry.lastChunkAt = receivedAt;
  telemetry.hasChunk = true;
  if (event.type === "model.delta") {
    const chars = Array.from(String(data.text || "")).length;
    if (data.kind === "reasoning") telemetry.reasoningChars += chars;
    if (chars) telemetry.samples.push({ at: receivedAt, chars });
  } else if (event.type === "model.response") {
    telemetry.done = true;
    telemetry.reasoningTokens = Number(data.reasoning_tokens || 0);
    telemetry.timings = data.timings || {};
  }
}

export function hydrateTelemetry(session) {
  session._streamTelemetry = null;
  for (const event of session.timeline || []) {
    if (!["model.request", "model.progress", "model.delta", "model.response"].includes(event.type)) continue;
    applyStreamEvent(session, event, eventTime(event));
  }
}

export function liveTelemetry(session, now = Date.now()) {
  const telemetry = session?._streamTelemetry;
  if (!telemetry || telemetry.done) return null;
  const ageSeconds = Math.max(0, Math.floor((now - telemetry.lastChunkAt) / 1000));
  const reasoningTokens = Math.ceil(telemetry.reasoningChars / CHARS_PER_TOKEN);
  const recent = telemetry.samples.filter((sample) => sample.at >= now - RATE_WINDOW_MS);
  let rate = null;
  if (recent.length) {
    const chars = recent.reduce((sum, sample) => sum + sample.chars, 0);
    const start = Math.max(now - RATE_WINDOW_MS, telemetry.startedAt);
    const seconds = Math.max(0.25, (now - start) / 1000);
    rate = Math.round(chars / CHARS_PER_TOKEN / seconds);
  }
  return { ageSeconds, reasoningTokens, rate, hasChunk: telemetry.hasChunk };
}

export function recordedTelemetry(session) {
  const response = [...(session?.timeline || [])]
    .reverse()
    .find((event) => event.type === "model.response");
  if (!response) return null;
  const data = response.data || {};
  const rawRate = Number(data.timings?.predicted_per_second);
  return {
    reasoningTokens: Number(data.reasoning_tokens || 0),
    reasoningTokensEstimated: !!data.reasoning_tokens_estimated,
    rate: Number.isFinite(rawRate) && rawRate > 0 ? Math.round(rawRate) : null,
  };
}

function eventTime(event) {
  const parsed = Date.parse(event?.ts || "");
  return Number.isFinite(parsed) ? parsed : Date.now();
}

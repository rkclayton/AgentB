export const recommendationTableVersion = "2026-09-07";

export const recommendationTable = [
  { memory_class: "under 4 GiB", min_gib: 0, max_gib: 3, model: "qwen3.5:0.8b", package_size: "1.0 GB", use: "light assistant" },
  { memory_class: "4–5 GiB", min_gib: 4, max_gib: 5, model: "qwen3.5:2b", package_size: "2.7 GB", use: "small assistant" },
  { memory_class: "6–11 GiB", min_gib: 6, max_gib: 11, model: "qwen3.5:4b", package_size: "3.4 GB", use: "everyday assistant" },
  { memory_class: "12–19 GiB", min_gib: 12, max_gib: 19, model: "qwen3.5:9b", package_size: "6.6 GB", use: "strong assistant" },
  { memory_class: "20–31 GiB", min_gib: 20, max_gib: 31, model: "qwen3.5:27b", package_size: "17 GB", use: "local main model" },
  { memory_class: "32–47 GiB", min_gib: 32, max_gib: 47, model: "qwen3.5:27b", package_size: "17 GB", use: "local main with headroom" },
  { memory_class: "48–95 GiB", min_gib: 48, max_gib: 95, model: "qwen3.5:35b", package_size: "24 GB", use: "larger main with headroom" },
  { memory_class: "96 GiB or more", min_gib: 96, max_gib: null, model: "qwen3.5:122b", package_size: "81 GB", use: "high-memory local main" },
];

export function recommendationForBytes(bytes) {
  const gib = Math.floor(Number(bytes || 0) / (1024 ** 3));
  return recommendationTable.find((row) => gib >= row.min_gib && (row.max_gib === null || gib <= row.max_gib)) || recommendationTable[0];
}

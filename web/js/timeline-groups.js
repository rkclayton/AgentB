const rangePriority = ["offset", "path", "url", "pattern", "query", "command", "note", "limit"];

export function groupToolRuns(entries) {
  return groupAdjacentRuns(
    entries,
    (entry) => {
      const call = singleTool(entry);
      return call ? { key: call.name, id: call.id } : null;
    },
    (entry) => entry?.kind === "compaction",
  ).map((entry) => entry.kind !== "adjacent-group" ? entry : ({
    kind: "tool-group",
    tool: entry.groupKey,
    firstCallID: entry.members[0].id,
    items: entry.items,
    models: entry.members.map((member) => member.entry),
  }));
}

export function groupAdjacentRuns(entries, classify, isBridge = () => false) {
  const grouped = [];
  for (let index = 0; index < entries.length;) {
    const first = classify(entries[index]);
    if (!first) {
      grouped.push(entries[index++]);
      continue;
    }

    const items = [entries[index]], members = [{ ...first, entry: entries[index] }];
    let cursor = index + 1;
    for (;;) {
      const bridgeStart = cursor;
      while (isBridge(entries[cursor])) cursor++;
      const next = classify(entries[cursor]);
      if (!next || next.key !== first.key) {
        cursor = bridgeStart;
        break;
      }
      items.push(...entries.slice(bridgeStart, cursor), entries[cursor]);
      members.push({ ...next, entry: entries[cursor] });
      cursor++;
    }

    if (members.length < 2) {
      grouped.push(entries[index++]);
      continue;
    }
    grouped.push({
      kind: "adjacent-group",
      groupKey: first.key,
      items,
      members,
    });
    index = cursor;
  }
  return grouped;
}

export function toolGroupRange(group, calls) {
  const argumentsList = group.models.map((entry) => {
    const call = singleTool(entry);
    return calls.get(call.id)?.data?.args || safeJSON(call.arguments);
  });
  if (!argumentsList.every((args) => args && typeof args === "object" && !Array.isArray(args)))
    return null;

  const keys = [...new Set(argumentsList.flatMap((args) => Object.keys(args)))];
  keys.sort((left, right) => {
    const leftPriority = rangePriority.indexOf(left), rightPriority = rangePriority.indexOf(right);
    return (leftPriority < 0 ? rangePriority.length : leftPriority) -
      (rightPriority < 0 ? rangePriority.length : rightPriority) || left.localeCompare(right);
  });
  for (const key of keys) {
    const values = argumentsList.map((args) => args[key]);
    if (values.some((value) => !isScalar(value)) || new Set(values.map(stableValue)).size < 2)
      continue;
    if (values.every((value) => typeof value === "number" && Number.isFinite(value))) {
      return { key, first: Math.min(...values), last: Math.max(...values), numeric: true };
    }
    return { key, first: String(values[0]), last: String(values.at(-1)), numeric: false };
  }
  return null;
}

export function groupCallIDs(group) {
  return group.models.map((entry) => singleTool(entry).id);
}

export function toolGroupStatus(group, results) {
  const ids = groupCallIDs(group), resultList = ids.map((id) => results.get(id)?.data);
  const failed = resultList.filter((result) => result?.ok === false).length;
  const pending = resultList.filter((result) => !result).length;
  return {
    ids,
    failed,
    pending,
    untrusted: resultList.some((result) => result?.untrusted),
    duration: resultList.reduce((total, result) => total + Number(result?.ms || 0), 0),
  };
}

export function toolResultText(message, result, original = "") {
  return message?.elided
    ? original || result?.preview || message.content || ""
    : message?.content || result?.preview || "";
}

function singleTool(entry) {
  const calls = entry?.kind === "model" ? entry.event?.data?.tool_calls || [] : [];
  return calls.length === 1 && calls[0]?.id && calls[0]?.name ? calls[0] : null;
}

function safeJSON(value) {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

function isScalar(value) {
  return value === null || ["string", "number", "boolean"].includes(typeof value);
}

function stableValue(value) {
  return `${typeof value}:${String(value)}`;
}

const agentStates = ["idle", "waiting", "running", "offline"];

export async function pageStyleReach(page, { stylesheet, state }) {
  return page.evaluate(({ stylesheet, state }) => {
    const shell = document.querySelector(".app-shell");
    if (!shell) throw new Error("page-style boundary: .app-shell is missing");
    const robots = [...shell.querySelectorAll(".agent-tab-robot")];
    if (!robots.length) throw new Error("page-style boundary: no agent robot is rendered");
    for (const robot of robots) {
      robot.classList.remove("idle", "waiting", "running", "offline");
      robot.classList.add(state);
    }

    const sheet = [...document.styleSheets].find((candidate) => candidate.href && new URL(candidate.href).pathname.endsWith(`/css/${stylesheet}`));
    if (!sheet) throw new Error(`page-style boundary: ${stylesheet} is missing`);

    const styleRules = [];
    const walk = (rules, conditions = []) => {
      for (const rule of rules) {
        if (rule.selectorText) styleRules.push({ rule, conditions });
        if (rule.cssRules) walk(rule.cssRules, [...conditions, rule.conditionText || rule.cssText.split("{")[0].trim()]);
      }
    };
    walk(sheet.cssRules);

    const describe = (element) => `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ""}${[...element.classList].map((name) => `.${name}`).join("")}`;
    const elements = [shell, ...shell.querySelectorAll("*")];
    return styleRules.flatMap(({ rule, conditions }) => {
      const matched = elements.filter((element) => {
        try { return element.matches(rule.selectorText); }
        catch { return false; }
      });
      return matched.length ? [{ selector: rule.selectorText, conditions, elements: matched.map(describe) }] : [];
    });
  }, { stylesheet, state });
}

export async function assertPageStyleBoundary(page, stylesheet) {
  const evidence = {};
  for (const state of agentStates) {
    const reaches = await pageStyleReach(page, { stylesheet, state });
    evidence[state] = reaches;
    if (reaches.length) throw new Error(`page-style boundary: ${stylesheet} reaches .app-shell in ${state}: ${JSON.stringify(reaches)}`);
  }
  return evidence;
}

export async function provePageStyleBoundaryControl(page, stylesheet) {
  const ruleIndex = await page.evaluate((name) => {
    const sheet = [...document.styleSheets].find((candidate) => candidate.href && new URL(candidate.href).pathname.endsWith(`/css/${name}`));
    if (!sheet) throw new Error(`page-style boundary negative control: ${name} is missing`);
    return sheet.insertRule(".idle img { width: 96px; height: 96px; margin: auto; }", sheet.cssRules.length);
  }, stylesheet);
  try {
    const idle = await pageStyleReach(page, { stylesheet, state: "idle" });
    const waiting = await pageStyleReach(page, { stylesheet, state: "waiting" });
    if (!idle.some((entry) => entry.selector === ".idle img" && entry.elements.includes("img"))) {
      throw new Error(`page-style boundary negative control did not catch .idle img: ${JSON.stringify(idle)}`);
    }
    if (waiting.length) throw new Error(`page-style boundary negative control reached a non-idle state: ${JSON.stringify(waiting)}`);
    return { idle, waiting };
  } finally {
    await page.evaluate(({ name, index }) => {
      const sheet = [...document.styleSheets].find((candidate) => candidate.href && new URL(candidate.href).pathname.endsWith(`/css/${name}`));
      sheet?.deleteRule(index);
    }, { name: stylesheet, index: ruleIndex });
  }
}

export { agentStates };

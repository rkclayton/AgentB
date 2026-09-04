const assets = {
  off: {
    src: "/static/assets/operator-off-24.png",
    srcset: "/static/assets/operator-off-48.png 2x",
  },
  on: {
    src: "/static/assets/operator-on-24.png",
    srcset: "/static/assets/operator-on-48.png 2x",
  },
};

export function operatorStatusView(identity) {
  const active = !!identity?.operator_context;
  return {
    active,
    label: active ? "Operator mode active" : "Operator mode off",
    ...assets[active ? "on" : "off"],
  };
}

export function renderOperatorStatus(button, identity) {
  const view = operatorStatusView(identity);
  const image = button.querySelector("img");
  button.setAttribute("aria-label", view.label);
  button.setAttribute("aria-pressed", String(view.active));
  button.title = view.label;
  button.dataset.active = String(view.active);
  image.src = view.src;
  image.srcset = view.srcset;
}

export function isOperatorStateEvent(event) {
  return event.type === "snapshot" || event.type === "shell.identity" || event.type === "operator.context";
}

export function createOperatorStatusController(button, options) {
  let pending = false;
  let pendingTarget = false;

  function render() {
    const identity = options.identity();
    renderOperatorStatus(button, identity);
    const interactive = options.interactive ? options.interactive() : true;
    button.disabled = pending || !interactive;
    button.dataset.pending = String(pending);
    button.setAttribute("aria-busy", String(pending));
    if (pending) {
      const label = pendingTarget ? "Enabling operator mode" : "Disabling operator mode";
      button.setAttribute("aria-label", label);
      button.title = label;
    }
  }

  async function toggle() {
    if (pending || (options.interactive && !options.interactive())) return;
    const enable = !options.identity()?.operator_context;
    if (enable && !options.confirmEnable()) return;
    pending = true;
    pendingTarget = enable;
    render();
    try {
      await options.setOperatorContext(enable);
    } catch (error) {
      options.reportError(error instanceof Error ? error.message : String(error));
    } finally {
      pending = false;
      render();
    }
  }

  button.addEventListener("click", toggle);
  render();
  return { render, toggle };
}

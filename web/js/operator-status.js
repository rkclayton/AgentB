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

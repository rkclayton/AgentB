import { store } from "./bus.js";

const root = document.getElementById("todos");
const count = document.getElementById("todo-count");

export function renderTodos() {
  const session = store.sessions[store.active];
  root.replaceChildren();
  if (!session) {
    count.textContent = "";
    return;
  }
  const todos = session.todos || [];
  const done = todos.filter((todo) => todo.status === "done").length;
  count.textContent = todos.length ? `${done} / ${todos.length} done` : "";
  if (!todos.length) {
    const empty = document.createElement("div");
    empty.className = "panel-empty";
    empty.textContent = "—";
    root.append(empty);
    return;
  }
  todos.forEach((todo, index) => {
    const row = document.createElement("div");
    row.className = `todo-row ${todo.status.replaceAll(" ", "-")}`;
    row.innerHTML = '<span class="todo-index number"></span><span class="todo-lamp"></span><span class="todo-text"></span><span class="todo-status"></span>';
    row.children[0].textContent = index + 1;
    row.children[2].textContent = todo.text;
    row.children[3].textContent = todo.status;
    root.append(row);
  });
}

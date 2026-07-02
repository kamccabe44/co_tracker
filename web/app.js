"use strict";

// ---- state ----
const state = {
  view: startOfMonth(new Date()), // first day of displayed month
  units: [],
  users: [],
  entries: [], // entries for the visible grid range
};

const $ = (sel) => document.querySelector(sel);
const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// ---- api helpers ----
async function api(path, opts = {}) {
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `${res.status} ${res.statusText}`);
  return body;
}

const jsonReq = (method, body) => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

// ---- date utils ----
function startOfMonth(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
function ymd(d) {
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
function addDays(d, n) { const r = new Date(d); r.setDate(r.getDate() + n); return r; }

// The calendar shows full weeks: from the Sunday on/before the 1st
// through the Saturday on/after the last day of the month.
function gridRange(view) {
  const first = startOfMonth(view);
  const start = addDays(first, -first.getDay());
  const last = new Date(view.getFullYear(), view.getMonth() + 1, 0);
  const end = addDays(last, 6 - last.getDay());
  return { start, end };
}

// ---- data loading ----
async function refreshAll() {
  [state.units, state.users] = await Promise.all([api("/api/units"), api("/api/users")]);
  await refreshEntries();
  renderSidebar();
  renderCalendar();
}

async function refreshEntries() {
  const { start, end } = gridRange(state.view);
  state.entries = await api(`/api/entries?from=${ymd(start)}&to=${ymd(end)}`);
}

// ---- calendar rendering ----
function renderCalendar() {
  $("#month-label").textContent = state.view.toLocaleDateString(undefined, {
    month: "long", year: "numeric",
  });

  const wk = $("#weekday-row");
  wk.replaceChildren(...WEEKDAYS.map((w) => {
    const d = document.createElement("div");
    d.textContent = w;
    return d;
  }));

  const byDate = new Map();
  for (const e of state.entries) {
    if (!byDate.has(e.date)) byDate.set(e.date, []);
    byDate.get(e.date).push(e);
  }

  const { start, end } = gridRange(state.view);
  const todayStr = ymd(new Date());
  const cells = [];
  for (let d = new Date(start); d <= end; d = addDays(d, 1)) {
    const dateStr = ymd(d);
    const cell = document.createElement("div");
    cell.className = "day";
    if (d.getMonth() !== state.view.getMonth()) cell.classList.add("other-month");
    if (dateStr === todayStr) cell.classList.add("today");
    cell.dataset.date = dateStr;

    const num = document.createElement("span");
    num.className = "day-num";
    num.textContent = d.getDate();
    cell.appendChild(num);

    for (const entry of byDate.get(dateStr) || []) {
      cell.appendChild(entryPill(entry));
    }

    cell.addEventListener("click", () => openEntryDialog({ date: dateStr }));
    cells.push(cell);
  }
  $("#calendar").replaceChildren(...cells);
}

function entryPill(entry) {
  const pill = document.createElement("button");
  pill.type = "button";
  pill.className = "entry-pill";
  pill.style.background = entry.unitColor;
  pill.textContent = entry.unitName + (entry.userNames.length ? ` (${entry.userNames.length})` : "");
  pill.addEventListener("mouseenter", (ev) => showTooltip(ev, entry));
  pill.addEventListener("mousemove", moveTooltip);
  pill.addEventListener("mouseleave", hideTooltip);
  pill.addEventListener("click", (ev) => {
    ev.stopPropagation();
    hideTooltip();
    openEntryDialog(entry);
  });
  return pill;
}

// ---- tooltip (names on hover) ----
function showTooltip(ev, entry) {
  const tt = $("#tooltip");
  tt.replaceChildren();

  const title = document.createElement("div");
  title.className = "tt-title";
  title.textContent = `${entry.unitName} — ${entry.date}`;
  tt.appendChild(title);

  if (entry.userNames.length) {
    const ul = document.createElement("ul");
    for (const name of entry.userNames) {
      const li = document.createElement("li");
      li.textContent = name;
      ul.appendChild(li);
    }
    tt.appendChild(ul);
  } else {
    const p = document.createElement("div");
    p.className = "tt-empty";
    p.textContent = "No one assigned";
    tt.appendChild(p);
  }

  if (entry.notes) {
    const n = document.createElement("div");
    n.className = "tt-notes";
    n.textContent = entry.notes;
    tt.appendChild(n);
  }

  tt.classList.remove("hidden");
  moveTooltip(ev);
}

function moveTooltip(ev) {
  const tt = $("#tooltip");
  const pad = 12;
  let x = ev.clientX + pad;
  let y = ev.clientY + pad;
  const rect = tt.getBoundingClientRect();
  if (x + rect.width > window.innerWidth - pad) x = ev.clientX - rect.width - pad;
  if (y + rect.height > window.innerHeight - pad) y = ev.clientY - rect.height - pad;
  tt.style.left = `${x}px`;
  tt.style.top = `${y}px`;
}

function hideTooltip() {
  $("#tooltip").classList.add("hidden");
}

// ---- sidebar rendering ----
function renderSidebar() {
  const unitItems = state.units.map((u) => {
    const li = document.createElement("li");

    const color = document.createElement("input");
    color.type = "color";
    color.value = u.color;
    color.title = "Change color";
    color.addEventListener("change", () =>
      run(() => api(`/api/units/${u.id}`, jsonReq("PUT", { name: u.name, color: color.value }))));

    const name = document.createElement("span");
    name.className = "unit-name";
    name.textContent = u.name;
    name.title = "Double-click to rename";
    name.addEventListener("dblclick", () => {
      const next = prompt("Rename unit", u.name);
      if (next && next.trim() && next !== u.name) {
        run(() => api(`/api/units/${u.id}`, jsonReq("PUT", { name: next.trim(), color: u.color })));
      }
    });

    li.append(color, name, deleteBtn(`Delete unit "${u.name}" and all its schedule entries?`, `/api/units/${u.id}`));
    return li;
  });
  $("#unit-list").replaceChildren(...unitItems);

  const userItems = state.users.map((u) => {
    const li = document.createElement("li");
    const name = document.createElement("span");
    name.className = "user-name";
    name.textContent = u.name;
    if (u.email) name.title = u.email;
    li.append(name, deleteBtn(`Delete "${u.name}"?`, `/api/users/${u.id}`));
    return li;
  });
  $("#user-list").replaceChildren(...userItems);
}

function deleteBtn(confirmMsg, path) {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "icon-btn";
  btn.textContent = "×";
  btn.title = "Delete";
  btn.addEventListener("click", () => {
    if (confirm(confirmMsg)) run(() => api(path, { method: "DELETE" }));
  });
  return btn;
}

// ---- entry dialog ----
function openEntryDialog(entry) {
  if (!state.units.length) {
    alert("Create a business unit first.");
    return;
  }
  const dlg = $("#entry-dialog");
  dlg.dataset.entryId = entry.id || "";
  $("#entry-dialog-title").textContent = entry.id ? "Edit entry" : "Add entry";
  $("#entry-date").value = entry.date;
  $("#entry-notes").value = entry.notes || "";
  $("#entry-delete").classList.toggle("hidden", !entry.id);

  const sel = $("#entry-unit");
  sel.replaceChildren(...state.units.map((u) => {
    const opt = document.createElement("option");
    opt.value = u.id;
    opt.textContent = u.name;
    return opt;
  }));
  if (entry.unitId) sel.value = entry.unitId;

  const checked = new Set(entry.userIds || []);
  const box = $("#entry-users");
  if (state.users.length) {
    box.replaceChildren(...state.users.map((u) => {
      const label = document.createElement("label");
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.value = u.id;
      cb.checked = checked.has(u.id);
      label.append(cb, document.createTextNode(u.name));
      return label;
    }));
  } else {
    const p = document.createElement("p");
    p.className = "no-users";
    p.textContent = "No people yet — add them in the sidebar or import a CSV.";
    box.replaceChildren(p);
  }

  dlg.showModal();
}

// ---- generic action runner: do the call, then re-sync UI ----
async function run(action) {
  try {
    await action();
    await refreshAll();
  } catch (err) {
    alert(err.message);
  }
}

// ---- wire up events ----
function init() {
  $("#prev-month").addEventListener("click", () => shiftMonth(-1));
  $("#next-month").addEventListener("click", () => shiftMonth(1));
  $("#today-btn").addEventListener("click", () => {
    state.view = startOfMonth(new Date());
    run(async () => {});
  });

  $("#unit-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    const name = $("#unit-name").value.trim();
    if (!name) return;
    run(() => api("/api/units", jsonReq("POST", { name, color: $("#unit-color").value })));
    $("#unit-name").value = "";
  });

  $("#user-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    const name = $("#user-name").value.trim();
    if (!name) return;
    run(() => api("/api/users", jsonReq("POST", { name })));
    $("#user-name").value = "";
  });

  $("#import-users").addEventListener("change", (ev) => importCSV(ev.target, "/api/import/users"));
  $("#import-entries").addEventListener("change", (ev) => importCSV(ev.target, "/api/import/entries"));

  $("#entry-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    const dlg = $("#entry-dialog");
    const id = dlg.dataset.entryId;
    const body = {
      date: $("#entry-date").value,
      unitId: Number($("#entry-unit").value),
      notes: $("#entry-notes").value.trim(),
      userIds: [...$("#entry-users").querySelectorAll("input:checked")].map((cb) => Number(cb.value)),
    };
    dlg.close();
    run(() => id
      ? api(`/api/entries/${id}`, jsonReq("PUT", body))
      : api("/api/entries", jsonReq("POST", body)));
  });

  $("#entry-cancel").addEventListener("click", () => $("#entry-dialog").close());
  $("#entry-delete").addEventListener("click", () => {
    const dlg = $("#entry-dialog");
    const id = dlg.dataset.entryId;
    if (id && confirm("Delete this entry?")) {
      dlg.close();
      run(() => api(`/api/entries/${id}`, { method: "DELETE" }));
    }
  });

  refreshAll().catch((err) => alert(`Failed to load: ${err.message}`));
}

function shiftMonth(delta) {
  state.view = new Date(state.view.getFullYear(), state.view.getMonth() + delta, 1);
  run(async () => {});
}

async function importCSV(input, endpoint) {
  const file = input.files[0];
  if (!file) return;
  input.value = "";
  const status = $("#import-status");
  status.textContent = "Importing…";
  try {
    const form = new FormData();
    form.append("file", file);
    const result = await api(endpoint, { method: "POST", body: form });
    const parts = [];
    if ("created" in result) parts.push(`${result.created} created`);
    if (result.updated) parts.push(`${result.updated} updated`);
    let msg = `Done: ${parts.join(", ") || "no rows"}`;
    if (result.errors && result.errors.length) {
      msg += `\n${result.errors.length} error(s):\n` + result.errors.slice(0, 5).join("\n");
    }
    status.textContent = msg;
    await refreshAll();
  } catch (err) {
    status.textContent = `Import failed: ${err.message}`;
  }
}

document.addEventListener("DOMContentLoaded", init);

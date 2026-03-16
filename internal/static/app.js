"use strict";

const STORAGE_KEY = "profile";

// --- Initialization ---

document.addEventListener("DOMContentLoaded", () => {
  const form = document.getElementById(IDs.profileForm);
  if (form !== null) {
    initForm(form);
  }
});

// Re-initialize forms and UI after HTMX swaps new content in.
document.addEventListener("htmx:oobAfterSwap", () => {
  const form = document.getElementById(IDs.profileForm);
  if (form !== null && !form.dataset.initialized) {
    initForm(form);
  }

  const messages = document.getElementById(IDs.messages);
  if (messages !== null) {
    messages.scrollTop = messages.scrollHeight;
  }
});

// --- Protocol message interception ---

// Intercept incoming WS messages. JSON protocol messages (token,
// rate_limited) are handled here and cancelled, so HTMX doesn't
// try to swap them as HTML. HTML fragments pass through to HTMX.
document.addEventListener("htmx:wsBeforeMessage", (event) => {
  let env;
  try {
    env = JSON.parse(event.detail.message);
  } catch {
    return;
  }

  // cancel so HTMX doesn't try to swap JSON as HTML
  event.preventDefault();

  switch (env.type) {
    case "token":
      sessionStorage.setItem("session_token", env.payload.token);
      sessionStorage.setItem("refresh_token", env.payload.refresh);
      break;

    case "rate_limited":
      console.warn(
        "rate limited, retry after",
        env.payload.retry_after,
        "s",
      );
      break;
  }
});

// --- Outgoing message formatting ---

// Intercept outgoing ws-send messages. The form data includes a
// "type" hidden field. For find_match and block, merge in the
// profile from the drawer form or sessionStorage.
document.addEventListener("htmx:wsConfigSend", (event) => {
  const params = event.detail.parameters;
  const msgType = params.type;

  if (msgType === "find_match") {
    const profile = getProfile();
    Object.assign(params, profile);
  }

  // confirm before leaving
  if (msgType === "leave") {
    if (!confirm("Are you sure you want to leave?")) {
      event.preventDefault();
      return;
    }
  }

  // block empty chat messages
  if (msgType === "message") {
    const text = (params.text || "").trim();
    if (text === "") {
      event.preventDefault();
      return;
    }
  }

  // wrap as our JSON envelope format
  const payload = Object.assign({}, params);
  delete payload.type;
  event.detail.messageBody = JSON.stringify({
    type: msgType,
    payload: payload,
  });
});

// Clear chat input after a message is sent and refocus.
document.addEventListener("htmx:wsAfterSend", (event) => {
  const form = event.detail.elt;
  if (form.id !== IDs.chatForm) return;
  const input = document.getElementById(IDs.messageInput);
  if (input !== null) {
    input.value = "";
    input.style.height = "auto";
    input.focus();
  }
});

// --- Helpers ---

// getProfile reads the user's profile from the drawer form,
// falling back to sessionStorage.
function getProfile() {
  const form = document.getElementById(IDs.profileForm);
  if (form !== null) {
    const data = readForm(form);
    if (data.gender !== "") {
      return data;
    }
  }
  return readFromStorage();
}

function readFromStorage() {
  const raw = sessionStorage.getItem(STORAGE_KEY);
  if (raw === null) return {};
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// --- Interest picker ---

function initForm(form) {
  form.dataset.initialized = "true";
  initInterestPickers(form);
  restoreForm(form);

  form.addEventListener("change", () => {
    saveForm(form);
    updateFindButton(form);
  });

  updateFindButton(form);
}

function initInterestPickers(form) {
  form.querySelectorAll(".interest-picker").forEach(initPicker);
}

function initPicker(picker) {
  const search = picker.querySelector(".interest-search");
  const groups = picker.querySelectorAll(".interest-group");

  search.addEventListener("input", () => {
    const query = search.value.toLowerCase().trim();
    groups.forEach((group) => {
      let anyVisible = false;
      group.querySelectorAll(".interest-item").forEach((item) => {
        const matches =
          query === "" || item.dataset.value.includes(query);
        item.style.display = matches ? "" : "none";
        if (matches) anyVisible = true;
      });
      group.style.display = anyVisible ? "" : "none";
    });
  });

  picker.querySelectorAll(".interest-group-toggle").forEach((btn) => {
    btn.addEventListener("click", () => {
      const group = btn.closest(".interest-group");
      const items = group.querySelector(".interest-group-items");
      const arrow = btn.querySelector(".interest-group-arrow");
      const collapsed = items.style.display === "none";
      items.style.display = collapsed ? "" : "none";
      arrow.textContent = collapsed ? "\u25BC" : "\u25B6";
    });
  });

  picker.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener("change", () => syncTags(picker));
  });
}

function syncTags(picker) {
  const container = picker.querySelector(".interest-tags");

  while (container.firstChild) {
    container.removeChild(container.firstChild);
  }

  const checked = picker.querySelectorAll(
    'input[type="checkbox"]:checked',
  );

  if (checked.length === 0) {
    container.classList.add("hidden");
    container.classList.remove("flex");
  } else {
    container.classList.remove("hidden");
    container.classList.add("flex");
  }

  checked.forEach((cb) => {
    const tag = document.createElement("span");
    tag.className =
      "inline-flex items-center gap-0.5 rounded-full bg-indigo-100 " +
      "dark:bg-indigo-900 px-2 py-0.5 text-xs font-medium " +
      "text-indigo-700 dark:text-indigo-300";
    tag.textContent = cb.value;

    const remove = document.createElement("button");
    remove.type = "button";
    remove.className =
      "ml-0.5 text-indigo-500 hover:text-indigo-700 " +
      "dark:hover:text-indigo-100 text-sm leading-none";
    remove.textContent = "\u00D7";
    remove.addEventListener("click", () => {
      cb.checked = false;
      cb.dispatchEvent(new Event("change", { bubbles: true }));
    });

    tag.appendChild(remove);
    container.appendChild(tag);
  });
}

// --- Form persistence (sessionStorage) ---

function readForm(form) {
  const data = new FormData(form);
  return {
    gender: data.get("gender") || "",
    role: data.get("role") || "",
    interests: data.getAll("interests"),
    filter_gender: data.getAll("filter_gender"),
    filter_role: data.getAll("filter_role"),
    exclude_interests: data.getAll("exclude_interests"),
  };
}

function saveForm(form) {
  const data = readForm(form);
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(data));
}

function restoreForm(form) {
  const raw = sessionStorage.getItem(STORAGE_KEY);
  if (raw === null) return;

  let data;
  try {
    data = JSON.parse(raw);
  } catch {
    return;
  }

  setSelect(form, "gender", data.gender);
  setSelect(form, "role", data.role);
  setCheckboxes(form, "interests", data.interests || []);
  setCheckboxes(form, "filter_gender", data.filter_gender || []);
  setCheckboxes(form, "filter_role", data.filter_role || []);
  setCheckboxes(form, "exclude_interests", data.exclude_interests || []);

  form.querySelectorAll(".interest-picker").forEach(syncTags);
}

function setSelect(form, name, value) {
  if (value === undefined) return;
  const el = form.querySelector(`select[name="${name}"]`);
  if (el !== null) el.value = value;
}

function setCheckboxes(form, name, values) {
  const set = new Set(values);
  form
    .querySelectorAll(`input[type="checkbox"][name="${name}"]`)
    .forEach((input) => {
      input.checked = set.has(input.value);
    });
}

function updateFindButton(form) {
  const data = new FormData(form);
  const btn = document.getElementById(IDs.findMatchBtn);
  if (btn === null) return;
  btn.disabled = !data.get("gender") || !data.get("role");
}

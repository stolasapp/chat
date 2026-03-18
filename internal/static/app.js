"use strict";

const STORAGE_KEY = "profile";
const CHAR_COUNT_THRESHOLD = 200;

// message sequence counter for optimistic send confirmation
let messageSeq = 0;

// WebSocket reference for direct sends (typing indicators)
let ws = null;

// typing indicator debounce state
let typingTimeout = null;
let isTyping = false;


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

  const input = document.getElementById(IDs.messageInput);
  if (input !== null && !input.dataset.typingBound) {
    input.dataset.typingBound = "true";
    input.addEventListener("input", onMessageInput);
  }
});

// --- WebSocket lifecycle ---

document.addEventListener("htmx:wsOpen", (event) => {
  ws = event.detail.event.target;
});

document.addEventListener("htmx:wsClose", () => {
  ws = null;
  isTyping = false;
  clearTimeout(typingTimeout);

  const container = document.getElementById(IDs.wsContainer);
  if (container === null) return;
  const token = sessionStorage.getItem("session_token");
  if (token !== null) {
    container.setAttribute(
      "ws-connect",
      "/ws?token=" + encodeURIComponent(token),
    );
  } else {
    container.setAttribute("ws-connect", "/ws");
  }
});

// --- Typing indicator ---

function onMessageInput() {
  if (ws === null) return;

  if (!isTyping) {
    isTyping = true;
    ws.send(JSON.stringify({ type: "typing", payload: { active: true } }));
  }

  clearTimeout(typingTimeout);
  typingTimeout = setTimeout(() => {
    isTyping = false;
    if (ws !== null) {
      ws.send(
        JSON.stringify({ type: "typing", payload: { active: false } }),
      );
    }
  }, 2000);
}

function stopTyping() {
  if (!isTyping || ws === null) return;
  isTyping = false;
  clearTimeout(typingTimeout);
  ws.send(JSON.stringify({ type: "typing", payload: { active: false } }));
}

// --- Protocol message interception ---

// Intercept incoming WS messages. JSON protocol messages (token,
// rate_limited) are handled here and cancelled, so HTMX doesn't
// try to swap them as HTML. HTML with data-seq confirms optimistic
// messages. Other HTML fragments pass through to HTMX.
document.addEventListener("htmx:wsBeforeMessage", (event) => {
  const raw = event.detail.message;

  let env;
  try {
    env = JSON.parse(raw);
  } catch {
    return;
  }

  // cancel so HTMX doesn't try to swap JSON as HTML
  event.preventDefault();

  switch (env.type) {
    case "token":
      sessionStorage.setItem("session_token", env.payload.token);
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

  // optimistic message send
  if (msgType === "message") {
    const sendBtn = document.getElementById(IDs.sendBtn);
    if (sendBtn !== null && sendBtn.disabled) {
      event.preventDefault();
      return;
    }
    const text = (params.text || "").trim();
    if (text === "" || text.length > IDs.maxMessageLength) {
      event.preventDefault();
      return;
    }
    messageSeq++;
    const seq = messageSeq;

    // render optimistic preview immediately
    stopTyping();
    appendOptimisticMessage(text, seq);

    // send with seq for server echo confirmation
    event.detail.messageBody = JSON.stringify({
      type: "message",
      payload: { text: text, seq: seq },
    });
    return;
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
    updateCharCount(input);
  }
});

// --- Character count ---

function updateCharCount(textarea) {
  const counter = document.getElementById(IDs.charCount);
  if (counter === null) return;
  const remaining = IDs.maxMessageLength - textarea.value.length;
  if (remaining <= CHAR_COUNT_THRESHOLD) {
    counter.textContent = remaining;
    counter.classList.remove("hidden");
    if (remaining <= 50) {
      counter.classList.add("text-red-500", "dark:text-red-400");
      counter.classList.remove("text-gray-400", "dark:text-gray-500");
    } else {
      counter.classList.remove("text-red-500", "dark:text-red-400");
      counter.classList.add("text-gray-400", "dark:text-gray-500");
    }
  } else {
    counter.classList.add("hidden");
  }
}

// --- Optimistic messages ---

function appendOptimisticMessage(text, seq) {
  // build a safe HTML string using a temporary element for
  // textContent escaping, then let htmx handle the DOM insert.
  // NOTE: base classes must match message.templ ChatMessage self
  // branch (plus opacity-50 for pending state).
  const tmp = document.createElement("span");
  tmp.textContent = text;
  const html =
    '<div id="msg-' +
    seq +
    '" class="ml-auto max-w-[75%] break-words hyphens-auto rounded-lg ' +
    'bg-indigo-600 px-3 py-2 text-sm text-white opacity-50">' +
    tmp.innerHTML +
    "</div>";
  htmx.swap("#" + IDs.messages, html, { swapStyle: "beforeend" });
  const msgs = document.getElementById(IDs.messages);
  if (msgs !== null) {
    msgs.scrollTop = msgs.scrollHeight;
  }
}


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

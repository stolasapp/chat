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

  processNotifications();
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
      counter.classList.remove("text-stone-400", "dark:text-stone-500");
    } else {
      counter.classList.remove("text-red-500", "dark:text-red-400");
      counter.classList.add("text-stone-400", "dark:text-stone-500");
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
    'bg-amber-700 px-3 py-2 text-sm leading-relaxed text-white opacity-50">' +
    tmp.innerHTML +
    "</div>";
  htmx.swap("#" + IDs.messages, html, { swapStyle: "beforeend" });
  const msgs = document.getElementById(IDs.messages);
  if (msgs !== null) {
    msgs.scrollTop = msgs.scrollHeight;
  }
}

// --- Leave dialog ---

function openLeaveDialog() {
  const dialog = document.getElementById("leave-dialog");
  dialog.showModal();
  const cancel = document.getElementById("leave-cancel");
  if (cancel !== null) cancel.focus();
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

// --- Searchable picker ---

function initForm(form) {
  form.dataset.initialized = "true";
  initSearchablePickers(form);
  restoreForm(form);

  form.addEventListener("change", () => {
    saveForm(form);
    updateFindButton(form);
  });

  updateFindButton(form);
}

function initSearchablePickers(form) {
  form.querySelectorAll(".searchable-picker").forEach(initPicker);
}

function initPicker(picker) {
  const search = picker.querySelector(".picker-search");
  const items = picker.querySelectorAll(".picker-item");

  search.addEventListener("input", () => {
    const query = search.value.toLowerCase().trim();
    items.forEach((item) => {
      const matches = query === "" || item.dataset.value.includes(query);
      item.style.display = matches ? "" : "none";
    });
  });

  picker
    .querySelectorAll('input[type="checkbox"], input[type="radio"]')
    .forEach((input) => {
      input.addEventListener("change", () => syncTags(picker));
    });
}

function syncTags(picker) {
  const container = picker.querySelector(".picker-tags");

  while (container.firstChild) {
    container.removeChild(container.firstChild);
  }

  const checked = picker.querySelectorAll(
    'input[type="checkbox"]:checked, input[type="radio"]:checked',
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
      "inline-flex items-center gap-0.5 rounded-full bg-amber-100 " +
      "dark:bg-amber-900 px-2 py-0.5 text-xs font-medium " +
      "text-amber-700 dark:text-amber-300";
    const parentLabel = cb.closest("label");
    tag.textContent =
      parentLabel !== null ? parentLabel.textContent.trim() : cb.value;

    if (cb.type === "checkbox") {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className =
        "ml-0.5 text-amber-500 hover:text-amber-700 " +
        "dark:hover:text-amber-100 text-sm leading-none";
      remove.textContent = "\u00D7";
      remove.addEventListener("click", () => {
        cb.checked = false;
        cb.dispatchEvent(new Event("change", { bubbles: true }));
      });
      tag.appendChild(remove);
    }

    container.appendChild(tag);
  });
}

// --- Form persistence (sessionStorage) ---

function readForm(form) {
  const data = new FormData(form);
  return {
    gender: data.get("gender") || "",
    role: data.get("role") || "",
    species: data.get("species") || "",
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
  if (raw !== null) {
    let data;
    try {
      data = JSON.parse(raw);
    } catch {
      data = {};
    }

    setSelect(form, "gender", data.gender);
    setSelect(form, "role", data.role);
    setRadio(form, "species", data.species || "");
    setCheckboxes(form, "interests", data.interests || []);
    setCheckboxes(form, "filter_gender", data.filter_gender || []);
    setCheckboxes(form, "filter_role", data.filter_role || []);
    setCheckboxes(form, "exclude_interests", data.exclude_interests || []);
  }

  // default species to "other" if nothing selected
  const speciesSelected = form.querySelector(
    'input[type="radio"][name="species"]:checked',
  );
  if (speciesSelected === null) {
    setRadio(form, "species", "other");
  }

  form.querySelectorAll(".searchable-picker").forEach(syncTags);
}

function setSelect(form, name, value) {
  if (value === undefined) return;
  const el = form.querySelector(`select[name="${name}"]`);
  if (el !== null) el.value = value;
}

function setRadio(form, name, value) {
  if (value === "") return;
  const el = form.querySelector(
    `input[type="radio"][name="${name}"][value="${value}"]`,
  );
  if (el !== null) el.checked = true;
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

// --- Notifications ---

const NOTIFY_KEY = "notifications_enabled";

function isNotifyEnabled() {
  const toggle = document.getElementById("notify-enabled");
  if (toggle !== null) return toggle.checked;
  return localStorage.getItem(NOTIFY_KEY) !== "false";
}

const notifyMessages = {
  match: "You've been matched",
  message: "New message received",
  ended: "Your chat has ended",
};

// audioCtx is created lazily on first notification to comply
// with browser autoplay policies (requires prior user gesture).
let audioCtx = null;

function playNotificationSound() {
  if (audioCtx === null) {
    audioCtx = new (window.AudioContext || window.webkitAudioContext)();
  }
  const now = audioCtx.currentTime;
  const oscillator = audioCtx.createOscillator();
  const gain = audioCtx.createGain();
  oscillator.connect(gain);
  gain.connect(audioCtx.destination);

  oscillator.type = "sine";
  oscillator.frequency.setValueAtTime(784, now); // G5
  oscillator.frequency.setValueAtTime(988, now + 0.08); // B5

  gain.gain.setValueAtTime(0.08, now);
  gain.gain.exponentialRampToValueAtTime(0.001, now + 0.25);

  oscillator.start(now);
  oscillator.stop(now + 0.25);
}

function showBrowserNotification(body) {
  if (!("Notification" in window)) return;

  if (Notification.permission === "default") {
    Notification.requestPermission();
    return;
  }
  if (Notification.permission !== "granted") return;

  new Notification("Stolas Chat", {
    body: body,
    icon: "/static/fox.svg",
  });
}

function processNotifications() {
  const elements = document.querySelectorAll("[data-notify]");
  elements.forEach((element) => {
    const kind = element.dataset.notify;
    element.removeAttribute("data-notify");

    if (!document.hidden || !isNotifyEnabled()) return;

    const body = notifyMessages[kind];
    if (body === undefined) return;

    playNotificationSound();
    showBrowserNotification(body);
  });
}

// Persist notification toggle to localStorage.
document.addEventListener("change", (event) => {
  if (event.target.id !== "notify-enabled") return;
  localStorage.setItem(NOTIFY_KEY, event.target.checked);
});

// Restore notification toggle from localStorage on form init.
document.addEventListener("htmx:oobAfterSwap", () => {
  const toggle = document.getElementById("notify-enabled");
  if (toggle !== null && !toggle.dataset.restored) {
    toggle.dataset.restored = "true";
    const stored = localStorage.getItem(NOTIFY_KEY);
    if (stored !== null) {
      toggle.checked = stored !== "false";
    }
  }
});

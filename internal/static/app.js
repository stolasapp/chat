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
      counter.classList.remove("text-muted-foreground");
    } else {
      counter.classList.remove("text-red-500", "dark:text-red-400");
      counter.classList.add("text-muted-foreground");
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
  window.tui.dialog.open("leave-dialog");
}

// --- SelectBox pill expansion ---

// templUI's selectbox JS collapses pills to "{n} items selected"
// when they overflow. Re-expand them in a double-rAF so our
// override runs after the library's collapse logic.
function scheduleExpandPills(event) {
  const item = event.target.closest(".select-item");
  if (item === null) return;
  const container = item.closest(".select-container");
  if (container === null) return;
  const trigger = container.querySelector(
    "[data-tui-selectbox-multiple=true]",
  );
  if (trigger === null) return;
  // Double rAF to run after the library's single rAF collapse.
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      expandPills(trigger);
    });
  });
}
document.addEventListener("click", scheduleExpandPills);
document.addEventListener("keydown", (event) => {
  if (event.key === "Enter") scheduleExpandPills(event);
});

function expandPills(trigger) {
  const valueEl = trigger.querySelector(".select-value");
  if (valueEl === null) return;
  // If pills are already showing, nothing to do.
  if (valueEl.querySelector("div")) return;

  const container = trigger.closest(".select-container");
  if (container === null) return;
  const content = container.querySelector("[data-tui-selectbox-content]");
  if (content === null) return;
  const selectedItems = content.querySelectorAll(
    '[data-tui-selectbox-selected="true"]',
  );
  if (selectedItems.length === 0) return;

  while (valueEl.firstChild) valueEl.removeChild(valueEl.firstChild);
  const pillsDiv = document.createElement("div");
  pillsDiv.className = "flex flex-wrap gap-1 items-center min-h-[1.5rem]";

  selectedItems.forEach((item) => {
    const pill = document.createElement("span");
    pill.className =
      "inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded-md bg-primary text-primary-foreground";
    const text = document.createElement("span");
    text.textContent =
      item.querySelector(".select-item-text")?.textContent || "";
    pill.appendChild(text);

    const removeBtn = document.createElement("button");
    removeBtn.className = "ml-0.5 hover:text-destructive focus:outline-none";
    removeBtn.type = "button";
    removeBtn.textContent = "\u00D7";
    removeBtn.setAttribute("data-tui-selectbox-pill-remove", "");
    removeBtn.setAttribute(
      "data-tui-selectbox-value",
      item.getAttribute("data-tui-selectbox-value"),
    );
    pill.appendChild(removeBtn);

    pillsDiv.appendChild(pill);
  });

  valueEl.appendChild(pillsDiv);
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

// --- Form initialization ---

function initForm(form) {
  form.dataset.initialized = "true";
  restoreForm(form);

  form.addEventListener("change", () => {
    saveForm(form);
    updateFindButton(form);
  });

  updateFindButton(form);
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
  if (raw === null) return;

  let data;
  try {
    data = JSON.parse(raw);
  } catch {
    return;
  }

  setSelectBoxValue(form, "gender", data.gender);
  setSelectBoxValue(form, "role", data.role);
  setSelectBoxValue(form, "species", data.species || "");
  setSelectBoxValues(form, "interests", data.interests || []);
  setSelectBoxValues(form, "filter_gender", data.filter_gender || []);
  setSelectBoxValues(form, "filter_role", data.filter_role || []);
  setSelectBoxValues(form, "exclude_interests", data.exclude_interests || []);
}

// setSelectBoxValue sets a single-select SelectBox's hidden input
// value and triggers its change event so the component updates.
function setSelectBoxValue(form, name, value) {
  if (!value) return;
  const input = form.querySelector('input[type="hidden"][name="' + name + '"]');
  if (input === null) return;
  input.value = value;
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

// setSelectBoxValues sets a multi-select SelectBox's hidden inputs.
// SelectBox creates one hidden input per selected value.
function setSelectBoxValues(form, name, values) {
  if (!values || values.length === 0) return;
  // Find the selectbox container by looking for the trigger with
  // the matching name attribute.
  const trigger = form.querySelector('[data-name="' + name + '"]');
  if (trigger === null) {
    // Fall back to setting hidden inputs directly
    values.forEach((value) => {
      const input = form.querySelector(
        'input[type="hidden"][name="' + name + '"][value="' + value + '"]',
      );
      if (input !== null) {
        input.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });
    return;
  }
  // For multi-select, we need to click the matching items
  const selectboxEl = trigger.closest("[data-selectbox]");
  if (selectboxEl === null) return;
  values.forEach((value) => {
    const item = selectboxEl.querySelector('[data-value="' + value + '"]');
    if (item !== null) {
      item.click();
    }
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

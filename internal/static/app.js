"use strict";

const STORAGE_KEY = "demographics";

document.addEventListener("DOMContentLoaded", () => {
  const form = document.getElementById("demographics-form");
  if (form === null) return;

  initInterestPickers(form);
  restoreForm(form);

  form.addEventListener("change", () => {
    saveForm(form);
    updateFindButton(form);
  });

  const btn = document.getElementById("find-match-btn");
  if (btn !== null) {
    btn.addEventListener("click", () => {
      const data = readForm(form);
      console.log("find_match", data);
    });
  }

  updateFindButton(form);
});

// --- Interest picker ---

function initInterestPickers(form) {
  form.querySelectorAll(".interest-picker").forEach(initPicker);
}

function initPicker(picker) {
  const search = picker.querySelector(".interest-search");
  const groups = picker.querySelectorAll(".interest-group");

  // Search filtering
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

  // Category collapse/expand
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

  // Checkbox changes update tags
  picker.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener("change", () => syncTags(picker));
  });
}

function syncTags(picker) {
  const container = picker.querySelector(".interest-tags");

  // Remove existing tags
  while (container.firstChild) {
    container.removeChild(container.firstChild);
  }

  const checked = picker.querySelectorAll(
    'input[type="checkbox"]:checked',
  );

  // Show/hide tags container
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

// --- Form persistence ---

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
  localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
}

function restoreForm(form) {
  const raw = localStorage.getItem(STORAGE_KEY);
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

  // Sync tags after restoring checkbox state
  form.querySelectorAll(".interest-picker").forEach(syncTags);
}

function setSelect(form, name, value) {
  if (value === undefined) return;
  const select = form.querySelector(`select[name="${name}"]`);
  if (select !== null) select.value = value;
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
  const btn = document.getElementById("find-match-btn");
  if (btn === null) return;
  btn.disabled = !data.get("gender") || !data.get("role");
}

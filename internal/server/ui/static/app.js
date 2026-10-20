// Progressive enhancements. Every page works without JavaScript.
(() => {
  const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)");
  const ease = "cubic-bezier(0.2, 0.8, 0.2, 1)";

  // ---------- Toasts ----------

  const toasts = document.querySelector(".toasts");

  // Success toasts fade out on their own (CSS); remove them once hidden.
  function track(el) {
    el.addEventListener("animationend", (e) => {
      if (e.animationName === "toast-out") el.remove();
    });
  }

  function dismiss(el) {
    el.classList.add("leaving"); // plays toast-out; track() removes it afterwards
  }

  function toast(message, kind = "ok") {
    if (!toasts) return;
    const el = document.createElement("div");
    el.className = kind === "error" ? "toast toast-error" : "toast";
    el.setAttribute("role", kind === "error" ? "alert" : "status");
    el.textContent = message;
    track(el);
    toasts.append(el);
    if (kind === "error") setTimeout(() => dismiss(el), 6000);
  }

  document.addEventListener("click", (event) => {
    const close = event.target.closest(".toast-close");
    if (close) dismiss(close.closest(".toast"));
  });
  document.querySelectorAll(".toast").forEach(track);

  // ---------- Confirmation dialog ----------

  const dialog = document.getElementById("confirm-dialog");

  document.addEventListener("submit", (event) => {
    const form = event.target;
    const message = form.dataset.confirm;
    if (!message || form.dataset.confirmed) return;
    event.preventDefault();

    const proceed = () => {
      form.dataset.confirmed = "1";
      form.requestSubmit();
    };
    if (!dialog || typeof dialog.showModal !== "function") {
      if (window.confirm(message)) proceed();
      return;
    }
    dialog.querySelector(".dialog-msg").textContent = message;
    dialog.querySelector('[value="ok"]').textContent = form.dataset.confirmLabel || "Delete";
    dialog.returnValue = "";
    dialog.addEventListener("close", () => {
      if (dialog.returnValue === "ok") proceed();
    }, { once: true });
    dialog.showModal();
    dialog.querySelector('[value="cancel"]').focus();
  });

  dialog?.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close("cancel"); // backdrop click
  });

  // ---------- Animated <details> ----------
  // The whole <details> is animated between its closed height (summary only) and its
  // open height. Animating the body instead stalls: its padding keeps it from
  // reaching zero, so it stopped short and then snapped shut.

  for (const details of document.querySelectorAll("details.panel")) {
    const summary = details.querySelector(":scope > summary");
    if (!summary) continue;
    let anim = null;

    const run = (from, to, duration, done) => {
      details.style.overflow = "hidden";
      anim = details.animate({ height: [`${from}px`, `${to}px`] }, { duration, easing: ease });
      anim.onfinish = () => {
        anim = null;
        details.style.overflow = "";
        done?.();
      };
    };

    summary.addEventListener("click", (event) => {
      if (reduceMotion.matches) return;
      event.preventDefault();
      const from = details.getBoundingClientRect().height; // mid-animation too
      anim?.cancel();
      const closed = summary.offsetHeight + details.offsetHeight - details.clientHeight; // + borders
      if (details.open && !details.classList.contains("closing")) {
        details.classList.add("closing");
        run(from, closed, 220, () => {
          details.open = false;
          details.classList.remove("closing");
        });
      } else {
        details.classList.remove("closing");
        details.open = true;
        run(from, details.offsetHeight, 280);
      }
    });
  }

  // ---------- Custom dropdowns ----------
  // Native <select>s are replaced by an animated listbox in the top layer, so it is
  // never clipped by scrolling tables. The hidden native select stays the source of
  // truth: forms, autosubmit and no-JS behaviour are unchanged.

  const canPopover = typeof HTMLElement.prototype.showPopover === "function";
  let openMenu = null;

  function enhanceSelect(select) {
    const wrap = document.createElement("div");
    wrap.className = "select";
    if (select.closest("label, .stack")) wrap.classList.add("select-block");

    const button = document.createElement("button");
    button.type = "button";
    button.className = "select-button";
    button.setAttribute("aria-haspopup", "listbox");
    button.setAttribute("aria-expanded", "false");
    const label = select.getAttribute("aria-label") || select.closest("label")?.firstChild?.textContent?.trim();
    if (label) button.setAttribute("aria-label", label);
    const text = document.createElement("span");
    text.className = "select-value";
    button.append(text);

    const menu = document.createElement("ul");
    menu.className = "select-menu";
    menu.setAttribute("role", "listbox");
    menu.popover = "manual";
    menu.tabIndex = -1;

    const items = [...select.options].map((option) => {
      const li = document.createElement("li");
      li.setAttribute("role", "option");
      li.dataset.value = option.value;
      li.textContent = option.textContent;
      li.tabIndex = -1;
      menu.append(li);
      return li;
    });

    select.before(wrap);
    wrap.append(select, button);
    document.body.append(menu);
    select.classList.add("select-native");
    select.tabIndex = -1;
    select.setAttribute("aria-hidden", "true");
    select.addEventListener("focus", () => button.focus()); // label clicks

    const sync = () => {
      const current = select.options[select.selectedIndex];
      text.textContent = current ? current.textContent : "";
      wrap.dataset.value = select.value;
      for (const li of items) li.setAttribute("aria-selected", String(li.dataset.value === select.value));
    };
    sync();
    select.addEventListener("change", sync);

    const place = () => {
      const rect = button.getBoundingClientRect();
      const height = menu.offsetHeight;
      const below = innerHeight - rect.bottom;
      const up = below < height + 12 && rect.top > below;
      menu.classList.toggle("up", up);
      menu.style.minWidth = `${rect.width}px`;
      menu.style.left = `${Math.min(rect.left, innerWidth - menu.offsetWidth - 8)}px`;
      menu.style.top = up ? `${rect.top - height - 6}px` : `${rect.bottom + 6}px`;
    };

    const focusItem = (li) => {
      for (const other of items) other.classList.toggle("active", other === li);
      li?.focus({ preventScroll: true });
    };

    const open = () => {
      if (openMenu && openMenu !== close) openMenu();
      menu.showPopover();
      place();
      requestAnimationFrame(() => menu.classList.add("open"));
      wrap.classList.add("open");
      button.setAttribute("aria-expanded", "true");
      focusItem(items.find((li) => li.dataset.value === select.value) || items[0]);
      openMenu = close;
    };

    function close(refocus = false) {
      if (openMenu !== close) return;
      openMenu = null;
      menu.classList.remove("open");
      wrap.classList.remove("open");
      button.setAttribute("aria-expanded", "false");
      const hide = () => menu.matches(":popover-open") && !menu.classList.contains("open") && menu.hidePopover();
      if (reduceMotion.matches) hide();
      else setTimeout(hide, 180);
      if (refocus) button.focus();
    }

    const choose = (li) => {
      if (li.dataset.value !== select.value) {
        select.value = li.dataset.value;
        select.dispatchEvent(new Event("change", { bubbles: true }));
      }
      close(true);
    };

    button.addEventListener("click", () => (openMenu === close ? close() : open()));
    button.addEventListener("keydown", (event) => {
      if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) {
        event.preventDefault();
        open();
      }
    });
    menu.addEventListener("click", (event) => {
      const li = event.target.closest("li");
      if (li) choose(li);
    });
    menu.addEventListener("pointermove", (event) => {
      const li = event.target.closest("li");
      if (li && !li.classList.contains("active")) focusItem(li);
    });
    menu.addEventListener("keydown", (event) => {
      const index = items.indexOf(document.activeElement);
      switch (event.key) {
        case "ArrowDown": event.preventDefault(); focusItem(items[Math.min(index + 1, items.length - 1)]); break;
        case "ArrowUp": event.preventDefault(); focusItem(items[Math.max(index - 1, 0)]); break;
        case "Home": event.preventDefault(); focusItem(items[0]); break;
        case "End": event.preventDefault(); focusItem(items[items.length - 1]); break;
        case "Enter": case " ": event.preventDefault(); if (index >= 0) choose(items[index]); break;
        case "Escape": event.preventDefault(); close(true); break;
        case "Tab": close(true); break;
      }
    });
    return { button, close };
  }

  if (canPopover) {
    for (const select of document.querySelectorAll("select:not([multiple])")) enhanceSelect(select);
    document.addEventListener("pointerdown", (event) => {
      if (openMenu && !event.target.closest(".select-menu, .select-button")) openMenu();
    });
    addEventListener("resize", () => openMenu?.());
    addEventListener("scroll", () => openMenu?.(), { passive: true, capture: true });
  }

  // ---------- Auto-submitting selects ----------
  // GET forms (filters) navigate; POST forms (inline visibility) save in the background.

  document.addEventListener("change", async (event) => {
    const select = event.target;
    if (!select.matches("select[data-autosubmit]")) return;
    const form = select.form;
    if (form.method.toLowerCase() !== "post") {
      form.requestSubmit();
      return;
    }

    const previous = [...select.options].find((o) => o.defaultSelected)?.value;
    const control = select.closest(".select") || select;
    const body = new URLSearchParams(new FormData(form));
    control.classList.add("busy");
    try {
      const res = await fetch(form.action, {
        method: "POST",
        body,
        headers: { "X-Artifax-Fetch": "1" },
      });
      if (!res.ok) throw new Error((await res.text()).trim() || "Could not save.");
      for (const option of select.options) option.defaultSelected = option.value === select.value;
      control.classList.remove("saved");
      void control.offsetWidth; // restart the animation
      control.classList.add("saved");
      toast(`Visibility set to ${select.value}.`);
    } catch (err) {
      if (previous) {
        select.value = previous;
        select.dispatchEvent(new Event("change")); // resync the custom control (not bubbling)
      }
      toast(err.message || "Could not save.", "error");
    } finally {
      control.classList.remove("busy");
    }
  });

  // ---------- Copy buttons ----------

  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-copy], [data-copy-target]");
    if (!button) return;
    const value = button.dataset.copy ?? document.getElementById(button.dataset.copyTarget)?.textContent ?? "";
    try {
      await navigator.clipboard.writeText(value);
      toast("Copied to clipboard.");
    } catch {
      window.prompt("Copy:", value);
    }
  });

  // ---------- Local timestamps ----------

  const format = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
  const formatDay = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
  for (const el of document.querySelectorAll("time[datetime]")) {
    const date = new Date(el.dateTime);
    if (!Number.isNaN(date.getTime())) {
      el.title = el.dateTime;
      el.textContent = (el.hasAttribute("data-date-only") ? formatDay : format).format(date);
    }
  }
})();

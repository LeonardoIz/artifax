// Progressive enhancements. Every page works without JavaScript.

// Ask before destructive form submissions.
document.addEventListener("submit", (event) => {
  const message = event.target.dataset.confirm;
  if (message && !window.confirm(message)) event.preventDefault();
});

// Submit filter and visibility selects as soon as they change.
document.addEventListener("change", (event) => {
  const select = event.target;
  if (select.matches("select[data-autosubmit]")) select.form.requestSubmit();
});

// Copy buttons.
document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-copy]");
  if (!button) return;
  try {
    await navigator.clipboard.writeText(button.dataset.copy);
    const label = button.textContent;
    button.textContent = "Copied";
    setTimeout(() => (button.textContent = label), 1500);
  } catch {
    window.prompt("Copy:", button.dataset.copy);
  }
});

// Show timestamps in the viewer's local time zone.
const format = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
for (const el of document.querySelectorAll("time[datetime]")) {
  const date = new Date(el.dateTime);
  if (!Number.isNaN(date.getTime())) {
    el.title = el.dateTime;
    el.textContent = format.format(date);
  }
}

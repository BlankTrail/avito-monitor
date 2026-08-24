// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This is here instead of htmx, which spec §7 names.
//
// What this panel actually needs from it is one thing: press a button, fetch a
// fragment, put it in an element. That is the function below. htmx is about
// fifty kilobytes of third-party JavaScript, and vendoring it into a public
// AGPL repository to replace something a reader can audit in a minute is a
// trade this project does not make anywhere else either — see the chart written
// against image/png and the Bot API client written against net/http.
//
// Two attributes:
//   data-load="/url"     fetch on click, replace the target's innerHTML
//   data-into="#id"      where it goes; default is the element itself
//
// The fragment is HTML the server already escaped. Nothing here builds markup
// from a value, which is why there is no escaping in this file: everything it
// inserts came from a handler that did it.

(function () {
  "use strict";

  function target(el) {
    var sel = el.getAttribute("data-into");
    return sel ? document.querySelector(sel) : el;
  }

  function load(el) {
    var url = el.getAttribute("data-load");
    var into = target(el);
    if (!url || !into) return;

    into.setAttribute("aria-busy", "true");
    fetch(url, { headers: { "X-Fragment": "1" } })
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.text();
      })
      .then(function (html) {
        into.innerHTML = html;
      })
      .catch(function (err) {
        // Said out loud rather than left as a spinner that never stops: a panel
        // whose check silently does nothing teaches its user to press twice.
        into.textContent = "Не удалось: " + err.message;
      })
      .finally(function () {
        into.removeAttribute("aria-busy");
      });
  }

  document.addEventListener("click", function (ev) {
    var el = ev.target.closest("[data-load]");
    if (!el) return;
    ev.preventDefault();
    load(el);
  });

  // A form with data-confirm asks before it posts. Used by the delete buttons,
  // where the alternative is an undo this product does not have.
  document.addEventListener("submit", function (ev) {
    var msg = ev.target.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) ev.preventDefault();
  });
})();

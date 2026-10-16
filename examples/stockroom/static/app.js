/* Three small behaviours, and nothing that the page depends on.
 *
 * Every screen here is server-rendered and every action is a form post, so this
 * file is enhancement only: with JavaScript off, the app still adds, edits,
 * deletes, filters and sends. That is the whole reason there is no framework in
 * it — an internal tool that stops working when a script fails to load is a
 * support ticket nobody can diagnose from the outside.
 */

(function () {
  "use strict";

  // 1. Dismiss the flash.
  document.addEventListener("click", function (e) {
    var close = e.target.closest("[data-dismiss]");
    if (close) close.closest(".flash").remove();
  });

  // 2. The aisle band's tooltip. The band is already labelled underneath — this
  //    adds the exact figure on hover, and nothing that is only available here.
  var tip = null;
  function showTip(el) {
    if (!tip) {
      tip = document.createElement("div");
      tip.id = "tip";
      document.body.appendChild(tip);
    }
    var box = el.getBoundingClientRect();
    tip.textContent = el.dataset.tip;
    tip.style.left = box.left + box.width / 2 + "px";
    tip.style.top = box.top + "px";
    tip.classList.add("on");
  }
  function hideTip() { if (tip) tip.classList.remove("on"); }

  document.addEventListener("mouseover", function (e) {
    var seg = e.target.closest("[data-tip]");
    if (seg) showTip(seg); else hideTip();
  });
  document.addEventListener("scroll", hideTip, true);

  // 3. Guard the two irreversible buttons.
  //
  //    Only these two: a confirmation on every action trains people to click
  //    through confirmations. Deleting a line takes its movement history with
  //    it, and an email leaves the building — everything else on these screens
  //    can be undone by doing the opposite thing.
  document.addEventListener("submit", function (e) {
    var form = e.target;

    if (form.action.indexOf("/delete") !== -1) {
      var typed = form.querySelector('input[name="confirm"]');
      if (typed && !typed.value.trim()) {
        e.preventDefault();
        typed.focus();
        return;
      }
    }

    var channel = form.querySelector('input[name="channel"]');
    if (channel && channel.value === "email") {
      var to = form.querySelector('input[name="to"]');
      var list = to && to.value.trim();
      if (!list) return; // The server says what is missing, in its own words.
      if (!window.confirm("Send this report to " + list + "?")) e.preventDefault();
    }
  });

  // 4. Keep the submit button from being pressed twice. A double-click on
  //    "Post to Slack" is idempotent on the platform side — the send carries a
  //    key and a replay returns the original — but the second press should not
  //    look like it did nothing either.
  document.addEventListener("submit", function (e) {
    var button = e.target.querySelector('button[type="submit"]:not(.step)');
    if (!button || e.defaultPrevented) return;
    setTimeout(function () {
      button.disabled = true;
      button.dataset.was = button.textContent;
      button.textContent = "Working…";
    }, 0);
    // A download does not navigate, so the button has to come back by itself.
    setTimeout(function () {
      if (button.dataset.was) {
        button.disabled = false;
        button.textContent = button.dataset.was;
      }
    }, 8000);
  });
})();

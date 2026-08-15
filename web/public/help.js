(function () {
  "use strict";
  // Tema: pre paint (head, bez defer) + toggle dugme
  try {
    var saved = localStorage.getItem("mr-theme") || localStorage.getItem("noema-theme") || "";
    if (saved === "dark" || saved === "light") document.documentElement.setAttribute("data-theme", saved);
  } catch (e) {}

  var themeBtn = document.getElementById("helpTheme");
  if (themeBtn) {
    themeBtn.addEventListener("click", function () {
      var cur = document.documentElement.getAttribute("data-theme");
      var next = cur === "dark" ? "light" : "dark";
      document.documentElement.setAttribute("data-theme", next);
      try { localStorage.setItem("mr-theme", next); } catch (e) {}
    });
  }

  // Search po sekcijama
  var search = document.getElementById("helpSearch");
  var count = document.getElementById("helpCount");
  var cards = Array.prototype.slice.call(document.querySelectorAll(".chapter"));
  var nav = Array.prototype.slice.call(document.querySelectorAll(".nav a"));
  if (search) {
    search.addEventListener("input", function () {
      var q = search.value.trim().toLowerCase();
      var shown = 0;
      cards.forEach(function (card) {
        var hit = !q || card.textContent.toLowerCase().indexOf(q) !== -1;
        card.style.display = hit ? "" : "none";
        if (hit) shown++;
      });
      nav.forEach(function (a) {
        var target = document.querySelector(a.getAttribute("href"));
        a.style.display = !q || (target && target.style.display !== "none") ? "" : "none";
      });
      if (count) count.textContent = q ? shown + " / " + cards.length + " sections match" : "";
    });
  }
})();

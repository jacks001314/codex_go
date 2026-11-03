package tui

// Rust #58436 (0ffae14a43) rewrote the bundled viewer assets
// (tui/assets/inline_visualization/visualize.html and visualize.css) to add tab
// switching, a `.viz-carousel` variant picker with previous/next controls and
// screen-reader announcements, and touch tooltips.
//
// Go materializes its own minimal viewer document (inline_visualization.go)
// instead of shipping those assets, so the equivalent user-visible behaviour is
// implemented here as a small dependency-free runtime that every generated
// viewer document embeds. Styling is intentionally not pixel-identical to the
// Rust stylesheet; the interactive contracts are what parity requires.

const inlineVisualizationControlsCSS = `:root{color-scheme:light dark}
.btn{display:inline-flex;align-items:center;gap:4px;border:1px solid currentColor;border-radius:6px;background:transparent;color:inherit;cursor:pointer;font:inherit;padding:6px 10px}
.btn-sm{padding:4px 8px;font-size:0.875rem;line-height:1.25rem;border-radius:4px}
.nav{display:flex;flex-wrap:wrap;gap:4px;margin:0 0 8px;padding:0;list-style:none}
.nav-link{background:transparent;border:1px solid transparent;border-radius:6px;color:inherit;cursor:pointer;font:inherit;padding:6px 10px}
.nav-pills .nav-link.active{background:#e8f0fe;border-color:#c2d7fb;color:#0b57d0}
.nav-pills [role="tab"][aria-selected="true"]{font-weight:600}
[role="tabpanel"][hidden]{display:none}
.viz-carousel{position:relative;margin:8px 0 0}
.viz-carousel > [data-variant][hidden]{display:none}
.viz-carousel-controls{display:flex;align-items:center;gap:6px;margin-top:6px;font-size:0.8125rem}
.viz-carousel-controls button{display:inline-flex;align-items:center;justify-content:center;min-width:32px;min-height:32px;border:1px solid currentColor;border-radius:6px;background:transparent;color:inherit;cursor:pointer;font:inherit}
.viz-carousel-picker{position:relative}
.viz-carousel-picker summary{display:inline-flex;align-items:center;gap:4px;cursor:pointer;list-style:none;padding:6px 8px;border:1px solid currentColor;border-radius:6px}
.viz-carousel-picker summary::-webkit-details-marker{display:none}
.viz-carousel-count{opacity:0.7}
.viz-carousel-caret{margin-left:2px}
.viz-carousel-options{position:absolute;z-index:20;left:0;top:100%;display:flex;flex-direction:column;min-width:140px;margin-top:4px;padding:4px;border-radius:6px;background:Canvas;border:1px solid currentColor}
.viz-carousel-option{background:transparent;border:0;color:inherit;cursor:pointer;font:inherit;padding:6px 8px;text-align:left;border-radius:4px}
.viz-carousel-option[aria-current="true"]{font-weight:600}
.tabular-nums{font-variant-numeric:tabular-nums}
.tooltip{position:fixed;z-index:2147483647;max-width:280px;padding:4px 8px;border-radius:4px;background:#1f2328;color:#fff;font-size:0.75rem;line-height:1.4}
.sr-only{position:absolute;width:1px;height:1px;margin:-1px;padding:0;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap;border:0}
`

const inlineVisualizationControlsJS = `(function () {
  "use strict";
  var TAB = '[role="tab"]';
  function panelFor(tab) {
    var id = tab.getAttribute("aria-controls");
    return id ? document.getElementById(id) : null;
  }
  function selectTab(tab, focus) {
    var list = tab.closest('[role="tablist"]');
    if (!list) { return; }
    Array.prototype.slice.call(list.querySelectorAll(TAB)).forEach(function (candidate) {
      var selected = candidate === tab;
      candidate.setAttribute("aria-selected", selected ? "true" : "false");
      candidate.setAttribute("tabindex", selected ? "0" : "-1");
      if (selected) { candidate.classList.add("active"); } else { candidate.classList.remove("active"); }
      var panel = panelFor(candidate);
      if (panel) { panel.hidden = !selected; }
    });
    if (focus) { tab.focus(); }
  }
  function tabFrom(event) {
    var target = event.target;
    var tab = target && target.closest ? target.closest(TAB) : null;
    return tab && tab.closest('[role="tablist"]') ? tab : null;
  }
  function onTabClick(event) {
    var tab = tabFrom(event);
    if (tab) { selectTab(tab, false); }
  }
  function onTabKeydown(event) {
    var tab = tabFrom(event);
    if (!tab) { return; }
    var tabs = Array.prototype.slice.call(tab.closest('[role="tablist"]').querySelectorAll(TAB));
    var index = tabs.indexOf(tab);
    var next = index;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") { next = (index + 1) % tabs.length; }
    else if (event.key === "ArrowLeft" || event.key === "ArrowUp") { next = (index + tabs.length - 1) % tabs.length; }
    else if (event.key === "Home") { next = 0; }
    else if (event.key === "End") { next = tabs.length - 1; }
    else { return; }
    event.preventDefault();
    selectTab(tabs[next], true);
  }
  function initTabs() {
    Array.prototype.slice.call(document.querySelectorAll('.nav[role="tablist"]')).forEach(function (list) {
      var selected = list.querySelector('[role="tab"][aria-selected="true"]') ||
        list.querySelector('[role="tab"].active') || list.querySelector(TAB);
      if (selected) { selectTab(selected, false); }
    });
  }
  function initCarousel(root) {
    var panels = Array.prototype.slice.call(root.querySelectorAll(":scope > [data-variant]"));
    if (panels.length === 0) { return; }
    root.setAttribute("role", "group");
    panels.forEach(function (panel, index) {
      panel.hidden = index !== 0;
      panel.setAttribute("role", "group");
      if (!panel.hasAttribute("aria-label")) { panel.setAttribute("aria-label", panel.dataset.variant || ""); }
    });
    root.dataset.activeVariant = panels[0].dataset.variant || "";
    if (panels.length === 1) { return; }
    var controls = document.createElement("div");
    controls.className = "viz-carousel-controls";
    controls.innerHTML =
      '<button type="button" class="viz-carousel-previous">&#8249;</button>' +
      '<details class="viz-carousel-picker"><summary>' +
      '<span class="viz-carousel-count tabular-nums"></span>' +
      '<span class="viz-carousel-name"></span>' +
      '<svg class="viz-carousel-caret" width="14" height="14" viewBox="0 0 20 21" aria-hidden="true" focusable="false">' +
      '<path d="M15.28 7.71 10 12.99 4.72 7.71" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>' +
      '</summary><div class="viz-carousel-options"></div></details>' +
      '<button type="button" class="viz-carousel-next">&#8250;</button>';
    var previous = controls.querySelector(".viz-carousel-previous");
    var next = controls.querySelector(".viz-carousel-next");
    var picker = controls.querySelector(".viz-carousel-picker");
    var count = controls.querySelector(".viz-carousel-count");
    var name = controls.querySelector(".viz-carousel-name");
    var options = controls.querySelector(".viz-carousel-options");
    var status = document.createElement("span");
    status.className = "sr-only";
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    status.setAttribute("aria-atomic", "true");
    previous.setAttribute("aria-label", root.dataset.previousLabel || "Previous variant");
    next.setAttribute("aria-label", root.dataset.nextLabel || "Next variant");
    var active = 0;
    var optionButtons = [];
    function render(announce) {
      panels.forEach(function (panel, index) {
        var selected = index === active;
        panel.hidden = !selected;
        panel.setAttribute("aria-hidden", selected ? "false" : "true");
      });
      var variant = panels[active].dataset.variant || "";
      root.dataset.activeVariant = variant;
      count.textContent = (active + 1) + "/" + panels.length;
      name.textContent = variant;
      optionButtons.forEach(function (option, index) {
        option.setAttribute("aria-current", index === active ? "true" : "false");
      });
      if (announce) { status.textContent = variant + " (" + (active + 1) + " of " + panels.length + ")"; }
    }
    function step(delta) {
      active = (active + delta + panels.length) % panels.length;
      render(true);
    }
    previous.addEventListener("click", function () { step(-1); });
    next.addEventListener("click", function () { step(1); });
    picker.addEventListener("keydown", function (event) {
      if (event.key === "ArrowLeft" || event.key === "ArrowUp") { event.preventDefault(); step(-1); }
      else if (event.key === "ArrowRight" || event.key === "ArrowDown") { event.preventDefault(); step(1); }
    });
    panels.forEach(function (panel, index) {
      var option = document.createElement("button");
      option.type = "button";
      option.className = "viz-carousel-option";
      option.textContent = panel.dataset.variant || "";
      option.addEventListener("click", function () {
        active = index;
        picker.open = false;
        render(true);
      });
      options.appendChild(option);
      optionButtons.push(option);
    });
    root.appendChild(controls);
    root.appendChild(status);
    render(false);
  }
  function tooltipTrigger(event) {
    if (!event.composedPath) { return null; }
    var path = event.composedPath();
    for (var i = 0; i < path.length; i += 1) {
      var node = path[i];
      if (node && node.nodeType === 1 && node.hasAttribute && node.hasAttribute("data-tooltip")) { return node; }
    }
    return null;
  }
  function initTooltips() {
    var bubble = null;
    function hide() {
      if (bubble) { bubble.remove(); bubble = null; }
    }
    document.addEventListener("pointerdown", function (event) {
      var trigger = tooltipTrigger(event);
      hide();
      if (!trigger || event.pointerType === "mouse") { return; }
      event.preventDefault();
      bubble = document.createElement("div");
      bubble.className = "tooltip";
      bubble.setAttribute("role", "tooltip");
      bubble.textContent = trigger.getAttribute("data-tooltip") || "";
      document.body.appendChild(bubble);
      var rect = trigger.getBoundingClientRect();
      bubble.style.left = Math.max(4, rect.left) + "px";
      bubble.style.top = (rect.bottom + 6) + "px";
    });
    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape") { hide(); }
    });
    window.addEventListener("pagehide", hide, { once: true });
  }
  function start() {
    initTabs();
    Array.prototype.slice.call(document.querySelectorAll(".viz-carousel")).forEach(initCarousel);
    initTooltips();
  }
  document.addEventListener("click", onTabClick);
  document.addEventListener("keydown", onTabKeydown);
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
`

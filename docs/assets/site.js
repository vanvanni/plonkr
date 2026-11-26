/* Plonkr site script: mobile menu, terminal preview, closing pebble drop.
   Everything is readable without JavaScript. */
(function () {
  'use strict';
  var root = document.documentElement;
  root.classList.add('js');
  var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* Mobile menu */
  var btn = document.querySelector('.menu-btn');
  var nav = document.getElementById('site-nav');
  if (btn && nav) {
    var setOpen = function (open) {
      btn.setAttribute('aria-expanded', String(open));
      nav.classList.toggle('open', open);
    };
    btn.addEventListener('click', function () { setOpen(btn.getAttribute('aria-expanded') !== 'true'); });
    nav.addEventListener('click', function (e) { if (e.target.closest('a')) setOpen(false); });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') { setOpen(false); btn.focus(); } });
  }

  /* Closing section: drop the pebble once when it scrolls into view */
  var cta = document.querySelector('.cta');
  if (cta && 'IntersectionObserver' in window && !reduce) {
    var seen = new IntersectionObserver(function (entries) {
      if (entries.some(function (e) { return e.isIntersecting; })) { cta.classList.add('in'); seen.disconnect(); }
    }, { threshold: 0.5 });
    seen.observe(cta);
  }

  /* Terminal preview */
  var tabs = Array.prototype.slice.call(document.querySelectorAll('[role="tab"]'));
  if (!tabs.length) return;
  var timers = [];
  var current = tabs[0];

  function panelFor(tab) { return document.getElementById(tab.getAttribute('aria-controls')); }
  function clearTimers() { timers.forEach(clearTimeout); timers = []; }

  function play(panel) {
    clearTimers();
    var lines = panel.querySelectorAll('.reveal-line');
    Array.prototype.forEach.call(lines, function (line) { line.classList.remove('in'); });
    if (reduce) { Array.prototype.forEach.call(lines, function (l) { l.classList.add('in'); }); return; }
    Array.prototype.forEach.call(lines, function (line, i) {
      timers.push(setTimeout(function () { line.classList.add('in'); }, 260 + i * 360));
    });
  }

  function select(tab, focus) {
    current = tab;
    tabs.forEach(function (t) {
      var on = t === tab;
      t.setAttribute('aria-selected', String(on));
      t.tabIndex = on ? 0 : -1;
      panelFor(t).hidden = !on;
    });
    if (focus) tab.focus();
    play(panelFor(tab));
  }

  tabs.forEach(function (tab, i) {
    tab.addEventListener('click', function () { select(tab, false); });
    tab.addEventListener('keydown', function (e) {
      var next = null;
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') next = tabs[(i + 1) % tabs.length];
      else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') next = tabs[(i - 1 + tabs.length) % tabs.length];
      else if (e.key === 'Home') next = tabs[0];
      else if (e.key === 'End') next = tabs[tabs.length - 1];
      if (next) { e.preventDefault(); select(next, true); }
    });
  });

  var replay = document.getElementById('replay');
  if (replay) replay.addEventListener('click', function () { play(panelFor(current)); });

  var demo = document.getElementById('demo');
  function onScreen() {
    var r = demo.getBoundingClientRect();
    return r.top < window.innerHeight * 0.85 && r.bottom > 0;
  }
  if (!demo || reduce || onScreen() || !('IntersectionObserver' in window)) {
    play(panelFor(current));
  } else {
    var io = new IntersectionObserver(function (entries) {
      if (entries.some(function (e) { return e.isIntersecting; })) { io.disconnect(); play(panelFor(current)); }
    }, { threshold: 0.2 });
    io.observe(demo);
  }
})();

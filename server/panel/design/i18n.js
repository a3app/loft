/* loft-panel languages. The page is written in Russian; en.js and zh.js map
   its Russian text to English and Simplified Chinese. After every draw the
   visible text and a few attributes are swapped, whole strings first, then
   the pieces a template was built from (they are joined with live values).
   User data is left alone: inputs, textareas, logs, links and anything
   marked data-i18n="no". The choice is per browser; without one the panel's
   default applies. */
(() => {
  const CYR = /[А-Яа-яЁё]/;
  const ORDER = ['en', 'ru', 'zh'];
  const NAMES = { ru: 'Русский', en: 'English', zh: '中文' };
  const PILL = { ru: 'RU', en: 'EN', zh: '中文' };
  const HTML_LANG = { ru: 'ru', en: 'en', zh: 'zh-CN' };
  // A template key keeps a $ where a value goes ("из $" → "of $"): its parts
  // map one to one. Sentences of a long entry map too, for text the page
  // builds from part of a sentence and part of another.
  const edge = x => x.replace(/^[\s,;:()=+?|&!]+|[\s,;:(=+?|&!]+$/g, '');
  function fill(src) {
    const D = {};
    const add = (k, v) => { if (k && CYR.test(k) && !(k in D)) D[k] = v; };
    for (const [k, v] of Object.entries(src || {})) D[k] = v;
    for (const [k, v] of Object.entries(src || {})) {
      const ks = k.split('$'), vs = String(v).split('$');
      if (ks.length > 1 && ks.length === vs.length) ks.forEach((p, i) => add(edge(p), edge(vs[i])));
      const sk = k.split(/(?<=[.!?…])\s+/), sv = String(v).split(/(?<=[.!?…])\s+/);
      if (sk.length > 1 && sk.length === sv.length) sk.forEach((p, i) => add(p.trim(), sv[i].trim()));
    }
    return { D, keys: Object.keys(D).sort((a, b) => b.length - a.length) };
  }
  const PACKS = { en: fill(window.LOFT_EN), zh: fill(window.LOFT_ZH) };
  const LETTER = /[А-Яа-яЁёA-Za-z]/;
  let saved = '';
  try { saved = localStorage.getItem('loft-lang') || ''; } catch (e) { /* private window */ }
  const I = window.I18N = { lang: ORDER.includes(saved) ? saved : 'ru', saved: !!saved, missing: new Set() };

  function pack() { return PACKS[I.lang]; }

  // replace k in s only where it is not part of a longer word
  function swap(s, k, v) {
    let out = '', i = 0;
    for (;;) {
      const j = s.indexOf(k, i);
      if (j < 0) return out + s.slice(i);
      const before = s[j - 1], after = s[j + k.length];
      const edgeL = !CYR.test(k[0]) || !before || !LETTER.test(before);
      const edgeR = !CYR.test(k[k.length - 1]) || !after || !LETTER.test(after);
      out += s.slice(i, j) + (edgeL && edgeR ? v : k);
      i = j + k.length;
    }
  }
  function t(s) {
    const P = pack();
    if (!P || !s || !CYR.test(s)) return s;
    const lead = s.match(/^\s*/)[0], trail = s.match(/\s*$/)[0], core = s.trim();
    const q = x => x.replace(/«/g, '“').replace(/»/g, '”');
    if (Object.prototype.hasOwnProperty.call(P.D, core)) return lead + q(P.D[core]) + trail;
    let out = core;
    for (const k of P.keys) {
      if (out.includes(k)) out = swap(out, k, P.D[k]);
      if (!CYR.test(out)) break;
    }
    if (CYR.test(out)) I.missing.add(core);
    return lead + q(out) + trail;
  }
  I.t = t;

  const SKIP = 'script,style,textarea,.joblog,.uri,[data-i18n="no"]';
  const ATTRS = ['placeholder', 'aria-label', 'title', 'alt'];
  function apply(root) {
    if (!pack() || !root) return;
    const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
      acceptNode: n => CYR.test(n.nodeValue) && !(n.parentElement && n.parentElement.closest(SKIP)) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT });
    const nodes = [];
    while (w.nextNode()) nodes.push(w.currentNode);
    for (const n of nodes) n.nodeValue = t(n.nodeValue);
    root.querySelectorAll('[placeholder],[aria-label],[title],[alt]').forEach(e => {
      if (e.closest(SKIP)) return;
      for (const a of ATTRS) { const v = e.getAttribute(a); if (v && CYR.test(v)) e.setAttribute(a, t(v)); }
    });
  }
  I.apply = apply;

  // language controls: the header select (short names), the menu select on
  // phones, the sign-in and wizard selects. Options are filled here so a
  // two-language HTML mock still offers Chinese after this file loads.
  function fillSelect(s, names) {
    const want = ORDER.map(l => l + ':' + names[l]).join('|');
    if (s.dataset.i18nLangs === want) return;
    const cur = s.value || ORDER[s.selectedIndex] || I.lang;
    s.innerHTML = ORDER.map(l => `<option value="${l}">${names[l]}</option>`).join('');
    s.dataset.i18nLangs = want;
    s.value = ORDER.includes(cur) ? cur : I.lang;
  }
  function controls() {
    for (const id of ['hLang', 'mLang', 'lg3', 'w1d']) {
      const s = document.getElementById(id);
      if (!s) continue;
      fillSelect(s, id === 'hLang' ? PILL : NAMES);
      s.value = I.lang;
      s.onchange = () => set(s.value);
    }
    document.documentElement.lang = HTML_LANG[I.lang] || I.lang;
    if (pack()) document.title = t(document.title);
  }
  function redraw() {
    const full = !document.getElementById('full').hidden;
    if (full) window.renderFull(); else window.render();
    const d = document.getElementById('dlgRoot');
    if (d && d.firstChild) apply(d);
  }
  function set(l) {
    I.lang = ORDER.includes(l) ? l : 'ru';
    I.saved = true;
    try { localStorage.setItem('loft-lang', I.lang); } catch (e) { /* not kept */ }
    redraw();
  }
  I.set = set;
  // def is the panel's default, used while this browser has not chosen.
  I.def = l => { if (!I.saved && ORDER.includes(l) && l !== I.lang) { I.lang = l; redraw(); } };

  const after = (name, fn) => {
    const f = window[name];
    if (typeof f !== 'function') return;
    window[name] = function (...a) { const r = f.apply(this, a); fn(a); return r; };
  };
  after('render', () => { apply(document.getElementById('app')); apply(document.getElementById('full')); controls(); });
  after('renderFull', () => { apply(document.getElementById('full')); controls(); });
  after('dialog', () => apply(document.getElementById('dlgRoot')));
  const toast0 = window.toast;
  if (typeof toast0 === 'function') window.toast = m => toast0(t(String(m)));
  // the first draw happened before this file loaded
  apply(document.body);
  controls();
})();

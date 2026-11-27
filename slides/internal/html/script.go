package html

// script moves through the slides and the steps of their scenes. Each
// step is drawn from its complete state in the data of the page, never
// from the step before it, so stepping forward, stepping back, choosing
// a slide and opening an address all end in the same picture. The
// address holds the scene and the step (#select/uaa) or the number of a
// slide of blocks (#12), and is replaced, not pushed, at every step.
//
// Motion is short and only joins two states: the view glides to the
// part of the diagram that a step shows, and a dot travels along the
// message that the step adds. With less motion, from the button or the
// setting of the system, both are left out and nothing is lost.
//
// With "?check" in the address the page shows every slide, draws every
// step of every scene, and lists each box whose text the browser drew
// larger than the box, each name larger than its node and each label
// that covers a node or another label; a browser can so confirm the
// measure of the build with its own fonts.
const script = `(function () {
  'use strict';
  const data = JSON.parse(document.getElementById('data').textContent);
  const words = data.words;
  const scenes = data.scenes;
  const slides = Array.from(document.querySelectorAll('#stage > .slide'));
  const root = document.documentElement;
  let current = { slide: 0, step: 0 };
  let cameras = new WeakMap();

  function store(key, value) {
    try { if (value === undefined) return localStorage.getItem(key); localStorage.setItem(key, value); } catch (e) { return null; }
  }

  // ---- drawing one state ----------------------------------------------
  function applyStep(slide, scene, k, animate) {
    const st = scene.steps[k];
    if (!slide.classList.contains('scene')) return;
    const svg = slide.querySelector('svg.diagram');
    const touched = new Set();
    for (const g of svg.querySelectorAll('.event')) {
      const id = g.dataset.id;
      const was = g.dataset.state;
      let state = 'off';
      if (st.events.includes(id)) state = id === st.active ? 'active' : 'done';
      g.dataset.state = state;
      if (animate && state === 'active' && was === 'off') travel(svg, g);
    }
    for (const id of st.events) {
      const g = svg.querySelector('.event[data-id="' + id + '"]');
      if (g && g.dataset.nodes) g.dataset.nodes.split(' ').forEach((n) => touched.add(n));
    }
    for (const n of svg.querySelectorAll('.node')) {
      const id = n.dataset.id;
      n.dataset.state = st.focus.includes(id) ? 'focus' : (st.focus.length && !touched.has(id) ? 'dim' : 'plain');
    }
    for (const div of slide.querySelectorAll('.panel .step')) div.hidden = div.dataset.step !== st.id;
    for (const div of slide.querySelectorAll('.panel .detail .fields')) div.hidden = !st.detail.includes(div.dataset.id);
    const count = slide.querySelector('.panel .count');
    if (count) count.textContent = words.step + ' ' + (k + 1) + ' ' + words.of + ' ' + scene.steps.length;
    moveCamera(slide, svg, st.camera, animate);
  }

  function moveCamera(slide, svg, target, animate) {
    const from = cameras.get(svg) || svg.viewBox.baseVal && [svg.viewBox.baseVal.x, svg.viewBox.baseVal.y, svg.viewBox.baseVal.width, svg.viewBox.baseVal.height];
    const heads = svg.querySelector('.heads');
    const mini = slide.querySelector('.minimap');
    const set = (c) => {
      svg.setAttribute('viewBox', c.map((v) => v.toFixed(2)).join(' '));
      if (heads) heads.setAttribute('transform', 'translate(0 ' + c[1].toFixed(2) + ')');
      cameras.set(svg, c);
    };
    // A label that the view cuts is hidden; its line still shows where
    // the message comes from.
    for (const g of svg.querySelectorAll('.world .event')) {
      const pill = g.querySelector('.pill');
      if (!pill) continue;
      const b = pill.getBBox();
      g.classList.toggle('clipped', b.x < target[0] || b.y < target[1] || b.x + b.width > target[0] + target[2] || b.y + b.height > target[1] + target[3]);
    }
    if (mini) {
      const whole = mini.viewBox.baseVal;
      const zoomed = target[2] < whole.width * 0.98 || target[3] < whole.height * 0.98;
      mini.toggleAttribute('hidden', !zoomed);
      const r = mini.querySelector('.cam');
      r.setAttribute('x', target[0]); r.setAttribute('y', target[1]);
      r.setAttribute('width', target[2]); r.setAttribute('height', target[3]);
      mini.setAttribute('aria-label', zoomed ? words.zoomed : '');
    }
    // The narrow layout gives the diagram the height that --aspect says.
    // It follows the target of the move, not the view in between, so the
    // box changes once, when the move starts, and the same stage has the
    // same box however it was reached.
    svg.style.setProperty('--aspect', (target[2] / target[3]).toFixed(3));
    if (svg.dataset.raf) cancelAnimationFrame(+svg.dataset.raf);
    if (!animate || root.classList.contains('still') || !from) { set(target); return; }
    const start = performance.now();
    const duration = 450;
    const step = (now) => {
      const t = Math.min(1, (now - start) / duration);
      const e = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
      set(from.map((v, i) => v + (target[i] - v) * e));
      if (t < 1) svg.dataset.raf = requestAnimationFrame(step);
    };
    svg.dataset.raf = requestAnimationFrame(step);
  }

  // A dot runs along a message that a step adds, from its sender to its
  // receiver.
  function travel(svg, g) {
    if (root.classList.contains('still')) return;
    const line = g.querySelector('line');
    if (!line) return;
    const [x1, y1, x2, y2] = ['x1', 'y1', 'x2', 'y2'].map((a) => +line.getAttribute(a));
    const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    dot.setAttribute('r', 6);
    dot.setAttribute('class', 'traveller');
    g.appendChild(dot);
    const start = performance.now();
    const run = (now) => {
      const t = Math.min(1, (now - start) / 550);
      dot.setAttribute('cx', x1 + (x2 - x1) * t);
      dot.setAttribute('cy', y1 + (y2 - y1) * t);
      if (t < 1 && dot.isConnected) requestAnimationFrame(run); else dot.remove();
    };
    requestAnimationFrame(run);
  }

  // ---- moving between states ------------------------------------------
  function clamp(pos) {
    const slide = Math.max(0, Math.min(scenes.length - 1, pos.slide));
    const step = Math.max(0, Math.min(scenes[slide].steps.length - 1, pos.step));
    return { slide, step };
  }

  function show(pos, animate) {
    pos = clamp(pos);
    const changed = pos.slide !== current.slide;
    current = pos;
    slides.forEach((s, i) => { s.hidden = i !== pos.slide; });
    const slide = slides[pos.slide];
    const scene = scenes[pos.slide];
    applyStep(slide, scene, pos.step, animate && !changed);
    document.getElementById('jump').value = String(pos.slide);
    renderSteps(scene, pos.step);
    const st = scene.steps[pos.step];
    const hash = '#' + scene.id + (st.id ? '/' + st.id : '');
    if (location.hash !== hash) history.replaceState(null, '', hash);
    const translation = document.getElementById('translation');
    if (translation) translation.hash = hash;
    document.getElementById('prev').disabled = pos.slide === 0 && pos.step === 0;
    document.getElementById('next').disabled = pos.slide === scenes.length - 1 && pos.step === scene.steps.length - 1;
    const live = document.getElementById('live');
    live.textContent = words.slide + ' ' + (pos.slide + 1) + ' ' + words.of + ' ' + scenes.length + ': ' + scene.title +
      (st.id ? '. ' + words.step + ' ' + (pos.step + 1) + ' ' + words.of + ' ' + scene.steps.length + ': ' + st.title : '');
    if (!document.getElementById('drawer').hidden) openDrawer();
    if (document.body.classList.contains('narrow') && changed) slide.scrollIntoView({ block: 'start' });
  }

  function renderSteps(scene, k) {
    const list = document.getElementById('steps');
    // A step button that had the focus gives it to the button of the
    // step it shows now, so that a keyboard keeps its place.
    const focused = list.contains(document.activeElement);
    list.textContent = '';
    if (scene.steps.length < 2) return;
    scene.steps.forEach((st, i) => {
      const li = document.createElement('li');
      const b = document.createElement('button');
      b.type = 'button';
      b.textContent = String(i + 1);
      b.title = st.title;
      b.setAttribute('aria-label', words.step + ' ' + (i + 1) + ': ' + st.title);
      if (i === k) b.setAttribute('aria-current', 'step');
      b.addEventListener('click', () => show({ slide: current.slide, step: i }, true));
      li.appendChild(b);
      list.appendChild(li);
    });
    if (focused) list.querySelector('[aria-current=step]').focus();
  }

  function next() {
    const scene = scenes[current.slide];
    if (current.step < scene.steps.length - 1) show({ slide: current.slide, step: current.step + 1 }, true);
    else if (current.slide < scenes.length - 1) show({ slide: current.slide + 1, step: 0 }, true);
  }

  // Back from the first step of a slide goes to the last step of the
  // slide before: the state that "next" left it in.
  function prev() {
    if (current.step > 0) show({ slide: current.slide, step: current.step - 1 }, true);
    else if (current.slide > 0) show({ slide: current.slide - 1, step: scenes[current.slide - 1].steps.length - 1 }, true);
  }

  function fromHash() {
    let hash = location.hash.slice(1);
    try { hash = decodeURIComponent(hash); } catch (e) { return { slide: 0, step: 0 }; }
    const [id, step] = hash.split('/');
    let slide = scenes.findIndex((s) => s.id === id);
    if (slide < 0) return { slide: 0, step: 0 };
    let k = scenes[slide].steps.findIndex((s) => s.id === step);
    return { slide, step: Math.max(0, k) };
  }

  // ---- the drawer, the reading view, motion -----------------------------
  function openDrawer() {
    const slide = slides[current.slide];
    const body = slide.querySelector('.body');
    const drawer = document.getElementById('drawer');
    if (!body) { drawer.hidden = true; return; }
    document.getElementById('drawer-title').textContent = scenes[current.slide].title;
    document.getElementById('drawer-body').innerHTML = body.innerHTML;
    drawer.hidden = false;
  }

  function toggleDrawer() {
    const drawer = document.getElementById('drawer');
    if (drawer.hidden) { openDrawer(); if (!drawer.hidden) document.getElementById('drawer-close').focus(); }
    else { drawer.hidden = true; const more = slides[current.slide].querySelector('.more'); if (more) more.focus(); }
  }

  function setReading(on) {
    document.getElementById('reading').hidden = !on;
    document.body.classList.toggle('reading', on);
    if (on) {
      const target = document.getElementById('read-' + current.slide);
      if (target) target.scrollIntoView();
      document.getElementById('read-close').focus();
    } else {
      show(current, false);
      document.getElementById('read').focus();
    }
  }

  function setStill(on) {
    root.classList.toggle('still', on);
    document.getElementById('motion').setAttribute('aria-pressed', String(on));
    store('ims-slides-still', on ? '1' : '0');
  }

  function fit() {
    const narrow = innerWidth < 760 || (innerWidth < innerHeight && innerWidth < 1000);
    document.body.classList.toggle('narrow', narrow);
    const viewport = document.getElementById('viewport');
    const scale = Math.min(viewport.clientWidth / 1280, viewport.clientHeight / 720);
    root.style.setProperty('--scale', narrow ? 1 : scale);
  }

  // ---- the check mode --------------------------------------------------
  function overlap(a, b) {
    return a.x < b.x + b.width - 1 && b.x < a.x + a.width - 1 && a.y < b.y + b.height - 1 && b.y < a.y + a.height - 1;
  }

  function check() {
    document.body.classList.add('check');
    const report = [];
    slides.forEach((slide, i) => {
      slide.hidden = false;
      const scene = scenes[i];
      if (slide.classList.contains('page')) {
        for (const box of slide.querySelectorAll('.fixed .box')) {
          let height = 0, width = 0;
          for (const p of box.children) {
            height += p.offsetHeight + parseFloat(getComputedStyle(p).marginTop);
            width = Math.max(width, p.scrollWidth);
          }
          const room = box.clientHeight - 2 * parseFloat(getComputedStyle(box).paddingTop);
          const across = box.clientWidth - 2 * parseFloat(getComputedStyle(box).paddingLeft);
          if (height > room + 1 || width > across + 1) {
            report.push('slide ' + slide.id + ': "' + box.textContent.slice(0, 40) + '" needs ' +
              Math.ceil(width) + 'x' + Math.ceil(height) + ' in ' + Math.floor(across) + 'x' + Math.floor(room));
          }
        }
        for (const table of slide.querySelectorAll('.fixed table')) {
          const planned = Array.from(table.rows).reduce((sum, row) => sum + parseFloat(row.style.height), 0);
          if (table.offsetHeight > planned + table.rows.length + 1) {
            report.push('slide ' + slide.id + ': a table needs ' + table.offsetHeight + ' of ' + planned);
          }
        }
        return;
      }
      const svg = slide.querySelector('svg.diagram');
      for (const n of svg.querySelectorAll('.world .node')) {
        const r = n.querySelector('rect').getBBox();
        for (const t of n.querySelectorAll('text')) {
          if (t.getBBox().width > r.width - 6) report.push('scene ' + scene.id + ': the name of node ' + n.dataset.id + ' is wider than its box');
        }
      }
      scene.steps.forEach((st, k) => {
        applyStep(slide, scene, k, false);
        const where = 'scene ' + scene.id + ' step ' + st.id + ': ';
        const panel = slide.querySelector('.panel');
        if (panel.scrollHeight > panel.clientHeight + 1) report.push(where + 'the panel needs ' + panel.scrollHeight + ' of ' + panel.clientHeight);
        const nodes = Array.from(svg.querySelectorAll('.world .node rect')).map((r) => ({ id: r.parentNode.dataset.id, box: r.getBBox() }));
        const labels = Array.from(svg.querySelectorAll('.event:not([data-state=off])')).map((g) => ({ id: g.dataset.id, box: (g.querySelector('.pill') || g.querySelector('rect')).getBBox(), process: g.classList.contains('process') }));
        for (let a = 0; a < labels.length; a++) {
          for (const n of nodes) {
            if (overlap(labels[a].box, n.box) && !labels[a].process) report.push(where + 'the label of ' + labels[a].id + ' covers node ' + n.id);
          }
          for (let b = a + 1; b < labels.length; b++) {
            if (overlap(labels[a].box, labels[b].box)) report.push(where + 'the labels of ' + labels[a].id + ' and ' + labels[b].id + ' overlap');
          }
        }
      });
      applyStep(slide, scene, 0, false);
    });
    const unique = Array.from(new Set(report));
    const out = document.createElement('pre');
    out.id = 'check';
    out.textContent = unique.length + ' boxes overflow\n' + unique.join('\n');
    document.body.appendChild(out);
  }

  // ---- start -----------------------------------------------------------
  for (const svg of document.querySelectorAll('#stage svg.diagram')) {
    const v = svg.viewBox.baseVal;
    cameras.set(svg, [v.x, v.y, v.width, v.height]);
  }
  if (location.search.includes('check')) { check(); return; }
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const saved = store('ims-slides-still');
  setStill(saved === null ? reduced : saved === '1');
  document.getElementById('prev').addEventListener('click', prev);
  document.getElementById('next').addEventListener('click', next);
  document.getElementById('jump').addEventListener('change', (e) => show({ slide: +e.target.value, step: 0 }, false));
  document.getElementById('motion').addEventListener('click', () => setStill(!root.classList.contains('still')));
  document.getElementById('read').addEventListener('click', () => setReading(true));
  document.getElementById('read-close').addEventListener('click', () => setReading(false));
  document.getElementById('skip').addEventListener('click', (e) => { e.preventDefault(); setReading(true); });
  document.getElementById('drawer-close').addEventListener('click', toggleDrawer);
  for (const more of document.querySelectorAll('.more')) more.addEventListener('click', toggleDrawer);
  addEventListener('resize', fit);
  addEventListener('hashchange', () => { const p = fromHash(); if (p.slide !== current.slide || p.step !== current.step) show(p, false); });
  addEventListener('keydown', (event) => {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const reading = document.body.classList.contains('reading');
    if (event.key === 'Escape') {
      if (!document.getElementById('drawer').hidden) toggleDrawer(); else if (reading) setReading(false);
      return;
    }
    if (reading) return;
    const tag = event.target.tagName;
    const control = tag === 'BUTTON' || tag === 'SELECT' || tag === 'A' || tag === 'INPUT';
    if (control && (event.key === ' ' || event.key === 'Enter' || (tag === 'SELECT' && event.key.startsWith('Arrow')))) return;
    const actions = {
      ArrowRight: next, ArrowDown: next, PageDown: next, ' ': next, Enter: next,
      ArrowLeft: prev, ArrowUp: prev, PageUp: prev, Backspace: prev,
      Home: () => show({ slide: 0, step: 0 }, false),
      End: () => show({ slide: scenes.length - 1, step: scenes[scenes.length - 1].steps.length - 1 }, false),
      d: toggleDrawer, m: () => setStill(!root.classList.contains('still')), r: () => setReading(true),
      f: () => { if (document.fullscreenElement) document.exitFullscreen(); else if (root.requestFullscreen) root.requestFullscreen(); },
    };
    const action = actions[event.key];
    if (action) { event.preventDefault(); action(); }
  });
  fit();
  show(fromHash(), false);
  fit();
})();
`

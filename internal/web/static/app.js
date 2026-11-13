// Mondspace Landscape UI — zoomable C4-style views over the analysis API.
// Everything rendered from analysis data goes through textContent (never
// innerHTML): repository content is untrusted input.
(() => {
  'use strict';

  const KIND_LABEL = {
    person: 'Person', system: 'System', container: 'Container',
    component: 'Component', file: 'File', external: 'External system',
  };
  const LEVEL_LABEL = { '': 'Context', system: 'Containers', container: 'Components', component: 'Code' };
  const EDGE_KINDS = ['depends', 'http', 'data', 'uses', 'runtime'];
  const POLL_MS = 1000;

  // ---------- tiny DOM helpers ----------
  const $ = (sel) => document.querySelector(sel);
  function h(tag, attrs = {}, ...children) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const c of children.flat(Infinity)) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  // ---------- API ----------
  const api = {
    async json(url, opts) {
      const res = await fetch(url, opts);
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw Object.assign(new Error(body.error || res.statusText), { status: res.status });
      return body;
    },
    config: () => api.json('/api/config'),
    list: () => api.json('/api/analyses'),
    get: (id) => api.json(`/api/analyses/${encodeURIComponent(id)}`),
    view: (id, focus) => api.json(`/api/analyses/${encodeURIComponent(id)}/view?focus=${encodeURIComponent(focus || '')}`),
    graph: (id) => api.json(`/api/analyses/${encodeURIComponent(id)}/graph`),
    analyze: (source) => api.json('/api/analyses', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source }),
    }),
  };

  // ---------- state ----------
  const state = {
    job: null,          // current analysis job
    focus: '',          // focused node id ('' = top level)
    view: null,         // last view payload
    graph: null,        // full graph (lazy, for search & details)
    selected: null,     // {type:'node'|'edge', id}
    rankDir: 'TB',
    labels: null,       // relationship labels: true/false, or null = automatic
    hiddenKinds: new Set(),
    showTests: false,
    focusKind: '',      // kind of box highlighted from the legend ('' = none)
    pollTimer: null,
    viewVersion: '',
  };

  // ---------- routing (#/a/<id>/<focus>) ----------
  function route() {
    const m = location.hash.match(/^#\/a\/([^/]+)(?:\/(.*))?$/);
    return m ? { id: m[1], focus: m[2] ? decodeURIComponent(m[2]) : '' } : null;
  }
  function navigate(id, focus, replace) {
    const hash = `#/a/${id}` + (focus ? `/${encodeURIComponent(focus)}` : '');
    if (location.hash === hash) return;
    if (replace) history.replaceState(null, '', hash); else history.pushState(null, '', hash);
  }

  // ---------- theme-aware colors for cytoscape ----------
  function colors() {
    const cs = getComputedStyle(document.documentElement);
    const v = (n) => cs.getPropertyValue(n).trim();
    return {
      text: v('--text'), muted: v('--muted'), surface: v('--surface'), border: v('--border'),
      person: v('--n-person'), system: v('--n-system'), container: v('--n-container'),
      component: v('--n-component'), file: v('--n-file'), fileText: v('--n-file-text'),
      external: v('--n-external'), nodeText: v('--n-text'), accent: v('--accent'),
      depends: v('--e-depends'), http: v('--e-http'), data: v('--e-data'), uses: v('--e-uses'), runtime: v('--e-runtime'),
    };
  }

  function stylesheet(c) {
    return [
      { selector: 'node', style: {
        shape: 'round-rectangle', 'background-color': (n) => c[n.data('kind')] || c.container,
        width: 'data(w)', height: 'data(h)',
        label: 'data(label)', color: (n) => n.data('kind') === 'file' ? c.fileText : c.nodeText,
        'font-family': 'ui-sans-serif, -apple-system, Segoe UI, Inter, sans-serif',
        'font-size': 11, 'text-wrap': 'wrap', 'text-max-width': (n) => n.data('w') - 22,
        'text-valign': 'center', 'text-halign': 'center', 'line-height': 1.3,
        'border-width': 0, 'corner-radius': 10,
      } },
      // Each kind has its own shape as well as its own colour (see the legend).
      { selector: 'node[kind = "person"]', style: { shape: 'round-rectangle', 'corner-radius': 28 } },
      { selector: 'node[kind = "system"]', style: { 'corner-radius': 14 } },
      { selector: 'node[kind = "component"]', style: { 'corner-radius': 3 } },
      { selector: 'node[kind = "file"]', style: { shape: 'cut-rectangle', 'corner-radius': 0 } },
      { selector: 'node[kind = "external"]', style: { 'border-width': 1.5, 'border-style': 'dashed', 'border-color': c.surface } },
      { selector: 'node.context', style: { opacity: 0.78, 'border-width': 2, 'border-style': 'dashed', 'border-color': c.muted } },
      { selector: 'node.expandable', style: { 'border-width': 3, 'border-color': c.accent, 'border-style': 'double' } },
      { selector: 'node.context.expandable', style: { 'border-style': 'dashed', 'border-width': 2 } },
      // Zoomed out: name only, large enough to read (level of detail).
      { selector: 'node.compact', style: {
        label: 'data(short)', 'font-size': 'data(cfont)', 'font-weight': 600, 'text-max-width': (n) => n.data('w') - 18,
        'line-height': 1.15, 'text-overflow-wrap': 'data(cwrap)',
      } },
      { selector: 'node:selected', style: {
        'overlay-color': c.accent, 'overlay-opacity': 0.22, 'overlay-padding': 7, 'overlay-shape': 'round-rectangle', 'overlay-corner-radius': 14,
      } },
      { selector: 'node.match', style: { 'overlay-color': c.data, 'overlay-opacity': 0.4, 'overlay-padding': 9, 'overlay-shape': 'round-rectangle' } },
      { selector: 'node:active', style: { 'overlay-opacity': 0.1, 'overlay-shape': 'round-rectangle' } },
      { selector: 'node.faded, edge.faded', style: { opacity: 0.18 } },
      { selector: 'node.dim, edge.dim', style: { opacity: 0.25 } },
      // Highlight one kind of box (legend buttons, keys 1-6).
      { selector: 'node.kfade, edge.kfade', style: { opacity: 0.14 } },
      { selector: 'node.kfocus', style: {
        'underlay-color': (n) => c[n.data('kind')] || c.accent, 'underlay-opacity': 0.45, 'underlay-padding': 7,
        'underlay-shape': 'round-rectangle', 'underlay-corner-radius': 14,
      } },
      { selector: 'edge.lit', style: { width: (e) => e.data('width') + 1.5, 'z-index': 9, label: 'data(full)' } },
      { selector: 'edge.near', style: { label: 'data(full)', 'z-index': 9 } },
      { selector: 'edge', style: {
        width: 'data(width)', 'curve-style': 'bezier', 'line-color': (e) => c[e.data('kind')] || c.depends,
        'target-arrow-color': (e) => c[e.data('kind')] || c.depends, 'target-arrow-shape': 'triangle', 'arrow-scale': 0.9,
        label: 'data(label)', 'font-size': 10, 'min-zoomed-font-size': 6.5, color: c.muted,
        'text-background-color': c.surface, 'text-background-opacity': 0.9, 'text-background-padding': 2,
        'text-background-shape': 'roundrectangle', 'text-wrap': 'wrap', 'text-max-width': 180,
        'text-rotation': 'none',
      } },
      { selector: 'edge[kind = "runtime"]', style: { 'line-style': 'dashed' } },
      { selector: 'edge[kind = "depends"]', style: { 'line-style': 'solid' } },
      { selector: 'edge:selected', style: { width: (e) => e.data('width') + 2, 'z-index': 10, label: 'data(full)' } },
    ];
  }

  // ---------- cytoscape ----------
  const cy = cytoscape({
    container: $('#cy'), wheelSensitivity: 0.25, minZoom: 0.1, maxZoom: 3,
    boxSelectionEnabled: false, style: stylesheet(colors()),
  });
  window.__landscape = { cy }; // handy from the devtools console

  // Level of detail: below this zoom the descriptions are unreadable, so
  // boxes show just their name in a larger font.
  const COMPACT_BELOW = 0.62;
  let compact = null;
  function updateDetail() {
    const c = cy.zoom() < COMPACT_BELOW;
    if (c === compact && !cy.nodes().some((n) => n.hasClass('compact') !== c)) return;
    compact = c;
    cy.batch(() => cy.nodes().toggleClass('compact', c));
    $('#zoom-hint').hidden = !c || !cy.nodes().length;
  }
  cy.on('zoom', updateDetail);

  // Hover: highlight what a box talks to (unless something is selected).
  cy.on('mouseover', 'node', (evt) => {
    $('#cy').style.cursor = 'pointer';
    if (state.selected) return;
    const keep = evt.target.closedNeighborhood();
    cy.batch(() => {
      cy.elements().not(keep).addClass('dim');
      keep.edges().addClass('lit');
    });
  });
  cy.on('mouseout', 'node', () => {
    $('#cy').style.cursor = '';
    cy.batch(() => cy.elements().removeClass('dim lit'));
  });
  cy.on('mouseover', 'edge', () => { $('#cy').style.cursor = 'pointer'; });
  cy.on('mouseout', 'edge', () => { $('#cy').style.cursor = ''; });

  // Track whether the user moved the camera, so resizes can refit safely.
  let userMoved = false;
  cy.on('dragpan scrollzoom pinchzoom', () => { userMoved = true; });
  new ResizeObserver(() => {
    cy.resize();
    if (!userMoved && cy.nodes().length) fit(false);
  }).observe($('#cy'));
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => cy.style(stylesheet(colors())));

  function truncate(s, n) { return !s ? '' : (s.length > n ? s.slice(0, n - 1).trimEnd() + '…' : s); }

  function nodeLabel(n) {
    const kind = KIND_LABEL[n.kind] || n.kind;
    const meta = n.technology ? `[${kind}: ${truncate(n.technology, 38)}]` : `[${kind}]`;
    const lines = [n.name, meta];
    const desc = truncate(n.description, n.kind === 'file' ? 0 : 120);
    if (desc) lines.push('', desc);
    if (n.expandable) lines.push('', `⊕ ${n.children} inside`);
    return lines.join('\n');
  }

  // compactText picks the largest font (up to 24px) at which the longest
  // word of a name still fits the box, so zoomed-out names do not break
  // mid-word.
  function compactText(name, w) {
    const longest = Math.max(...name.split(/\s+/).map((x) => x.length), 1);
    const ideal = Math.floor((w - 24) / (0.66 * longest));
    const cfont = Math.max(17, Math.min(24, ideal));
    // Only break inside words (host names, paths) when they cannot fit.
    return { cfont, cwrap: ideal < 17 ? 'anywhere' : 'whitespace' };
  }

  function nodeSize(n) {
    const w = n.kind === 'file' ? 170 : n.kind === 'person' ? 180 : 230;
    const charsPerLine = Math.floor((w - 22) / 6.1);
    const desc = truncate(n.description, n.kind === 'file' ? 0 : 120);
    let lines = 2 + (n.name.length > charsPerLine ? 1 : 0);
    if (desc) lines += 1 + Math.ceil(desc.length / charsPerLine);
    if (n.expandable) lines += 2;
    return { w, h: Math.max(56, 22 + lines * 14.5) };
  }

  function edgeLabel(e) {
    const labels = e.labels || [];
    if (!labels.length) return '';
    const shown = labels.slice(0, 2).map((l) => truncate(l, 44)).join('\n');
    return labels.length > 2 ? `${shown}\n+${labels.length - 2} more` : shown;
  }

  // Busy views show relationship labels only on hover and selection, unless
  // the user switched them on or off explicitly.
  const AUTO_LABEL_EDGES = 14;
  function labelsOn(edgeCount) {
    return state.labels ?? edgeCount <= AUTO_LABEL_EDGES;
  }

  function primaryKind(kinds) {
    for (const k of ['http', 'data', 'runtime', 'uses', 'depends']) if (kinds.includes(k)) return k;
    return kinds[0] || 'depends';
  }

  function elements(view) {
    const isTest = (n) => (n.tags || []).includes('test');
    const visible = new Set((view.nodes || []).filter((n) => state.showTests || !isTest(n)).map((n) => n.id));
    const nodes = (view.nodes || []).filter((n) => visible.has(n.id)).map((n) => {
      const { w, h: ht } = nodeSize(n);
      return {
        group: 'nodes',
        data: {
          id: n.id, kind: n.kind, label: nodeLabel(n), short: n.expandable ? `${n.name} ⊕` : n.name,
          ...compactText(n.name, w), w, h: ht, raw: n,
        },
        classes: [n.context ? 'context' : '', n.expandable ? 'expandable' : ''].join(' '),
      };
    });
    const shown = (view.edges || [])
      .filter((e) => visible.has(e.from) && visible.has(e.to))
      .filter((e) => e.kinds.some((k) => !state.hiddenKinds.has(k)));
    const on = labelsOn(shown.length);
    const edges = shown.map((e) => {
      const full = edgeLabel(e);
      return {
        group: 'edges',
        data: {
          id: `${e.from}→${e.to}`, source: e.from, target: e.to, kind: primaryKind(e.kinds),
          label: on ? full : '', full, width: Math.min(1.2 + Math.log2(e.weight + 1), 6), raw: e,
        },
      };
    });
    return [...nodes, ...edges];
  }

  const MAX_FIT_ZOOM = 1.2; // small views should not be blown up

  // Room for the floating status pill (top) and legend (bottom).
  const FIT_PADDING = { top: 64, right: 32, bottom: 56, left: 32 };

  // fit shows the whole view, but never blows small views up beyond
  // MAX_FIT_ZOOM.
  function fit(animate) {
    const eles = cy.elements();
    if (!eles.length) return;
    const bb = eles.boundingBox();
    const p = FIT_PADDING;
    const w = Math.max(cy.width() - p.left - p.right, 50), ht = Math.max(cy.height() - p.top - p.bottom, 50);
    const z = Math.min(MAX_FIT_ZOOM, Math.max(cy.minZoom(), Math.min(w / bb.w, ht / bb.h)));
    const pan = { x: p.left + (w - z * (bb.x1 + bb.x2)) / 2, y: p.top + (ht - z * (bb.y1 + bb.y2)) / 2 };
    if (animate) cy.animate({ zoom: z, pan }, { duration: 220, complete: updateDetail });
    else { cy.viewport({ zoom: z, pan }); updateDetail(); }
  }

  // layout: dagre for ranks and crossings, then fold ranks that are too
  // wide for the screen into several rows (see layout.js), then fit.
  function layout(animate) {
    userMoved = false;
    const before = animate ? new Map(cy.nodes().map((n) => [n.id(), { ...n.position() }])) : null;
    cy.layout({
      name: 'dagre', rankDir: state.rankDir, nodeSep: 40, rankSep: 90, edgeSep: 16,
      ranker: 'network-simplex', animate: false, fit: false,
    }).run();
    const boxes = cy.nodes().map((n) => ({ id: n.id(), ...n.position(), w: n.data('w'), h: n.data('h') }));
    const aspect = Math.max(cy.width(), 1) / Math.max(cy.height(), 1);
    const placed = window.LandscapeLayout.wrapRanks(boxes, { dir: state.rankDir, aspect, gap: 40 });
    if (!before) {
      cy.batch(() => placed.forEach((p) => cy.getElementById(p.id).position({ x: p.x, y: p.y })));
      fit(false);
      return;
    }
    // Animate from the old positions to the new ones.
    cy.batch(() => placed.forEach((p) => cy.getElementById(p.id).position(before.get(p.id) || p)));
    placed.forEach((p) => cy.getElementById(p.id).animate({ position: { x: p.x, y: p.y } }, { duration: 250 }));
    setTimeout(() => fit(true), 260);
  }

  // Render a view. When only descriptions changed (same node/edge ids),
  // update data in place so the user's camera and positions are kept.
  function render(view, { relayout = true } = {}) {
    state.view = view;
    const els = elements(view);
    const ids = els.map((e) => e.data.id).sort().join('|');
    $('#empty').hidden = true;
    $('#progress').hidden = true;
    document.body.classList.remove('no-view');
    if (!relayout && ids === state.viewVersion) {
      for (const el of els) cy.getElementById(el.data.id).data(el.data);
      applyKinds();
      refreshPanel();
      return;
    }
    state.viewVersion = ids;
    cy.elements().remove();
    cy.add(els);
    compact = null;
    layout(false);
    syncLabelsButton();
    applyKinds();
    renderCrumbs(view);
    refreshPanel();
  }

  function renderCrumbs(view) {
    const crumbs = $('#crumbs');
    crumbs.replaceChildren(
      h('li', {}, h('a', { onclick: () => zoomTo('') }, 'Landscape')),
      ...(view.breadcrumb || []).map((b) => h('li', {}, h('a', { onclick: () => zoomTo(b.id), title: b.name }, b.name))),
    );
    const focusKind = view.focus ? view.focus.kind : '';
    $('#level').textContent = LEVEL_LABEL[focusKind] || 'Detail';
    $('#up').disabled = !view.focus;
  }

  // ---------- navigation ----------
  async function zoomTo(focus, { push = true } = {}) {
    if (!state.job) return;
    try {
      const view = await api.view(state.job.id, focus);
      state.focus = focus;
      state.selected = null;
      if (push) navigate(state.job.id, focus);
      render(view);
    } catch (err) {
      showStatus(err.message, 'error');
    }
  }

  function zoomOut() {
    const crumbs = (state.view && state.view.breadcrumb) || [];
    if (!crumbs.length) return;
    zoomTo(crumbs.length > 1 ? crumbs[crumbs.length - 2].id : '');
  }

  cy.on('dbltap', 'node', (evt) => {
    const n = evt.target.data('raw');
    if (n.expandable) zoomTo(n.id);
    else if (n.context) zoomTo(n.parent || '');
  });
  cy.on('tap', 'node', (evt) => select({ type: 'node', id: evt.target.id() }));
  cy.on('tap', 'edge', (evt) => select({ type: 'edge', id: evt.target.id() }));
  cy.on('tap', (evt) => { if (evt.target === cy) select(null); });

  // ---------- kinds of boxes (legend) ----------
  const KIND_KEYS = ['system', 'container', 'component', 'file', 'person', 'external'];
  const legendButtons = [...document.querySelectorAll('#legend .lg')];

  // applyKinds updates the legend counts for the current view and, when a
  // kind is focused, highlights its boxes and fades everything else.
  function applyKinds() {
    const counts = {};
    cy.nodes().forEach((n) => { counts[n.data('kind')] = (counts[n.data('kind')] || 0) + 1; });
    // A highlight only makes sense where that kind exists: moving to a level
    // without it clears it rather than leaving a pressed button and no boxes.
    if (state.focusKind && !counts[state.focusKind]) state.focusKind = '';
    for (const b of legendButtons) {
      const k = b.dataset.kind, n = counts[k] || 0, on = state.focusKind === k;
      b.querySelector('.n').textContent = n ? String(n) : '';
      b.setAttribute('aria-pressed', String(on));
      b.disabled = !n;
      b.dataset.tip = b.dataset.tip || b.title;
      b.title = n ? b.dataset.tip : `${b.dataset.tip}: none at this level`;
    }
    $('#legend').classList.toggle('focusing', !!state.focusKind);
    cy.batch(() => {
      cy.elements().removeClass('kfade kfocus');
      if (!state.focusKind) return;
      const hit = cy.nodes(`[kind = "${state.focusKind}"]`);
      hit.addClass('kfocus');
      cy.nodes().not(hit).addClass('kfade');
      cy.edges().filter((e) => !hit.contains(e.source()) && !hit.contains(e.target())).addClass('kfade');
    });
  }

  function plural(kind, n) {
    const label = (KIND_LABEL[kind] || kind).toLowerCase();
    if (n === 1) return label;
    return kind === 'person' ? 'people' : `${label}s`;
  }

  function focusKind(kind) {
    if (!cy.nodes(`[kind = "${kind}"]`).length) {
      showStatus(`No ${plural(kind, 0)} at this level`, 'info', 1800);
      return;
    }
    state.focusKind = state.focusKind === kind ? '' : kind;
    applyKinds();
    const n = cy.nodes(`[kind = "${kind}"]`).length;
    showStatus(state.focusKind ? `${n} ${plural(kind, n)} highlighted · Esc to clear` : '', 'info', 2200);
  }

  legendButtons.forEach((b) => b.addEventListener('click', () => focusKind(b.dataset.kind)));

  // ---------- selection & details panel ----------
  function select(sel, { reveal = false } = {}) {
    state.selected = sel;
    cy.elements().removeClass('faded dim lit near');
    cy.elements().unselect();
    if (sel) {
      const el = cy.getElementById(sel.id);
      el.select();
      const keep = sel.type === 'node' ? el.closedNeighborhood() : el.union(el.connectedNodes());
      cy.elements().not(keep).addClass('faded');
      keep.edges().addClass('near');
      if (reveal) revealElement(el);
    }
    refreshPanel();
  }

  // revealElement brings an element into view at a readable zoom when it
  // is off-screen or too small to read.
  function revealElement(el) {
    if (!el.length) return;
    const ext = cy.extent(), bb = el.boundingBox();
    const visible = bb.x1 >= ext.x1 && bb.x2 <= ext.x2 && bb.y1 >= ext.y1 && bb.y2 <= ext.y2;
    if (visible && cy.zoom() >= COMPACT_BELOW) return;
    userMoved = true;
    cy.animate({ center: { eles: el }, zoom: Math.max(cy.zoom(), 0.9) }, { duration: 250, complete: updateDetail });
  }

  function refreshPanel() {
    const panel = $('#panel');
    const sel = state.selected;
    if (!sel || !cy.getElementById(sel.id).length) {
      panel.replaceChildren(summaryPanel());
      return;
    }
    const el = cy.getElementById(sel.id);
    panel.replaceChildren(sel.type === 'node' ? nodePanel(el.data('raw')) : edgePanel(el.data('raw')));
  }

  function nameOf(id) {
    const n = state.view && state.view.nodes.find((x) => x.id === id);
    return n ? n.name : id;
  }

  // The panel with nothing selected explains the current view: what it is
  // (the repository at the top level, the focused box below it) and an
  // outline of every box in it, so the diagram can be explored from a list.
  function summaryPanel() {
    const job = state.job;
    if (!job || !job.summary) {
      return h('div', { class: 'panel-empty' }, job ? 'The overview appears here once the analysis is done.' : 'Analyze a repository and its overview appears here: size, languages and every box in the current view.');
    }
    const focus = state.view && state.view.focus;
    return h('div', {},
      focus ? focusHeader(focus) : repoHeader(job),
      outline(),
      h('p', { class: 'hint' }, 'Hover a box to see what it talks to; click it for details. Double-click a box with ⊕ to zoom in, Backspace to zoom out.'),
    );
  }

  function repoHeader(job) {
    const s = job.summary;
    const langs = Object.entries(s.languages || {}).sort((a, b) => b[1] - a[1]).slice(0, 8);
    return h('div', {},
      h('span', { class: 'badge system' }, 'Repository'),
      h('h2', {}, s.name),
      h('div', { class: 'tech mono' }, job.remote || job.dir || job.source),
      h('h3', {}, 'Size'),
      h('div', { class: 'metrics' },
        metric(s.files, 'files'), metric(s.lines.toLocaleString(), 'lines'), metric(s.containers, 'containers'),
        metric(s.components, 'components'), metric(s.externals, 'external'), metric(langs.length, 'languages')),
      h('h3', {}, 'Languages (files)'),
      h('div', { class: 'tags' }, langs.map(([l, n]) => h('span', { class: 'tag' }, `${l} ${n}`))),
      (s.warnings || []).length ? [h('h3', {}, 'Warnings'), h('ul', { class: 'list warnings' }, s.warnings.map((w) => h('li', {}, w)))] : null,
    );
  }

  function focusHeader(n) {
    const details = n.details || {};
    return h('div', {},
      h('span', { class: `badge ${n.kind}` }, `Inside this ${(KIND_LABEL[n.kind] || n.kind).toLowerCase()}`),
      h('h2', {}, n.name),
      n.technology ? h('div', { class: 'tech' }, n.technology) : null,
      n.description ? h('p', { class: 'desc' }, n.description, aiMark(details)) : null,
      n.path ? h('div', { class: 'mono' }, n.path) : null,
    );
  }

  function outline() {
    const nodes = ((state.view && state.view.nodes) || []).filter((n) => cy.getElementById(n.id).length);
    if (!nodes.length) return null;
    const order = { person: 0, system: 1, container: 2, component: 3, file: 4, external: 5 };
    const sorted = [...nodes].sort((a, b) => (!!a.context - !!b.context) || (order[a.kind] - order[b.kind]) || a.name.localeCompare(b.name));
    const inside = sorted.filter((n) => !n.context && n.kind !== 'external' && n.kind !== 'person');
    const around = sorted.filter((n) => n.context || n.kind === 'external' || n.kind === 'person');
    const item = (n) => h('li', {
      class: n.context ? 'context' : '', title: n.description || n.name,
      onclick: () => select({ type: 'node', id: n.id }, { reveal: true }),
    },
    h('span', { class: `dot ${n.kind}` }),
    h('span', { class: 'name' }, n.name, ' ', h('span', { class: 'sub' }, n.technology ? truncate(n.technology, 28) : (KIND_LABEL[n.kind] || n.kind))),
    n.expandable ? h('button', {
      class: 'zoom', title: `Zoom into ${n.name}`,
      onclick: (ev) => { ev.stopPropagation(); zoomTo(n.id); },
    }, `⊕ ${n.children}`) : h('span'));
    return [
      inside.length ? [h('h3', {}, `In this view (${inside.length})`), h('ul', { class: 'outline' }, inside.map(item))] : null,
      around.length ? [h('h3', {}, `Around it (${around.length})`), h('ul', { class: 'outline' }, around.map(item))] : null,
    ];
  }

  function aiMark(details) {
    return details.ai ? h('span', { class: 'ai', title: 'Written by a local model' }, `✦ ${details.ai}`) : null;
  }

  function metric(v, label) { return h('div', { class: 'metric' }, h('b', {}, v), h('span', {}, label)); }

  function nodePanel(n) {
    const details = n.details || {};
    const conns = (state.view ? state.view.edges : []).filter((e) => e.from === n.id || e.to === n.id);
    const m = n.metrics || {};
    return h('div', {},
      h('span', { class: `badge ${n.kind}` }, KIND_LABEL[n.kind] || n.kind),
      n.context ? h('span', { class: 'ai' }, 'outside this view') : null,
      h('h2', {}, n.name),
      n.technology ? h('div', { class: 'tech' }, n.technology) : null,
      n.description ? h('p', { class: 'desc' }, n.description, aiMark(details)) : h('p', { class: 'desc panel-empty' }, 'No description.'),
      details.doc && details.doc !== n.description ? h('p', { class: 'doc' }, h('span', { class: 'doc-label' }, 'From the code'), details.doc) : null,
      n.path ? h('div', { class: 'mono' }, n.path) : null,
      Object.keys(m).length ? [h('h3', {}, 'Metrics'), h('div', { class: 'metrics' }, Object.entries(m).map(([k, v]) => metric(v.toLocaleString(), k)))] : null,
      (n.tags || []).length ? [h('h3', {}, 'Tags'), h('div', { class: 'tags' }, n.tags.map((t) => h('span', { class: 'tag' }, t)))] : null,
      details.deploy ? [h('h3', {}, 'Deployment'), h('div', {}, details.deploy)] : null,
      details.routes ? [h('h3', {}, `HTTP routes served (${details.routes.split('\n').length})`), collapsible('list mono', details.routes.split('\n').map((r) => h('li', {}, r)))] : null,
      details.symbols ? [h('h3', {}, 'Exported symbols'), h('div', { class: 'mono' }, details.symbols)] : null,
      conns.length ? [h('h3', {}, `Connections in this view (${conns.length})`), collapsible('list', conns.map((e) => connItem(e, n.id)))] : null,
      h('div', { class: 'actions' },
        n.expandable ? h('button', { class: 'primary', onclick: () => zoomTo(n.id) }, `Zoom in (${n.children})`) : null,
        n.context && n.parent !== undefined ? h('button', { onclick: () => zoomTo(n.parent || '') }, 'Go to its level') : null,
        h('button', { onclick: () => copy(referenceFor(n)) }, 'Copy reference'),
      ),
    );
  }

  // collapsible renders a list showing the first few items and a button to
  // reveal the rest, so long route lists do not bury everything below.
  function collapsible(cls, items, max = 8) {
    const ul = h('ul', { class: cls }, items.slice(0, max));
    if (items.length <= max) return ul;
    const more = h('button', { class: 'more', onclick: () => { ul.append(...items.slice(max)); more.remove(); } },
      `Show all ${items.length}`);
    return [ul, more];
  }

  function connItem(e, selfId) {
    const out = e.from === selfId;
    const other = out ? e.to : e.from;
    return h('li', { class: 'link', onclick: () => select({ type: 'edge', id: `${e.from}→${e.to}` }) },
      e.kinds.map((k) => h('span', { class: `conn-kind ${k}` }, k)),
      out ? `→ ${nameOf(other)}` : `← ${nameOf(other)}`,
      e.labels && e.labels.length ? h('span', { class: 'conn-label' }, truncate(e.labels.join(' · '), 160)) : null,
    );
  }

  function edgePanel(e) {
    return h('div', {},
      h('span', { class: 'badge' }, 'Relationship'),
      h('h2', {}, `${nameOf(e.from)} → ${nameOf(e.to)}`),
      h('div', { class: 'tags' }, e.kinds.map((k) => h('span', { class: `conn-kind ${k}` }, k))),
      h('h3', {}, 'Strength'),
      h('div', {}, `${e.weight} underlying relationship${e.weight === 1 ? '' : 's'}`),
      e.labels && e.labels.length ? [h('h3', {}, 'Details'), collapsible('list mono', e.labels.map((l) => h('li', {}, l)))] : null,
      h('div', { class: 'actions' },
        h('button', { onclick: () => select({ type: 'node', id: e.from }) }, `Source: ${truncate(nameOf(e.from), 22)}`),
        h('button', { onclick: () => select({ type: 'node', id: e.to }) }, `Target: ${truncate(nameOf(e.to), 22)}`),
      ),
    );
  }

  function referenceFor(n) {
    const path = n.path ? ` (${n.path})` : '';
    return `${KIND_LABEL[n.kind] || n.kind} "${n.name}"${path}${n.description ? ' — ' + n.description : ''}`;
  }

  async function copy(text) {
    try { await navigator.clipboard.writeText(text); showStatus('Copied to clipboard', 'info', 1500); }
    catch { showStatus('Clipboard unavailable', 'error', 2000); }
  }

  // ---------- status ----------
  let statusTimer = null;
  function showStatus(text, type = 'info', ttl = 0, spinner = false) {
    const el = $('#status');
    clearTimeout(statusTimer);
    if (!text) { el.hidden = true; return; }
    el.className = `status ${type}`;
    el.replaceChildren(spinner ? h('span', { class: 'spinner' }) : '', text);
    el.hidden = false;
    if (ttl) statusTimer = setTimeout(() => { el.hidden = true; }, ttl);
  }

  // Before the first diagram exists, progress is shown as a card in the
  // middle of the canvas; afterwards as the small status pill.
  function showProgress(job) {
    const remote = !!job.remote || /github/.test(job.source || '');
    const steps = [
      [remote ? 'Fetch the repository' : 'Open the repository', ['queued', 'resolving']],
      ['Analyze the code', ['analyzing']],
      ['Draw the diagram', []],
    ];
    const at = steps.findIndex(([, st]) => st.includes(job.status));
    $('#empty').hidden = true;
    $('#progress').hidden = false;
    $('#progress-title').textContent = statusText(job) || 'Working…';
    $('#progress-source').textContent = job.remote || job.dir || job.source || '';
    $('#progress-steps').replaceChildren(...steps.map(([label], i) =>
      h('li', { class: i < at ? 'done' : i === at ? 'now' : '' }, label)));
  }

  function statusText(job) {
    switch (job.status) {
      case 'queued': case 'resolving': return job.remote || /github/.test(job.source) ? 'Fetching repository…' : 'Opening repository…';
      case 'analyzing': return 'Analyzing code…';
      case 'enriching': {
        const p = job.progress;
        return p ? `Writing descriptions with ${job.llm} — ${p.stage} ${p.done}/${p.total}` : `Writing descriptions with ${job.llm}…`;
      }
      default: return '';
    }
  }

  // ---------- job lifecycle ----------
  async function openJob(id, focus = '', { push = false } = {}) {
    clearTimeout(state.pollTimer);
    state.graph = null;
    state.viewVersion = '';
    try {
      state.job = await api.get(id);
    } catch (err) {
      showStatus(`Analysis not found (${err.message})`, 'error', 4000);
      return;
    }
    $('#source').value = state.job.remote || state.job.dir || state.job.source;
    if (push) navigate(id, focus); else navigate(id, focus, true);
    await poll(focus);
  }

  async function poll(focus) {
    const job = state.job = await api.get(state.job.id).catch(() => state.job);
    if (job.dir || job.remote) $('#source').value = job.remote || job.dir;
    if (job.status === 'failed') {
      $('#progress').hidden = true;
      if (!state.view) $('#empty').hidden = false;
      showStatus(`Analysis failed: ${job.error}`, 'error');
      $('#analyze-btn').disabled = false;
      return;
    }
    const hasModel = job.status === 'enriching' || job.status === 'ready';
    if (hasModel) {
      const firstRender = !state.view || state.viewVersion === '';
      try {
        const view = await api.view(job.id, firstRender ? focus : state.focus);
        if (firstRender) state.focus = focus;
        render(view, { relayout: firstRender });
      } catch (err) {
        if (err.status === 404 && focus) { await zoomTo('', { push: false }); } else { showStatus(err.message, 'error'); }
      }
    }
    if (job.status === 'ready') {
      showStatus(job.llm ? `Ready · descriptions by ${job.llm}` : 'Ready', 'info', 2500);
      $('#analyze-btn').disabled = false;
      refreshPanel();
      loadHistory();
      return;
    }
    if (state.view) showStatus(statusText(job), 'info', 0, true);
    else { showStatus(''); showProgress(job); }
    state.pollTimer = setTimeout(() => poll(focus), job.status === 'enriching' ? POLL_MS * 2 : POLL_MS);
  }

  $('#analyze-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const source = $('#source').value.trim();
    if (!source) { $('#source').focus(); return; }
    $('#analyze-btn').disabled = true;
    clearDiagram();
    try {
      const job = await api.analyze(source);
      await openJob(job.id, '', { push: true });
    } catch (err) {
      showStatus(err.message, 'error', 5000);
      $('#analyze-btn').disabled = false;
    }
  });

  document.querySelectorAll('[data-example]').forEach((b) => b.addEventListener('click', () => {
    $('#source').value = b.dataset.example;
    $('#analyze-form').requestSubmit();
  }));

  function clearDiagram() {
    state.view = null;
    state.selected = null;
    state.viewVersion = '';
    cy.elements().remove();
    $('#zoom-hint').hidden = true;
  }

  async function loadHistory() {
    const jobs = await api.list().catch(() => []);
    const sel = $('#history');
    sel.replaceChildren(h('option', { value: '' }, 'Recent…'),
      ...jobs.map((j) => h('option', { value: j.id }, `${j.summary ? j.summary.name : j.source} · ${j.status}`)));
  }
  $('#history').addEventListener('change', (ev) => {
    const id = ev.target.value;
    ev.target.value = '';
    if (id) { clearDiagram(); openJob(id, '', { push: true }); }
  });

  // ---------- search (whole graph) ----------
  let searchActive = -1;
  async function ensureGraph() {
    if (!state.graph && state.job) state.graph = await api.graph(state.job.id).catch(() => null);
    return state.graph;
  }
  $('#search').addEventListener('input', async (ev) => {
    const q = ev.target.value.trim().toLowerCase();
    const list = $('#search-results');
    if (!q) { list.hidden = true; return; }
    const g = await ensureGraph();
    if (!g) return;
    const hits = g.nodes.filter((n) => n.name.toLowerCase().includes(q) || (n.path || '').toLowerCase().includes(q)
      || (n.description || '').toLowerCase().includes(q)).slice(0, 30);
    searchActive = -1;
    list.replaceChildren(...hits.map((n) => h('li', { role: 'option', onclick: () => jumpTo(n) },
      h('span', {}, highlight(n.name, q)),
      h('span', { class: 'k' }, h('span', { class: `dot ${n.kind}` }), `${KIND_LABEL[n.kind] || n.kind}${n.path ? ' · ' + n.path : ''}`))));
    if (!hits.length) list.replaceChildren(h('li', { class: 'none' }, `Nothing matches “${q}”`));
    list.hidden = false;
  });
  // highlight wraps the first match of q in <mark> (text nodes only).
  function highlight(text, q) {
    const i = text.toLowerCase().indexOf(q);
    if (i < 0) return text;
    return [text.slice(0, i), h('mark', {}, text.slice(i, i + q.length)), text.slice(i + q.length)];
  }

  $('#search').addEventListener('keydown', (ev) => {
    const items = [...$('#search-results').children].filter((li) => !li.classList.contains('none'));
    if (!items.length) return;
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      searchActive = (searchActive + (ev.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
      items.forEach((li, i) => li.classList.toggle('active', i === searchActive));
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      (items[searchActive] || items[0]).click();
    } else if (ev.key === 'Escape') {
      $('#search-results').hidden = true;
    }
  });
  document.addEventListener('click', (ev) => { if (!ev.target.closest('.search')) $('#search-results').hidden = true; });

  async function jumpTo(n) {
    $('#search-results').hidden = true;
    $('#search').value = '';
    $('#search').blur(); // hand the keyboard back to the diagram
    await zoomTo(n.parent || '');
    const el = cy.getElementById(n.id);
    if (el.length) {
      select({ type: 'node', id: n.id }, { reveal: true });
      el.addClass('match');
      setTimeout(() => el.removeClass('match'), 1600);
    }
  }

  // ---------- toolbar ----------
  $('#up').addEventListener('click', zoomOut);
  $('#fit').addEventListener('click', () => fit(true));
  function toggleDir() {
    state.rankDir = state.rankDir === 'TB' ? 'LR' : 'TB';
    const lr = state.rankDir === 'LR';
    $('#dir').classList.toggle('lr', lr);
    $('#dir').title = `Layout: ${lr ? 'left to right' : 'top to bottom'} (D to switch)`;
    if (cy.nodes().length) layout(true);
  }
  function toggleLabels() {
    state.labels = !labelsOn(cy.edges().length);
    syncLabelsButton();
    cy.batch(() => cy.edges().forEach((e) => e.data('label', state.labels ? e.data('full') : '')));
  }
  function syncLabelsButton() {
    const on = labelsOn(cy.edges().length);
    $('#labels').setAttribute('aria-pressed', String(on));
    $('#labels').title = `Relationship labels: ${on ? 'shown' : 'on hover'}${state.labels === null ? ' (automatic)' : ''} (L)`;
  }
  $('#dir').addEventListener('click', toggleDir);
  $('#labels').addEventListener('click', toggleLabels);
  $('#help').addEventListener('click', () => $('#help-dialog').showModal());
  document.querySelectorAll('.chips input').forEach((cb) => cb.addEventListener('change', () => {
    state.hiddenKinds = new Set([...document.querySelectorAll('.chips input[data-kind]')].filter((x) => !x.checked).map((x) => x.dataset.kind));
    state.showTests = $('#tests').checked;
    if (state.view) { state.viewVersion = ''; render(state.view); }
  }));
  $('#png').addEventListener('click', () => {
    if (!cy.elements().length) return;
    const bg = getComputedStyle(document.body).backgroundColor;
    const a = h('a', { href: cy.png({ full: true, scale: 2, bg }), download: `${(state.job && state.job.summary && state.job.summary.name) || 'landscape'}${state.focus ? '-' + state.focus.replace(/[^a-z0-9]+/gi, '_') : ''}.png` });
    a.click();
  });
  document.addEventListener('keydown', (ev) => {
    if (ev.target.closest('input, select, textarea, dialog')) return;
    if (ev.key === 'Escape') {
      if (state.selected) select(null);
      else if (state.focusKind) focusKind(state.focusKind);
      return;
    }
    if (ev.key === 'Backspace') { ev.preventDefault(); zoomOut(); return; }
    if (ev.metaKey || ev.ctrlKey || ev.altKey) return;
    if (ev.key === 'f' || ev.key === 'F') fit(true);
    if (ev.key === 'l' || ev.key === 'L') toggleLabels();
    if (ev.key === 'd' || ev.key === 'D') toggleDir();
    if (ev.key === '?') $('#help-dialog').showModal();
    if (/^[1-6]$/.test(ev.key) && cy.nodes().length) focusKind(KIND_KEYS[Number(ev.key) - 1]);
    if (ev.key === 'Enter' && state.selected && state.selected.type === 'node') {
      const n = cy.getElementById(state.selected.id).data('raw');
      if (n && n.expandable) zoomTo(n.id);
    }
    if (ev.key === '/') { ev.preventDefault(); $('#search').focus(); }
  });

  window.addEventListener('popstate', () => {
    const r = route();
    if (!r) return;
    if (state.job && r.id === state.job.id) zoomTo(r.focus, { push: false });
    else { state.view = null; openJob(r.id, r.focus); }
  });

  // ---------- boot ----------
  (async function boot() {
    const cfg = await api.config().catch(() => ({}));
    const pill = $('#llm');
    pill.textContent = cfg.llm ? `AI: ${cfg.llm}` : 'AI off';
    pill.classList.toggle('on', !!cfg.llm);
    pill.title = cfg.llm ? 'Descriptions are written by this local model' : 'No local model found (start Ollama or LM Studio); static analysis only';
    loadHistory();
    const r = route();
    if (r) openJob(r.id, r.focus);
    else if (cfg.initial) openJob(cfg.initial, '');
    else $('#source').focus();
  })();
})();

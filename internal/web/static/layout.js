// Pure layout helpers for the landscape UI (no DOM, no Cytoscape), so they
// can be unit tested with `node --test 'internal/web/jstest/*.test.mjs'`.
(() => {
  'use strict';

  // wrapRanks post-processes a layered (dagre) layout. A layered layout puts
  // every node of a rank on one line, so a hub with 20 neighbours becomes a
  // strip 20 boxes wide that only fits the screen at an unreadable zoom.
  // Ranks wider than the viewport shape allows are folded into several
  // rows (TB) or columns (LR), keeping their order, and the ranks after them
  // are shifted to make room.
  //
  // nodes: [{id, x, y, w, h}] (centre positions); opts: {dir, aspect, gap}.
  // Returns [{id, x, y}] in input order.
  function wrapRanks(nodes, { dir = 'TB', aspect = 1.6, gap = 40 } = {}) {
    const tb = dir !== 'LR';
    const main = tb ? 'x' : 'y', cross = tb ? 'y' : 'x';
    const mainSize = tb ? 'w' : 'h', crossSize = tb ? 'h' : 'w';
    const pos = new Map(nodes.map((n) => [n.id, { id: n.id, x: n.x, y: n.y }]));
    if (nodes.length < 2) return [...pos.values()];

    const area = nodes.reduce((s, n) => s + (n.w + gap) * (n.h + gap), 0);
    const ideal = Math.sqrt(area * (tb ? aspect : 1 / aspect)) * 1.25;
    const widest = Math.max(...nodes.map((n) => n[mainSize]));
    // Never fold a rank of six boxes or fewer: folding costs readability
    // (edges run behind boxes), so it only pays off for really wide ranks.
    const limit = Math.max(ideal, 6 * widest + 5 * gap);

    const ranks = new Map();
    for (const n of nodes) {
      const key = Math.round(n[cross]);
      if (!ranks.has(key)) ranks.set(key, []);
      ranks.get(key).push(n);
    }

    let shift = 0;
    for (const key of [...ranks.keys()].sort((a, b) => a - b)) {
      const rank = ranks.get(key).sort((a, b) => a[main] - b[main]);
      const span = (ns) => ns.reduce((s, n) => s + n[mainSize], 0) + gap * (ns.length - 1);
      if (span(rank) <= limit) {
        for (const n of rank) pos.get(n.id)[cross] = n[cross] + shift;
        continue;
      }
      const rows = [[]];
      for (const n of rank) {
        const row = rows[rows.length - 1];
        if (row.length && span([...row, n]) > limit) rows.push([n]); else row.push(n);
      }
      const centre = (Math.min(...rank.map((n) => n[main] - n[mainSize] / 2))
        + Math.max(...rank.map((n) => n[main] + n[mainSize] / 2))) / 2;
      const step = Math.max(...rank.map((n) => n[crossSize])) + gap;
      const slot = rank.reduce((s, n) => s + n[mainSize], 0) / rank.length + gap;
      rows.forEach((row, i) => {
        // Stagger odd rows by half a box so edges from above can pass
        // through the gaps of the rows in front of them.
        let at = centre - span(row) / 2 + (i % 2 ? slot / 2 : 0);
        for (const n of row) {
          const p = pos.get(n.id);
          p[main] = at + n[mainSize] / 2;
          p[cross] = key + shift + i * step;
          at += n[mainSize] + gap;
        }
      });
      shift += (rows.length - 1) * step;
    }
    return nodes.map((n) => pos.get(n.id));
  }

  // overlaps counts pairs of boxes that intersect.
  function overlaps(nodes) {
    let count = 0;
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        if (Math.abs(a.x - b.x) < (a.w + b.w) / 2 && Math.abs(a.y - b.y) < (a.h + b.h) / 2) count++;
      }
    }
    return count;
  }

  globalThis.LandscapeLayout = { wrapRanks, overlaps };
})();

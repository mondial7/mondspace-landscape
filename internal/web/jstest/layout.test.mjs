// Unit tests for the pure layout helpers of the UI: make test-js
import { test } from 'node:test';
import assert from 'node:assert/strict';

await import('../static/layout.js');
const { wrapRanks, overlaps } = globalThis.LandscapeLayout;

const box = (id, x, y, w = 200, h = 60) => ({ id, x, y, w, h });

// One hub with many leaves on the rank below: the classic context view.
function star(n) {
  const nodes = [box('hub', 0, 0)];
  for (let i = 0; i < n; i++) nodes.push(box(`leaf${i}`, (i - (n - 1) / 2) * 240, 150));
  return nodes;
}

const bounds = (nodes) => {
  const xs = nodes.flatMap((n) => [n.x - n.w / 2, n.x + n.w / 2]);
  const ys = nodes.flatMap((n) => [n.y - n.h / 2, n.y + n.h / 2]);
  return { w: Math.max(...xs) - Math.min(...xs), h: Math.max(...ys) - Math.min(...ys) };
};

test('a narrow layout is left alone', () => {
  const nodes = star(3);
  const out = wrapRanks(nodes, { dir: 'TB', aspect: 1.6, gap: 40 });
  assert.deepEqual(out, nodes.map(({ id, x, y }) => ({ id, x, y })));
});

test('a very wide rank is wrapped into rows that fit the viewport shape', () => {
  const nodes = star(18);
  const before = bounds(nodes);
  const out = wrapRanks(nodes, { dir: 'TB', aspect: 1.6, gap: 40 });
  const moved = nodes.map((n) => ({ ...n, ...out.find((o) => o.id === n.id) }));
  const after = bounds(moved);
  assert.ok(after.w < before.w / 2, `width ${after.w} should shrink from ${before.w}`);
  const rows = new Set(moved.filter((n) => n.id !== 'hub').map((n) => n.y));
  assert.ok(rows.size >= 3, `leaves should be spread over several rows, got ${rows.size}`);
  assert.ok(after.w / after.h < 4, `aspect ${after.w / after.h} should be closer to the viewport`);
  assert.equal(overlaps(moved), 0);
});

test('wrapping keeps the left-to-right order of a rank (crossing minimisation)', () => {
  const nodes = star(12);
  const out = wrapRanks(nodes, { dir: 'TB', aspect: 1, gap: 40 });
  const leaves = out.filter((o) => o.id !== 'hub').sort((a, b) => a.y - b.y || a.x - b.x);
  assert.deepEqual(leaves.map((l) => l.id), nodes.slice(1).map((n) => n.id));
});

test('ranks below a wrapped rank are pushed down so nothing overlaps', () => {
  const nodes = star(16);
  nodes.push(box('below', 0, 300));
  const out = wrapRanks(nodes, { dir: 'TB', aspect: 1.6, gap: 40 });
  const moved = nodes.map((n) => ({ ...n, ...out.find((o) => o.id === n.id) }));
  const lowestLeaf = Math.max(...moved.filter((n) => n.id.startsWith('leaf')).map((n) => n.y));
  assert.ok(moved.find((n) => n.id === 'below').y > lowestLeaf, 'node below must stay below the wrapped rows');
  assert.equal(overlaps(moved), 0);
});

test('left-to-right layouts wrap columns instead of rows', () => {
  const nodes = star(18).map((n) => ({ ...n, x: n.y * 2, y: n.x / 2 }));
  const out = wrapRanks(nodes, { dir: 'LR', aspect: 1.6, gap: 40 });
  const moved = nodes.map((n) => ({ ...n, ...out.find((o) => o.id === n.id) }));
  const cols = new Set(moved.filter((n) => n.id !== 'hub').map((n) => n.x));
  assert.ok(cols.size >= 3, `leaves should be spread over several columns, got ${cols.size}`);
  assert.equal(overlaps(moved), 0);
});

test('a handful of boxes is never wrapped, even on a tall screen', () => {
  const nodes = star(5);
  const out = wrapRanks(nodes, { dir: 'TB', aspect: 0.6, gap: 40 });
  assert.equal(new Set(out.filter((o) => o.id !== 'hub').map((o) => o.y)).size, 1);
});

test('wrapped rows are staggered so edges from above pass between boxes', () => {
  const out = wrapRanks(star(18), { dir: 'TB', aspect: 1.6, gap: 40 });
  const rows = new Map();
  for (const o of out.filter((o) => o.id !== 'hub')) rows.set(o.y, [...(rows.get(o.y) || []), o.x]);
  const [first, second] = [...rows.keys()].sort((a, b) => a - b).map((y) => Math.min(...rows.get(y)));
  assert.notEqual(first, second, 'consecutive rows should not start at the same x');
});

test('overlaps counts intersecting boxes', () => {
  assert.equal(overlaps([box('a', 0, 0), box('b', 100, 0)]), 1);
  assert.equal(overlaps([box('a', 0, 0), box('b', 300, 0)]), 0);
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fmtTime } from '../static/live.js';

test('fmtTime', () => {
  assert.equal(fmtTime(0), '0:00');
  assert.equal(fmtTime(59.9), '0:59');
  assert.equal(fmtTime(375), '6:15');
  assert.equal(fmtTime(3723), '1:02:03');
  assert.equal(fmtTime(-1), '');
  assert.equal(fmtTime(NaN), '');
});

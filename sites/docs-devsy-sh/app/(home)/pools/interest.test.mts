import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseInterestMode } from './interest.ts';

test('the form is on when the variable is unset or empty', () => {
  assert.equal(parseInterestMode(undefined), 'on');
  assert.equal(parseInterestMode(''), 'on');
});

test('off, false and 0 close the form in any case', () => {
  for (const value of ['off', 'OFF', 'Off', 'false', 'FALSE', 'False', '0', ' off ']) {
    assert.equal(parseInterestMode(value), 'off', value);
  }
});

test('other values leave the form on', () => {
  for (const value of ['on', 'true', '1', 'preview', 'no']) {
    assert.equal(parseInterestMode(value), 'on', value);
  }
});

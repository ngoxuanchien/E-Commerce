import { describe, expect, test } from 'vitest';
import { formatDate, formatDateTime, formatVnd } from './format';

const NBSP = ' ';

describe('formatVnd', () => {
  test.each([
    [45_000, `45.000${NBSP}₫`],
    [0, `0${NBSP}₫`],
    [1_000_000_000, `1.000.000.000${NBSP}₫`],
  ])('formats %d as VND', (value, expected) => {
    expect(formatVnd(value)).toBe(expected);
  });

  test('renders missing prices as an em dash, never NaN', () => {
    expect(formatVnd(null)).toBe('—');
    expect(formatVnd(undefined)).toBe('—');
  });
});

describe('dates', () => {
  test('formats in Vietnam time (UTC+7)', () => {
    const text = formatDateTime('2026-09-28T10:00:00Z');
    expect(text).toContain('17:00');
    expect(text).toMatch(/28\/0?9\/(20)?26/);
    expect(formatDate('2026-09-28T20:00:00Z')).toMatch(/29\/0?9\/(20)?26/);
  });
});

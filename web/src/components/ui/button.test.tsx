import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { Button } from './button';

test('renders a primary button with the design token classes', () => {
  render(<Button>Mua ngay</Button>);
  const button = screen.getByRole('button', { name: 'Mua ngay' });
  expect(button).toHaveClass('bg-primary');
});

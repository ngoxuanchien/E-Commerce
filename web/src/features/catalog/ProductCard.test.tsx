import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { expect, test } from 'vitest';
import { ProductCard } from './ProductCard';

const base = { id: 'p1', name: 'Bình gốm lớn', shop: { id: 's1', name: 'Gốm Cô Bình', slug: 'gom' } };

test('links to the product, shows the price and the shop', () => {
  render(<MemoryRouter><ProductCard product={{ ...base, image_url: 'https://img.example/p1.png', min_price: 1_000_000_000 }} /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Bình gốm lớn' })).toHaveAttribute('href', '/products/p1');
  expect(screen.getByText('1.000.000.000 ₫')).toBeInTheDocument();
  expect(screen.getByText('Gốm Cô Bình')).toBeInTheDocument();
});

test('shows a placeholder when the product has no image', () => {
  render(<MemoryRouter><ProductCard product={{ ...base, image_url: null, min_price: 0 }} /></MemoryRouter>);
  expect(screen.getByText('Chưa có ảnh')).toBeInTheDocument();
  expect(screen.getByText('0 ₫')).toBeInTheDocument();
});

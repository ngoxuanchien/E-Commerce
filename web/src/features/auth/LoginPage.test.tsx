import { screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { server } from '@/test/server';
import { currentPath, renderApp } from '@/test/render';

async function submitLogin(user: ReturnType<typeof renderApp>['user'], email: string, password: string) {
  await user.type(await screen.findByLabelText('Email'), email);
  await user.type(screen.getByLabelText('Mật khẩu'), password);
  await user.click(screen.getByRole('button', { name: 'Đăng nhập' }));
}

test('logs in and goes to the safe next path', async () => {
  const { user } = renderApp('/login?next=%2Forders%3Fstatus%3DSHIPPING');
  await submitLogin(user, 'an@example.com', 'matkhau123');
  await screen.findByRole('button', { name: 'An Nguyễn' });
  expect(currentPath()).toBe('/orders?status=SHIPPING');
});

test.each(['%2F%2Fevil.example', 'https%3A%2F%2Fevil.example', '%2F%5Cevil.example'])('ignores an off-site next=%s', async (next) => {
  const { user } = renderApp(`/login?next=${next}`);
  await submitLogin(user, 'an@example.com', 'matkhau123');
  await screen.findByRole('button', { name: 'An Nguyễn' });
  expect(currentPath()).toBe('/');
});

test('wrong password shows a message and never calls refresh', async () => {
  const refreshes: string[] = [];
  server.events.on('request:start', ({ request }) => {
    if (request.url.endsWith('/api/auth/refresh')) refreshes.push(request.url);
  });
  const { user } = renderApp('/login');
  await screen.findByLabelText('Email');
  const afterMount = refreshes.length; // the on-load session restore
  await submitLogin(user, 'an@example.com', 'sai-mat-khau');
  expect(await screen.findByText('Email hoặc mật khẩu không đúng.')).toBeInTheDocument();
  expect(refreshes.length).toBe(afterMount);
});

test('empty fields are validated on the client', async () => {
  const { user } = renderApp('/login');
  await user.click(await screen.findByRole('button', { name: 'Đăng nhập' }));
  expect(await screen.findByText('Vui lòng nhập email')).toBeInTheDocument();
  expect(screen.getByText('Vui lòng nhập mật khẩu')).toBeInTheDocument();
  expect(screen.getByLabelText('Email')).toHaveAttribute('aria-invalid', 'true');
});

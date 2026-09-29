import { screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { currentPath, renderApp } from '@/test/render';

async function fill(user: ReturnType<typeof renderApp>['user'], values: { name: string; email: string; password: string; confirm: string }) {
  await user.type(await screen.findByLabelText('Họ và tên'), values.name);
  await user.type(screen.getByLabelText('Email'), values.email);
  await user.type(screen.getByLabelText('Mật khẩu'), values.password);
  await user.type(screen.getByLabelText('Nhập lại mật khẩu'), values.confirm);
  await user.click(screen.getByRole('button', { name: 'Tạo tài khoản' }));
}

test('registers, then asks the user to log in with the email prefilled (register does not log in)', async () => {
  const { user } = renderApp('/register?next=%2Fcart');
  await fill(user, { name: 'Minh Phạm', email: 'minh@example.com', password: 'matkhau123', confirm: 'matkhau123' });
  expect(await screen.findByText('Tạo tài khoản thành công. Mời bạn đăng nhập.')).toBeInTheDocument();
  expect(currentPath()).toBe('/login?next=%2Fcart');
  expect(screen.getByLabelText('Email')).toHaveValue('minh@example.com');
});

test('mismatched passwords and short passwords are rejected on the client', async () => {
  const { user } = renderApp('/register');
  await fill(user, { name: 'Minh', email: 'minh@example.com', password: 'ngan', confirm: 'khac' });
  expect(await screen.findByText('Mật khẩu cần ít nhất 8 ký tự')).toBeInTheDocument();
  expect(screen.getByText('Mật khẩu nhập lại không khớp')).toBeInTheDocument();
});

test('an existing email is reported on the email field', async () => {
  const { user } = renderApp('/register');
  await fill(user, { name: 'An', email: 'an@example.com', password: 'matkhau123', confirm: 'matkhau123' });
  expect(await screen.findByText('Email này đã được đăng ký.')).toBeInTheDocument();
});

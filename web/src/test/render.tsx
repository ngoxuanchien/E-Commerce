import { QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { type InitialEntry, MemoryRouter, useLocation } from 'react-router';
import { Toaster } from 'sonner';
import { createQueryClient } from '@/app/query-client';
import { AuthProvider } from '@/features/auth/AuthProvider';
import { getDb } from '@/mocks/state';
import { AppRoutes } from '@/routes/AppRoutes';

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location" hidden>
      {location.pathname + location.search}
    </output>
  );
}

/** Simulates a valid refresh cookie for a seeded user; AuthProvider restores it on mount. */
export function signIn(email: string): void {
  const user = getDb().state.users.find((u) => u.email === email);
  if (!user) throw new Error(`no seeded user ${email}`);
  getDb().state.session = user.id;
}

export const currentPath = () => screen.getByTestId('location').textContent;

export function renderApp(path: InitialEntry, options: { as?: string } = {}) {
  if (options.as) signIn(options.as);
  const queryClient = createQueryClient({ retry: false });
  // applyAccept: false so tests can hand <input accept> files it would reject and check our own validation.
  const user = userEvent.setup({ applyAccept: false });
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, user, queryClient };
}

export function renderWithProviders(ui: ReactElement, options: { path?: string; as?: string } = {}) {
  if (options.as) signIn(options.as);
  const queryClient = createQueryClient({ retry: false });
  const user = userEvent.setup();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[options.path ?? '/']}>
        <AuthProvider>
          {ui}
          <LocationProbe />
          <Toaster />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, user, queryClient };
}

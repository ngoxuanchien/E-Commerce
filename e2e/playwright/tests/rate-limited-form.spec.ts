import { test } from '@playwright/test';
import { ApiUser, PASSWORD, runId } from '../support/api';
import { loginViaUi } from '../support/ui';

// The gateway allows 10 logins per minute per client. The form helpers must
// wait out a 429 like a patient user instead of failing, so the suite stays
// green however the bucket is shared (see finding F1).
test('the login form waits out a drained login bucket instead of failing', async ({ page }) => {
  const run = runId();
  const user = await ApiUser.register(`Khách ${run}`, `e2e-${run}-ratelimit@example.test`);

  for (let i = 0; i < 12; i++) {
    const res = await page.request.post('/api/auth/login', { data: { email: 'nobody@example.test', password: PASSWORD } });
    if (res.status() === 429) break;
  }

  await loginViaUi(page, user.account);
  await user.dispose();
});

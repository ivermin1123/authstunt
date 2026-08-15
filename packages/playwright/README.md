# @authstunt/playwright

Playwright fixtures for [AuthStunt](https://github.com/ivermin1123/authstunt):
**every worker and every test gets its own identity, and they do not step on
each other.**

That sentence is the whole design. Playwright's unit of parallelism is the
worker, so the fixtures map onto it directly:

- **`run` is worker-scoped.** Each worker opens one run when it starts and
  ends it when it finishes. A worker that runs twenty tests in sequence opens
  one run and leases twenty identities from it - that is the intended shape,
  not a cache. The project bearer is used here and only here; it never
  reaches a test.
- **`lease` is test-scoped.** Each test leases one identity from its worker's
  run and releases it at teardown, **including when the test fails** - which
  is exactly when a leaked identity would otherwise be held until the run
  expires.

## Install

```sh
npm i -D @authstunt/playwright @authstunt/client
```

`@playwright/test` and `@authstunt/client` are peer dependencies. The client
is a peer rather than a bundled copy so your suite has exactly one of it: a
`ClaimError` thrown by a fixture is then the same class you catch with
`instanceof` in your own code.

## Use

```ts
import { test, expect } from '@authstunt/playwright'

test('signup', async ({ page, lease }) => {
  await page.fill('#email', lease.addr)
  await page.click('#submit')          // your app sends the mail
  const { value: otp } = await lease.claim('email_otp', { timeoutMs: 15_000 })
  await page.fill('#code', otp)
  await expect(page.getByText('Welcome')).toBeVisible()
})
```

`claim` long-polls: it returns as soon as the code is claimable, so there is
nothing to poll or sleep on. It is bound to one message, so a resend does not
turn into two tests reading the same code.

## Composing with your own fixtures

A test file runs under exactly one `test` object. If your suite already has
its own - almost every real suite does - importing ours would throw yours
away. Extend yours instead:

```ts
import { authstuntFixtures } from '@authstunt/playwright'
import { test as myTest } from './my-fixtures'

export const test = myTest.extend(authstuntFixtures)
```

With several fixture sets, `mergeTests` from `@playwright/test` combines them
without nesting `.extend` calls.

Both routes are the same object: the `test` this package exports is one line
built from `authstuntFixtures` and holds no logic of its own, so the front
door and the composed route cannot drift apart.

## Configuration

`authstuntUrl` and `authstuntBearer` are Playwright *option* fixtures. They
default to `AUTHSTUNT_URL` and `AUTHSTUNT_BEARER`, and a config `use` block
overrides them:

```ts
// playwright.config.ts
import { defineConfig } from '@playwright/test'

export default defineConfig({
  use: {
    authstuntUrl: 'http://127.0.0.1:8925',
    leaseRole: 'checkout',
  },
})
```

| Option | Default | Scope |
| --- | --- | --- |
| `authstuntUrl` | `process.env.AUTHSTUNT_URL` | worker |
| `authstuntBearer` | `process.env.AUTHSTUNT_BEARER` | worker |
| `leaseRole` | `'signup'` | test |

Leave the bearer in the environment. Nothing in this package prints it -
including the error you get when it is missing, which says it is missing and
does not echo what it found.

## License

MIT.

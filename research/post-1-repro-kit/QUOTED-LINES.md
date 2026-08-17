# Every line quoted in the post

**All repository lines read 2026-08-16. All vendor documentation read 2026-08-16.**

Line numbers are what was true at the listed SHA. To check one:

```
https://github.com/<owner>/<repo>/blob/<sha>/<path>#L<line>
```

The `ledger` column points at the entry in `docs/verified-artifacts.md` that carries the
verification status and, where an earlier draft got something wrong, the correction note.

---

## Repository lines

### `stytchauth/stytch-browser` - the one repository that consumes a real code
SHA `4bceacb5c73bb4b6c75ed90eba9471b505490089`

| Quoted | path:line | ledger |
|---|---|---|
| `` const email = `${emailName}+${timestamp.getTime()}@${MAILOSAUR_SERVER_ID}.mailosaur.net`; `` | `services/e2e-tests/cypress/e2e/react-demo.cy.ts:62` | 1.6 |
| `cy.mailosaurGetMessage(` | `services/e2e-tests/cypress/e2e/react-demo.cy.ts:68` | 1.7 |
| `const tokenLink = email.text.links[0].href;` | `services/e2e-tests/cypress/e2e/react-demo.cy.ts:81` | 1.8 |
| `cy.visit(tokenLink);` | `services/e2e-tests/cypress/e2e/react-demo.cy.ts:83` | 1.9 |
| `"cypress-mailosaur": "5.0.0",` | `services/e2e-tests/package.json:10` | 1.11 |
| `cypress_mailosaur_api_key: ${{ secrets.CYPRESS_MAILOSAUR_API_KEY }}` | `.github/workflows/on-pr.yml:33` | 1.12 |
| "the entire E2E suite is three spec files" - `react-b2b-ui.cy.ts`, `react-demo.cy.ts`, `react-ui.cy.ts` (plus `utils.ts`) | `services/e2e-tests/cypress/e2e/` | 1.13 |

Quoted in the post with indentation stripped for inline use; the source lines are indented inside
their test bodies. No words were removed.

### `yravan/cashlens` - storage state and an account pool, still pinned to one worker
SHA `adf5e78c1cde745c3c359d7de6228604c57caaa9`

| Quoted | path:line | ledger |
|---|---|---|
| `  workers: 1, // parallel clerk.signIn is flaky (clerk/javascript#7891)` | `apps/web/playwright.config.ts:27` | 1.1 |
| `  { key: "a", email: E2E_USER_A_EMAIL, storageState: STORAGE_STATE_A },` and the `"b"` line below it | `apps/web/e2e/global.setup.ts:16-17` | 1.2 (path corrected, note N-1) |

### `clerk/clerk-playwright-nextjs` - the vendor's own example uses the vendor's backdoor
SHA `858d186ca6b4854e1d8bb16c5384b75ba7f1ac30`

| Quoted | path:line | ledger |
|---|---|---|
| `// Unique email per run so concurrent runs don't collide.` | `e2e/app.spec.ts:35` | 1.5 |
| `// Uses +clerk_test so 424242 works as the verification code.` | `e2e/app.spec.ts:36` | note N-4 |

Both lines are quoted together on purpose. Quoting line 35 alone reads as collision avoidance
against real inboxes; line 36 is what makes it a backdoor.

### `woody34/rescope` - the substitution, stated in a comment
SHA `5cd773fd54ea768f29e72e752c672e5b1ba25a2e`

| Quoted | path:line | ledger |
|---|---|---|
| `// In real apps this would come from the email inbox.` | `apps/platform-tests/tests/02-otp-authentication.scenario.test.ts:34` | 1.4 (line corrected, note N-3) |

The post paraphrases the line above it rather than quoting it. Verbatim, line 33 reads
`// Step 2: Read the code the emulator generated via the escape-hatch API.`

### Self-built backdoors - forged credentials

| Repo | Quoted | path:line | SHA | ledger |
|---|---|---|---|---|
| `Luigi-Faldetta/fit-log` | `win.localStorage.setItem('clerk-db-jwt', 'mock-jwt-token');` | `client/cypress/support/commands.js:12` | `9e1dcec731578de4a4aa10e9fb05e5e7f36b4917` | 1.15 |
| `tensr-xyz/tensr-platform-web` | `name: 'stytch_session_token',` / `value: 'e2e-playwright-session',` (inside `page.context().addCookies([...])`, function `seedE2eSession`) | `tests/fixtures/e2e-auth.ts:13-14` | `6482dd0910fa8e46efe89ad12bfe74e4161f85bd` | 1.16 |
| `tensr-xyz/tensr-platform-web` | `localStorage.setItem('stytch_session_token', 'e2e-playwright-session');` | `tests/fixtures/e2e-auth.ts:26` | same | 1.16b |
| `tensr-xyz/tensr-platform-web` | `const SESSION_TOKEN_KEY = 'stytch_session_token';` - the product code reading the injected key | `src/utils/auth.ts:3` (also at `src/proxy.ts:33`) | same | 1.16c |
| `SDG-AI-Lab/Digital_Technologies_Radar` | `// Set logged in state` / `window.localStorage.setItem('drr-current-user-id', 'admin');` | `cypress/e2e/create-disaster.cy.ts:2-4` | `1dcf308577a0617690b8548412e2b0a3ccc0babf` | §2b delta table |

### Self-built backdoors - environment switches

The post quotes the full variable names on purpose. Shortened forms such as a bare `BYPASS_AUTH` or
`SKIP_AUTH` occur in none of the 284 (note N-6).

| Repo | Quoted | path:line | SHA | ledger |
|---|---|---|---|---|
| `vandean25/auto-core-platform` | `    command: 'cross-env VITE_E2E_SKIP_AUTH=true vite --port 5174 --strictPort',` | `apps/core-web/playwright.config.ts:37` | `2ecd3d409a72a17169a8eb0a8ea8546b720535f6` | 1.18 |
| `gumacahin/mis-capstone` | `VITE_E2E_BYPASS_AUTH` - named only. In the source it sits inside a longer `webServer.command` string; the post does not reproduce it as a standalone command. | `ui/playwright.config.ts:64` | `de3a62cc82ba49ed3b07f6945ba0f1420bff4f2e` | 1.17 |
| `RoleModel/betanxt-issuer-portal` | `NEXT_PUBLIC_BYPASS_AUTH=true`. The post names the variable and says it sits in a committed `.env`, without naming the repository; it is named here. | `issuer-portal/.env:11` | `afe92dab95291fe4be2cec63f6cdab3ee6f126be` | 1.20 |

### The thirty-fifth repository, found only by re-deriving the count on 2026-08-17

| Repo | Quoted / referenced | path:line | SHA |
|---|---|---|---|
| `sefi-uzan/yanshuf-ai` | a hardcoded `next-auth.session-token` JWT (value not reproduced here) defined as `userCookies`, then injected with `addCookies(userCookies)` | `tests/e2e/fixtures/config.ts:8-13`; injection at `tests/e2e/pages/website.ts:13` | `26638529ef61e906e8a61d24edc8d1176d5023d3` |

Missed by both earlier passes for the same structural reason as `kil-dev/kil.dev`: the argument to
`addCookies` is a variable, so no string literal sits beside the call for a regex to match. It is
not in `docs/verified-artifacts.md`, because it was found after that ledger was written. Its
provenance is `outputs/selfbuilt-bypass-v2.txt` and `outputs/divergence-classification.txt`.

### The three repositories the earlier keyword list missed

| Repo | Quoted / referenced | path:line | SHA |
|---|---|---|---|
| `ubcdiscovery/ubc-discovery` | `sessionStorage.setItem("ubc-discovery-test-google-user", ...)`; and the product code reading it, `const TEST_GOOGLE_USER_KEY = "ubc-discovery-test-google-user";` | `web/e2e/identity-convergence.spec.ts:104-110`; `web/app/lib/firebase.ts:22` | `ef6a28f9252285b47f6a93f61b963a433f96f947` |
| `kil-dev/kil.dev` | `export const ADMIN_TEST_BYPASS_COOKIE = 'pet-gallery-test-admin'` and its value; imported by the spec rather than written as a literal | `src/lib/admin-test-bypass.ts:1-2`; import at `tests/e2e/pages/admin-ask-kilian.spec.ts:2` | `441e8a5edb0980a2a8f67a91b11144438027a380` |
| `SDG-AI-Lab/Digital_Technologies_Radar` | see the forged-credentials table above | `cypress/e2e/create-disaster.cy.ts:2-4` | `1dcf308577a0617690b8548412e2b0a3ccc0babf` |

### The false positive the post discloses

| Repo | Quoted | path:line | SHA |
|---|---|---|---|
| `MCPJam/inspector` | `sessionStorage.setItem("oauth-debugger-e2e-started", "true")` - a run marker, not a credential. Matched only because "oauth" contains "auth". Excluded from the count. A second false positive, `QRun-IO/qqq-frontend-next`, was removed differently - by deleting the `MOCK|FAKE` token that matched it, see outputs/pattern-freeze.txt change 5. | `mcpjam-inspector/e2e/oauth-debugger.spec.ts:255` | `dd212decbd6db8b50348c9a543307928c7986857` |

### The three that say they gave up

| Repo | Quoted | path:line | SHA | ledger |
|---|---|---|---|---|
| `drifter089/orgOS` | the full `test.skip` block, all four numbered items | `tests/auth-unauthenticated.spec.ts:117-127` | `a9073690f340168f3b3b65e7ad57ba2390d4a047` | 1.3, 1.23, appendix A-1 |
| `intelogroup/ugent` | `* NOTE: Full OTP login requires a real email. Tests that need auth are` / `* marked with [auth-required] and skip if UGENT_TEST_OTP is not set.` | `e2e/auth-and-routes.spec.ts:8-9` | `0724ab13ef962e98147c2263b189e27cabe6ee05` | 1.22 |
| `amirrudd/flyerboard` | Named in the post but not quoted; the post states only that its blocker is Descope **SMS** OTP, not email (note N-7). Verbatim: `* SCOPE: the whole e2e suite runs unauthenticated — auth is Descope SMS OTP,` / `* which cannot be automated here.` | `e2e/messages.spec.ts:6-7` | `32a22a7c1f7f4a7bae15cdce659aacf1914ff7ea` | 1.21 |

The orgOS block is quoted in full. An earlier internal draft cut it after item 2, which made it read
as a dead end; item 3 names Mailinator and Mailtrap, so the developer who gave up knew commercial
inboxes existed. See appendix A-1 in the ledger.

### `microsoft/playwright` documentation
SHA `d5a185a894ab3ab17ff77a44e116a1339c6bdaed`

| Quoted | path:line | ledger |
|---|---|---|
| "This is the recommended approach for tests that modify server-side state. ... We will need multiple testing accounts, one per each parallel worker." | `docs/src/auth.md:135` | 6.4 |
| `    const account = await acquireAccount(id);` - called twice, never defined anywhere on the page | `docs/src/auth.md:178` and `:383` | 6.1, 6.2, 6.3 |

The post marks its elision in the first quote and says what was elided. Verbatim and unelided, with
the source's own emphasis: "This is the **recommended** approach for tests that **modify server-side
state**. In Playwright, worker processes run in parallel. In this approach, each parallel worker is
authenticated once. All tests ran by worker are reusing the same authentication state. We will need
multiple testing accounts, one per each parallel worker."

The four sample-code comment lines at `docs/src/auth.md:174-177` (repeated at `379-382`, ledger 6.5)
are paraphrased in the post, not quoted. Verbatim: `// Acquire a unique account, for example create
a new one.` / `// Alternatively, you can have a list of precreated accounts for testing.` /
`// Make sure that accounts are unique, so that multiple team members` / `// can run tests at the
same time without interference.`

The post does not claim Playwright instructs you to implement `acquireAccount` yourself; the
documentation contains no such sentence (ledger §6).

### Repositories in the download audit

The post gives the two truncation figures without naming the repositories. They are
`nathanjohnpayne/friends-and-family-billing` (36 of 272 files on the first fetch) and
`samayhuf-star/Adiology-23Dec-New` (14 of 865). Both refetched; neither had a hit on either signal.
No line is quoted from either. Ledger §2.

---

## Vendor documentation

No SHA applies. Each page was read on **2026-08-16**; vendors edit documentation without notice.

| Quoted | Source | ledger |
|---|---|---|
| "Any email with the `+clerk_test` subaddress is a test email address." | clerk.com/docs/guides/development/testing/test-emails-and-phones | 3.1 |
| "When testing email verification codes, no email with the verification code will be sent." | same | 3.2 (quoted in full, note N-9) |
| the fixed code `424242` | same | 3.3 |
| "However, this is highly discouraged." (on enabling test mode on a production instance) | same | 3.4 |
| "Setup must be run serially, this is necessary if Playwright is configured to run fully parallel" | clerk.com/docs/guides/development/testing/playwright/overview | 3.5 |
| `setup.describe.configure({ mode: 'serial' })` | same | 3.6 |
| "This is an insecure method and is only recommended when generated OTP codes are not viable for testing." | docs.descope.com/test-users - the callout on **Static OTP codes** | 3.7 (scope corrected, note N-10) |
| "Utilizing test users, you can generate OTP codes and Magic/Enchanted link tokens ... without sending actual communications to the test account." | docs.descope.com/test-users | 3.8 |
| "The best practice is never to visit or test third-party sites over which you have no control." | auth0.com/blog/end-to-end-testing-with-cypress-and-auth0/ | 3.9 |
| "Keep in mind that you must not use this grant on your public clients. This is an exception to this rule because it is an end-to-end test that won't be used by real users." | same | 3.10 |
| "When you provide the fictional phone number and send the verification code, no actual SMS is sent." | firebase.google.com/docs/auth/web/phone-auth | 3.11 (verbatim replaces an earlier paraphrase, note N-11) |
| "If your API credentials and the request format are correct you will receive a 200 status response, but no email will actually be sent." | stytch.com/docs/guides/testing/sandbox-values | 3.15 (URL corrected, note N-12) |
| "The sandbox values below are only available when calling the Stytch API directly. They will not work when used with a frontend or mobile Stytch SDK." | same | 3.16 |
| sandbox OTP `000000`, sandbox phone `+10000000000` | same | 3.17 |
| "we generally recommend using a platform like Mailosaur to set up a programmatically accessible email or SMS inbox" | stytch.com/docs/b2b/guides/testing/e2e-testing | 3.18 |
| "Kinde requires OTP email verification when signing up for a new user." | docs.kinde.com/testing/testing-authentication-flows/ | 3.21 (URL made specific, note N-13) |
| "Test email services like Mailosaur or Mailtrap provide API access to test inboxes, making it easy to retrieve OTP codes programmatically." | docs.kinde.com/testing/testing-passwordless-flows/ | 3.22 |
| `auth.email.enable_confirmations` defaulting to `false` in local development, alongside a bundled `inbucket` | supabase.com/docs/guides/local-development/cli/config | note N-15 |
| SuperTokens: testing documentation covers API testing with Postman, debug logs and troubleshooting, with no test mode and no fixed OTP | note N-15 | N-15 |

### `clerk/javascript` issue 7891

Read 2026-08-16 via `api.github.com/repos/clerk/javascript/issues/7891`.

| Quoted / stated | ledger |
|---|---|
| "When using `@clerk/testing` with Playwright and `--workers=2` (or more), all tests fail with `TimeoutError: page.waitForFunction: Timeout 15000ms exceeded` at `clerk.signIn()`. With `--workers=1`, authentication works 100% reliably." (issue body) | 3.28 |
| closed 2026-04-03, `state_reason: completed`, closed by `jacekradko` | 3.25 |
| exactly two comments, both by `m13v`, both `author_association: NONE` - so no comment from the vendor | 3.26 |
| "One practical workaround while waiting for a proper fix: pre-authenticate each worker with a dedicated test user and save the storage state to separate JSON files, then load each worker's state from its own file rather than calling signIn() concurrently." (comment, 2026-03-23T21:26:01Z) | 3.27 |

On that last one: it is the **second** suggestion in that comment and its author labels it a
stopgap. The primary suggestion in the same comment is a separate browser profile directory per
worker plus a file lock. Reporting it as "the community fix is one account per worker" promotes a
stopgap to a solution. See note N-16.

---

## Cleared for publication, present in the ledger, not used in the post

These were verified to the same standard and cut for length. They are listed so that the ledger and
this manifest do not look like they disagree.

| Source | Material | path:line / URL | SHA | ledger |
|---|---|---|---|---|
| `Eliahhango/ai-assistant` | forged WorkOS cookie: `name: "wos-session",` / `value: "mock-session-token",` in `mockWorkOSLogin` | `e2e/helpers/mock-handlers.ts:64-65` | `fcfcc58dc283e2147265e22d6ce9886158236aa5` | 1.14 |
| `dawi369/assistant-mk1` | `WORKBENCH_ALLOW_LOCAL_DEV_IDENTITY=true WORKBENCH_DEV_USER_ID=e2e-owner` inside `webServer.command` | `playwright.config.ts:16` | `5c459abe244ffbd5ccb850966593e47349d21490` | 1.19 |
| WorkOS | "The staging environment now includes a Test SSO page and comes with a pre-configured Test Organization with a WorkOS Test Identity Provider." | workos.com/changelog/test-sso | - | 3.13 |
| WorkOS | "When creating test users in automated tests, use email addresses on reserved example domains such as `user@example.com`." | workos.com/docs/authkit/environments | - | 3.14 |
| Dynamic | "Test Accounts allow you to log in with a static OTP"; triggered by a `+dynamic_test` subaddress or a `(555)` area code | docs.dynamic.xyz/developer-dashboard/test-accounts | - | 3.19, 3.20 |
| Firebase | "Run consecutive tests with the same phone number without getting throttled." | firebase.google.com/docs/auth/web/phone-auth | - | 3.12 |
| Clerk | the fixed code `424242` as a standalone claim (it appears in the post only inside the quoted comment from `clerk/clerk-playwright-nextjs`) | clerk.com/docs/.../test-emails-and-phones | - | 3.3 |
| `stytchauth/stytch-browser` | `cy.get('button[name="stytch.magicLinks.authenticate()"]').should('have.length', 1).click();` | `services/e2e-tests/cypress/e2e/react-demo.cy.ts:86` | `4bceacb5c73bb4b6c75ed90eba9471b505490089` | 1.10 |

## What is quoted in the post but is not in this kit

Nothing. Every quotation in the post appears above, and every entry above appears in
`docs/verified-artifacts.md`.

One entry in the ledger is marked **not found** and is banned from publication: a sentence
attributed to `supabase/supabase-js` `docs/TESTING.md` that does not exist in that file. It is
recorded in the ledger, section 7 and note N-14, so that the error stays visible rather than being
quietly deleted.

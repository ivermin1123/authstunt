# Demo app

A signup flow with an emailed six digit code, in one file of Express and
nodemailer. No database: pending signups live in a Map, because what this
app is here to show is the email round trip.

It is the application under test for the AuthStunt quickstart, and the
stage for the resend scene: asking for a new code retires the old one, so
a test that assumes "the first code I saw still works" fails here the way
it would fail against a real product.

## Run it

Three commands, from this directory. The first starts AuthStunt, which is
both the SMTP server this app sends to and the API the test claims from.

```sh
go run ../../cmd/authstunt serve --project demo --domain '*.demo.test'
npm install
npm start
```

Then open <http://127.0.0.1:3000/signup>. Mail goes to `SMTP_HOST:SMTP_PORT`,
which defaults to `127.0.0.1:1025` - the address `authstunt serve` listens
on. `PORT` moves the app off 3000.

Codes are printed nowhere. To read one, claim it: that is the point.

## Test it

```sh
npm test
```

`playwright test` starts its own AuthStunt on ephemeral ports and runs this
app in the test process, so nothing above has to be running. To point the
suite at a server you already have, set `AUTHSTUNT_URL`, `AUTHSTUNT_BEARER`
and the `SMTP_HOST`/`SMTP_PORT` that server listens on, and nothing is
started for you.

The suite needs a browser once:

```sh
npx playwright install --with-deps chromium
```

## What the test does not do

It never reads a mailbox, never sleeps waiting for delivery, and never
learns what code the app generated. It leases an address, the app mails a
code to it, and `lease.claim` returns when the server can hand over the
value bound to that lease - or a reason code saying why it cannot.

The resend case claims twice on one lease with two different idempotency
keys. One key would replay the first answer, which is what a retry wants;
two keys is how a test says "I want the next message, not the last one".

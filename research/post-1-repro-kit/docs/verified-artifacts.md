# Verified artifacts — sổ trích dẫn được phép lên bài

**Ngày kiểm: 2026-08-16** · Phiên XÁC MINH (không nghiên cứu mới) · Người kiểm: session verify
**Trạng thái repo lúc kiểm:** `authstunt` = `882b0d06` · `authstunt-internal` = `132c7b92` ·
`authstunt-dogfood-local` = `f5756e45`

---

## 0. LUẬT — đọc trước khi viết bài

> ### ⚠️ LUẬT SỐ 0 — `evidence/` xác nhận được SỰ CÓ MẶT, không bao giờ xác nhận được SỰ VẮNG MẶT.
> **Mọi kết luận "không tìm thấy" phải kiểm trên repo thật, không kiểm trên `evidence/`.**
>
> `df4/evidence/` là bản trích theo grep của `03-measure.sh`, không phải bản sao repo. Thứ script
> đó không quét thì không có trong đó, dù repo có.
>
> **Ca thật, xảy ra trong chính phiên xác minh này:** tìm `stytch_session_token` trong toàn bộ
> `df4/` (evidence + data + round2 + regex của `10-selfbuilt-bypass.py`) ra **0 lần**, và tôi đã
> ghi nó là *"không tìm thấy, cấm dùng"*. Sai. Tải lại 284 repo và quét **mã nguồn thật** thì nó
> có mặt, đúng hình dạng đã mô tả: `tensr-xyz/tensr-platform-web`
> `tests/fixtures/e2e-auth.ts:13-14, 26` (xem 1.16). Suýt loại bỏ một bằng chứng đúng.

1. **Chỉ được trích từ file này.** Không trích từ `00-synthesis.md`, `05-roadmap.md`,
   `03-parallel-reality.md`, `02-idp-vendor-scan.md` hay bất kỳ báo cáo nghiên cứu nào.
   Cái gì không có trong file này thì không được lên bài.
2. **Trạng thái `đã lệch` nghĩa là: dùng được nhưng phải sửa theo cột "Bản đúng".** Không được
   bê nguyên câu trong báo cáo cũ.
3. **Trạng thái `không tìm thấy` nghĩa là: cấm dùng dưới mọi hình thức**, kể cả diễn giải lại.
4. Mọi `path:line` trong file này là **số dòng thật lúc mở tay ngày 2026-08-16**, kèm SHA của
   commit HEAD tại thời điểm kiểm. Ngày đo DF-4 và ngày kiểm này **trùng nhau**, và 284/284 repo
   vẫn còn tồn tại ⇒ **không có ca "repo đã đổi so với ngày đo"**. Đối chiếu mẫu với `df4/evidence/`
   (cashlens, stytch-browser) khớp từng ký tự.

---

## 1. Artifact repo công khai

| # | Trích nguyên văn | Nguồn | path:line | SHA (lúc kiểm) | Trạng thái |
|---|---|---|---|---|---|
| 1.1 | `  workers: 1, // parallel clerk.signIn is flaky (clerk/javascript#7891)` | `yravan/cashlens` | `apps/web/playwright.config.ts:27` | `adf5e78c1cde745c3c359d7de6228604c57caaa9` | **khớp** |
| 1.2 | `  { key: "a", email: E2E_USER_A_EMAIL, storageState: STORAGE_STATE_A },` / `  { key: "b", email: E2E_USER_B_EMAIL, storageState: STORAGE_STATE_B },` | `yravan/cashlens` | `apps/web/e2e/global.setup.ts:16-17` | `adf5e78c…` | **đã lệch** (đường dẫn) — xem N-1 |
| 1.3 | `// which cannot be automated in this test environment without access to the email inbox.` | `drifter089/orgOS` | `tests/auth-unauthenticated.spec.ts:121` | `a9073690f340168f3b3b65e7ad57ba2390d4a047` | **đã lệch** (dải dòng + cắt cụt) — xem N-2, A-1 |
| 1.4 | `    // In real apps this would come from the email inbox.` | `woody34/rescope` | `apps/platform-tests/tests/02-otp-authentication.scenario.test.ts:34` | `5cd773fd54ea768f29e72e752c672e5b1ba25a2e` | **đã lệch** (số dòng) — xem N-3 |
| 1.5 | `    // Unique email per run so concurrent runs don't collide.` | `clerk/clerk-playwright-nextjs` | `e2e/app.spec.ts:35` | `858d186ca6b4854e1d8bb16c5384b75ba7f1ac30` | **đã lệch** (số dòng) — xem N-4 |
| 1.6 | `` const email = `${emailName}+${timestamp.getTime()}@${MAILOSAUR_SERVER_ID}.mailosaur.net`; `` | `stytchauth/stytch-browser` | `services/e2e-tests/cypress/e2e/react-demo.cy.ts:62` | `4bceacb5c73bb4b6c75ed90eba9471b505490089` | **khớp** |
| 1.7 | `    cy.mailosaurGetMessage(` | `stytchauth/stytch-browser` | `…/react-demo.cy.ts:68` | `4bceacb5…` | **khớp** |
| 1.8 | `      const tokenLink = email.text.links[0].href;` | `stytchauth/stytch-browser` | `…/react-demo.cy.ts:81` | `4bceacb5…` | **khớp** |
| 1.9 | `      cy.visit(tokenLink);` | `stytchauth/stytch-browser` | `…/react-demo.cy.ts:83` | `4bceacb5…` | **khớp** |
| 1.10 | `    cy.get('button[name="stytch.magicLinks.authenticate()"]').should('have.length', 1).click();` | `stytchauth/stytch-browser` | `…/react-demo.cy.ts:86` | `4bceacb5…` | **khớp** |
| 1.11 | `    "cypress-mailosaur": "5.0.0",` | `stytchauth/stytch-browser` | `services/e2e-tests/package.json:10` | `4bceacb5…` | **khớp** |
| 1.12 | `      cypress_mailosaur_api_key: ${{ secrets.CYPRESS_MAILOSAUR_API_KEY }}` | `stytchauth/stytch-browser` | `.github/workflows/on-pr.yml:33` | `4bceacb5…` | **khớp** |
| 1.13 | Suite E2E gồm đúng **3 file spec**: `react-b2b-ui.cy.ts` · `react-demo.cy.ts` · `react-ui.cy.ts` (+ `utils.ts`) | `stytchauth/stytch-browser` | `services/e2e-tests/cypress/e2e/` | `4bceacb5…` | **khớp** |

### Mẫu backdoor tự dựng — nêu đích danh

| # | Trích nguyên văn | Nguồn | path:line | SHA (lúc kiểm) | Trạng thái |
|---|---|---|---|---|---|
| 1.14 | `        name: "wos-session",` / `        value: "mock-session-token",` (trong `page.context().addCookies([...])`, hàm `mockWorkOSLogin`) | `Eliahhango/ai-assistant` | `e2e/helpers/mock-handlers.ts:64-65` | `fcfcc58dc283e2147265e22d6ce9886158236aa5` | **khớp** |
| 1.15 | `    win.localStorage.setItem('clerk-db-jwt', 'mock-jwt-token');` | `Luigi-Faldetta/fit-log` | `client/cypress/support/commands.js:12` | `9e1dcec731578de4a4aa10e9fb05e5e7f36b4917` | **khớp** |
| 1.16 | `      name: 'stytch_session_token',` / `      value: 'e2e-playwright-session',` (trong `page.context().addCookies([...])`, hàm `seedE2eSession`) | `tensr-xyz/tensr-platform-web` | `tests/fixtures/e2e-auth.ts:13-14` | `6482dd0910fa8e46efe89ad12bfe74e4161f85bd` | **khớp** — xem N-5 |
| 1.16b | `      localStorage.setItem('stytch_session_token', 'e2e-playwright-session');` | `tensr-xyz/tensr-platform-web` | `tests/fixtures/e2e-auth.ts:26` | `6482dd09…` | **khớp** |
| 1.16c | `const SESSION_TOKEN_KEY = 'stytch_session_token';` — **mã sản phẩm đọc đúng khoá bị tiêm** | `tensr-xyz/tensr-platform-web` | `src/utils/auth.ts:3` (còn ở `src/proxy.ts:33`) | `6482dd09…` | **khớp** |
| 1.17 | <code>    command: \`VITE_APP_ENV=e2e VITE_E2E_BYPASS_AUTH=1 npm run dev -- --host 127.0.0.1 --port ${port}\`,</code> | `gumacahin/mis-capstone` | `ui/playwright.config.ts:64` | `de3a62cc82ba49ed3b07f6945ba0f1420bff4f2e` | **đã lệch** (tên biến) — xem N-6 |
| 1.18 | `    command: 'cross-env VITE_E2E_SKIP_AUTH=true vite --port 5174 --strictPort',` | `vandean25/auto-core-platform` | `apps/core-web/playwright.config.ts:37` | `2ecd3d409a72a17169a8eb0a8ea8546b720535f6` | **đã lệch** (tên biến) — xem N-6 |
| 1.19 | `WORKBENCH_ALLOW_LOCAL_DEV_IDENTITY=true WORKBENCH_DEV_USER_ID=e2e-owner` (trong `webServer.command`) | `dawi369/assistant-mk1` | `playwright.config.ts:16` | `5c459abe244ffbd5ccb850966593e47349d21490` | **đã lệch** (tên biến) — xem N-6 |
| 1.20 | `NEXT_PUBLIC_BYPASS_AUTH=true` | `RoleModel/betanxt-issuer-portal` | `issuer-portal/.env:11` | `afe92dab95291fe4be2cec63f6cdab3ee6f126be` | **đã lệch** (tên biến) — xem N-6 |

### "Repo viết thẳng ra là không test được"

| # | Trích nguyên văn | Nguồn | path:line | SHA (lúc kiểm) | Trạng thái |
|---|---|---|---|---|---|
| 1.21 | `` * SCOPE: the whole e2e suite runs unauthenticated — auth is Descope SMS OTP,`` / `` * which cannot be automated here.`` | `amirrudd/flyerboard` | `e2e/messages.spec.ts:6-7` | `32a22a7c1f7f4a7bae15cdce659aacf1914ff7ea` | **khớp** — xem N-7 |
| 1.22 | `` * NOTE: Full OTP login requires a real email. Tests that need auth are`` / `` * marked with [auth-required] and skip if UGENT_TEST_OTP is not set.`` | `intelogroup/ugent` | `e2e/auth-and-routes.spec.ts:8-9` | `0724ab13ef962e98147c2263b189e27cabe6ee05` | **khớp** |
| 1.23 | (orgOS — xem 1.3 / A-1) | `drifter089/orgOS` | `tests/auth-unauthenticated.spec.ts:120-127` | `a9073690…` | **khớp** |
| 1.24 | Con số **"5 repo"** | báo cáo DF-4 §13.1 | — | — | **đã lệch → số đúng là 3** — xem N-8 |

---

## 2. Hai con số xương sống — suy dẫn độc lập

**Cách làm, và vì sao nó độc lập.** Không chạy lại bất kỳ script nào trong `df4/scripts/`. Thay
vào đó: **tải lại toàn bộ 284 repo trực tiếp từ GitHub** bằng đường vận chuyển khác (`tarball`
API thay vì `git clone`), ghi **SHA HEAD của từng repo tại thời điểm tải**, rồi áp **tín hiệu
phát hiện tự viết**. Kết quả tải: **284/284 repo còn tồn tại, 0 repo mất** — và vì ngày kiểm
trùng ngày đo, đây là cùng một trạng thái repo, không phải ảnh chụp khác thời điểm.

**Kiểm chính phép tải của mình (đây là lỗi tôi suýt bỏ qua).** Tôi dùng `curl | tar` và **nuốt
stderr**, nên một lần tải bị cắt giữa chừng sẽ **im lặng** cho ra bản trích thiếu file — và mọi
phép quét sau đó sẽ đếm thiếu mà không ai biết. Đã đối chiếu **từng repo**: số file trên đĩa với
số file mà `git/trees?recursive=1` nói là khớp cùng bộ lọc.

| Kết quả audit | |
|---|---|
| Repo trích đủ ≥90% cây file | **282/284** |
| Repo bị cắt cụt | **2** — `nathanjohnpayne/friends-and-family-billing` (36/272) và `samayhuf-star/Adiology-23Dec-New` (14/865) |
| Sau khi vá đủ, có đổi con số nào không | **KHÔNG.** Cả hai repo **0 hit trên cả hai tín hiệu** |

Nguyên nhân cả hai: tarball rất lớn (repo thứ hai nặng **243MB**) nên luồng tải đứt. Không phải
lỗi bộ lọc — trích lại cùng lệnh trên bản tải đủ ra đúng **272/272** file.

**Vá thế nào:**
- `nathanjohnpayne/friends-and-family-billing` — tải lại tarball ra đĩa trước rồi mới giải nén:
  **272/272 file**, quét lại ⇒ 0 hit.
- `samayhuf-star/Adiology-23Dec-New` — 243MB tải mãi không xong, nên đổi đường: lấy
  `git/trees?recursive=1`, lọc ra **18 file** có thể mang tín hiệu (test/e2e + manifest + config +
  compose + CI), kéo từng file qua `raw.githubusercontent`. **18/18** đọc được, 0 hit.
  Đã mở tay hai file đáng ngờ nhất: `backend/docker-compose.email.yml` là **API gửi mail qua AWS
  SES**, không phải mail-catcher; `tests/paid-signup.spec.ts` (14KB) **không đụng OTP / mã xác
  thực / cookie giả** ở bất kỳ dòng nào.

⇒ **`1/284` và `34/284` đứng nguyên sau khi vá.**

### 2a. `1/284` — repo có test signup tiêu thụ mã xác thực

**Tín hiệu độc lập: NĂNG LỰC ĐỌC HỘP THƯ, không phải từ vựng SDK.**
Đợt kiểm hai của DF-4 quét *thân file test* tìm động từ SDK của từng hãng
(`attemptEmailAddressVerification`, `otp.authenticate`, …). Đường của tôi hỏi câu khác hẳn:
*một test không thể lấy mã từ hộp thư thật nếu repo không có cách nào nói chuyện với hộp thư.*
Nên tôi quét **năng lực**: SDK dịch vụ mail test (Mailosaur/MailSlurp/Mailtrap/testmail…), client
IMAP/POP (`imapflow`, `node-imap`, `mailparser`…), mail-catcher cục bộ
(mailpit/MailHog/MailDev/Inbucket/GreenMail/ethereal), và Gmail/Graph API — trong **manifest phụ
thuộc, docker-compose, CI, và mã test**. Năng lực là **điều kiện cần**, nên phép quét này **cố ý
bắt thừa**, rồi mở tay từng ca.

| Bước | Số |
|---|---|
| Repo quét | **284** (0 mất) |
| Có **bất kỳ** năng lực đọc hộp thư ở đâu đó | **43** |
| Năng lực đó nằm trong **file test** kèm ngữ cảnh danh tính + mã | **6** |
| Mở tay 6 ca ⇒ **thật sự tiêu thụ mã từ hộp thư thật trong luồng danh tính** | **1** |

**Mở tay từng ca trong 6:**

| Repo | Hit | Phán quyết |
|---|---|---|
| `stytchauth/stytch-browser` | `services/e2e-tests/cypress.config.ts:31` · `cypress/e2e/react-demo.cy.ts:14` (MAILOSAUR) | ✅ **CA THẬT** — magic link thật, hộp thư thật (xem 1.6–1.13) |
| `drifter089/orgOS` | `tests/auth-unauthenticated.spec.ts:126` (Mailinator) | ❌ hit nằm trong **comment bỏ cuộc** của `test.skip` — là ca *không làm được*, không phải ca *làm* |
| `embreymorton/CS-Grad-Tracker` | `cypress/e2e/formTests/Nodemailer.cy.js:14` (ethereal.email) | ❌ đọc mail thật nhưng **không phải luồng danh tính** |
| `liorwn/workscanner` | `tests/level5-scan/icloud-ingest.test.ts:2` (ImapFlow) | ❌ đọc IMAP là **tính năng sản phẩm**, không phải xác thực |
| `anietieakpan/kazuq` | `services/notification-service/test/utils/test-utils.ts:179` (`getMailhogEmails`) | ❌ helper **được định nghĩa và không bao giờ được gọi** (grep toàn repo chỉ ra đúng dòng định nghĩa) |
| `sebkopsi/Programsko-inzenjerstvo` | `frontend/cypress/e2e/auth/not_implemented_yet.cy.js:9` (`@testmail`) | ❌ **false positive** — `test@testmail.com` là email đăng nhập cắm cứng, không phải dịch vụ testmail.app |

> ### ⇒ **Suy dẫn độc lập: 1/284. Con số cũ: 1/284. Lệch 0%.** ✅ **TRÙNG.**

Hai đường hoàn toàn khác nhau (từ vựng SDK trong thân test ⟷ năng lực đọc hộp thư trong manifest/
CI/compose/test) chỉ ra **cùng một repo duy nhất**. Đây là xác nhận thật, không phải lặp lại
pipeline. Bản đồ phần còn lại cũng khớp với DF-4: các ca đọc mail thật nhưng ngoài luồng danh
tính (`CS-Grad-Tracker`, `workscanner`) đúng là nhóm A2 mà DF-4 đã tách riêng.

### 2b. `31/284` — repo tự dựng backdoor

**Tín hiệu độc lập: tách đôi khái niệm, quét toàn repo, giữ mọi hit.**
`10-selfbuilt-bypass.py` chỉ đọc file có **đường dẫn** giống E2E, lấy **hit đầu tiên rồi dừng**.
Đường của tôi tách "tự dựng" thành hai nửa rõ ràng — **SWITCH** (cờ/biến môi trường tắt xác thực)
và **FORGE** (session/cookie/token bịa ra, cắm vào trình duyệt), cộng **HEADER** (header tin cậy
chỉ dùng trong test) — quét **toàn repo**, **trừ đi** cơ chế chính hãng theo tên để không đếm nhầm
backdoor hãng, và **giữ mọi hit kèm path:line** thay vì dừng ở hit đầu.

Để so đúng cùng một đại lượng, tôi chạy thêm một thang **STRICT-CMP**: **sao chép y nguyên luật
chọn file của `10-selfbuilt-bypass.py`** (chỉ `.ts/.tsx/.js/.jsx`, chỉ đường dẫn E2E, loại
`__tests__` và `*.test.*`), chỉ thay phần **regex phát hiện** bằng của tôi. Nhờ vậy mọi chênh lệch
còn lại là do **cách nhận diện**, không phải do **đọc file khác nhau**.

| Thang | Kết quả |
|---|---|
| Có **bất kỳ** tín hiệu bypass tự dựng ở đâu đó | 98/284 |
| BROAD (mọi đường test, kể cả unit test) | 51/284 = 18,0% |
| E2E-only (phạm vi của tôi) | 35/284 = 12,3% |
| **STRICT-CMP (luật chọn file của DF-4, regex của tôi)** | **35/284 = 12,3%** |

**Đối chiếu tập hợp — đây mới là kết quả đáng giá:**

```
chung (cả hai cùng bắt)      : 31
chỉ tôi bắt                  :  4
chỉ DF-4 bắt                 :  0
```

> **Tập 35 của tôi là TẬP CHA của tập 31 của DF-4.** Không có một repo nào DF-4 bắt mà đường
> độc lập không xác nhận lại được ⇒ **0 false positive trong danh sách cũ.**

**Mở tay 4 repo dôi ra:**

| Repo | path:line | SHA | Phán quyết |
|---|---|---|---|
| `ubcdiscovery/ubc-discovery` | `web/e2e/identity-convergence.spec.ts:104-110` — `sessionStorage.setItem("ubc-discovery-test-google-user", JSON.stringify({uid: "otp-first-uid", email: "member@example.com"}))`; và **mã sản phẩm đọc chính khoá đó**: `web/app/lib/firebase.ts:22` `const TEST_GOOGLE_USER_KEY = "ubc-discovery-test-google-user";` (5 file E2E cùng tiêm; không có `connectAuthEmulator`/`FIREBASE_AUTH_EMULATOR_HOST`) | `ef6a28f9252285b47f6a93f61b963a433f96f947` | ✅ **backdoor tự dựng thật** — và là ví dụ mạnh hơn phần lớn danh sách cũ |
| `kil-dev/kil.dev` | `tests/e2e/pages/admin-ask-kilian.spec.ts:2` import `ADMIN_TEST_BYPASS_COOKIE` từ `src/lib/admin-test-bypass`; `src/lib/admin-test-bypass.ts:1-2` = `export const ADMIN_TEST_BYPASS_COOKIE = 'pet-gallery-test-admin'` / `= '1'` | `441e8a5edb0980a2a8f67a91b11144438027a380` | ✅ **thật** — cả một module chuyên để bỏ qua auth admin |
| `SDG-AI-Lab/Digital_Technologies_Radar` | `cypress/e2e/create-disaster.cy.ts:2-4` — `// Set logged in state` / `window.localStorage.setItem('drr-current-user-id', 'admin');` | `1dcf308577a0617690b8548412e2b0a3ccc0babf` | ✅ **thật** — chính comment nói ra nó làm gì |
| `MCPJam/inspector` | `mcpjam-inspector/e2e/oauth-debugger.spec.ts:255` — `sessionStorage.setItem("oauth-debugger-e2e-started", "true")` | `dd212decbd6db8b50348c9a543307928c7986857` | ❌ **FALSE POSITIVE của tôi** — cờ đánh dấu đã chạy E2E, không phải credential. Regex của tôi khớp vì `"oauth"` chứa `auth`. |

**Vì sao DF-4 sót 3 ca:** regex `INJECT` của nó đòi khoá lưu trữ phải chứa một trong
`clerk|stytch|supabase|sb-|wos-|auth|session|token`. `drr-current-user-id` và
`ubc-discovery-test-google-user` chứa `user` — **không có trong danh sách đó**. Còn `kil.dev` thì
tên cookie là **hằng số import**, nên trong file spec không hề có chuỗi literal nào để khớp.

> ### ⇒ **Suy dẫn độc lập sau khi mở tay: 34/284 = 12,0%. Con số cũ: 31/284 = 10,9%.**
> ### **Lệch tương đối = 3/31 = +9,7% — NẰM TRONG ngưỡng 10%.** ✅ **COI LÀ TRÙNG.**

**Ba điều phải nói kèm, không được giấu:**
1. **Lệch 9,7% là sát mép.** Không được viết như thể hai phép đo khớp hoàn hảo.
2. **Hướng lệch có lợi cho sự trung thực của báo cáo cũ:** DF-4 **đếm thiếu**, không đếm thừa.
   Con số công bố là **bảo thủ**.
3. **Tôi chỉ mở tay phần CHÊNH LỆCH (4 repo), không mở lại cả 34.** 31 ca chung dựa vào đợt tay
   kiểm của DF-4 (tự khai 17/20 đúng ở mẫu 20 repo). Nếu bài viết cần con số chắc hơn nữa thì
   phải mở tay cả 34, và đó là việc chưa làm.

**Con số được phép dùng khi viết bài:** giữ **31/284** như báo cáo cũ (bảo thủ, đã tay kiểm), hoặc
dùng **34/284** kèm ba ca mới có path:line ở bảng trên. **Không được dùng 35** (chứa false
positive), và **không được dùng 51 hay 98** (thang khác, phạm vi khác).

---

## 3. Trích dẫn doc hãng (nền của DF-3)

| # | Trích nguyên văn | Nguồn (URL đọc lại 2026-08-16) | Trạng thái |
|---|---|---|---|
| 3.1 | "Any email with the `+clerk_test` subaddress is a test email address." | clerk.com/docs/guides/development/testing/test-emails-and-phones | **khớp** |
| 3.2 | "When testing email verification codes, no email with the verification code will be sent." | như trên | **đã lệch** (trích cụt) — xem N-9 |
| 3.3 | Mã xác thực cố định `424242` | như trên | **khớp** |
| 3.4 | "However, this is highly discouraged." (về bật test mode trên production instance) | như trên | **khớp** |
| 3.5 | "Setup must be run serially, this is necessary if Playwright is configured to run fully parallel" | clerk.com/docs/guides/development/testing/playwright/overview | **khớp** |
| 3.6 | `setup.describe.configure({ mode: 'serial' })` | như trên (mẫu code global setup) | **khớp** |
| 3.7 | "This is an insecure method and is only recommended when generated OTP codes are not viable for testing." | docs.descope.com/test-users, callout cảnh báo mục **Static OTP codes** | **đã lệch** (phạm vi) — xem N-10 |
| 3.8 | "Utilizing test users, you can generate OTP codes and Magic/Enchanted link tokens for test users directly using the Descope API and SDK, without sending actual communications to the test account." | docs.descope.com/test-users | **khớp** |
| 3.9 | "The best practice is never to visit or test third-party sites over which you have no control." | auth0.com/blog/end-to-end-testing-with-cypress-and-auth0/ | **khớp** |
| 3.10 | "Keep in mind that you must not use this grant on your public clients. This is an exception to this rule because it is an end-to-end test that won't be used by real users." | như trên | **khớp** |
| 3.11 | "When you provide the fictional phone number and send the verification code, no actual SMS is sent." | firebase.google.com/docs/auth/web/phone-auth | **đã lệch** — bản DF-3 là **diễn giải**, xem N-11 |
| 3.12 | "Run consecutive tests with the same phone number without getting throttled." | như trên | **khớp** |
| 3.13 | "The staging environment now includes a Test SSO page and comes with a pre-configured Test Organization with a WorkOS Test Identity Provider." | workos.com/changelog/test-sso | **khớp** |
| 3.14 | "When creating test users in automated tests, use email addresses on reserved example domains such as `user@example.com`." | workos.com/docs/authkit/environments | **khớp** |
| 3.15 | "If your API credentials and the request format are correct you will receive a 200 status response, but no email will actually be sent." | stytch.com/docs/guides/testing/sandbox-values | **đã lệch** (URL sai trong DF-3) — xem N-12 |
| 3.16 | "The sandbox values below are only available when calling the Stytch API directly. They will not work when used with a frontend or mobile Stytch SDK." | như trên | **khớp** |
| 3.17 | OTP sandbox `000000` · số điện thoại sandbox `+10000000000` | như trên | **khớp** |
| 3.18 | "we generally recommend using a platform like Mailosaur to set up a programmatically accessible email or SMS inbox" | stytch.com/docs/b2b/guides/testing/e2e-testing | **khớp** |
| 3.19 | "Test Accounts allow you to log in with a static OTP" | docs.dynamic.xyz/developer-dashboard/test-accounts | **khớp** |
| 3.20 | "Any email including `+dynamic_test` right before the ”@” sign" · "Any US phone number with an area code of `(555)`" | như trên | **khớp** |
| 3.21 | "Kinde requires OTP email verification when signing up for a new user." | docs.kinde.com/testing/testing-authentication-flows/ | **đã lệch** (URL cụ thể hơn) — xem N-13 |
| 3.22 | "Test email services like Mailosaur or Mailtrap provide API access to test inboxes, making it easy to retrieve OTP codes programmatically." | docs.kinde.com/testing/testing-passwordless-flows/ | **khớp** |
| 3.23 | "The magic-link path (Supabase's default passwordless flow) cannot be clicked from an inbox in CI, so it needs a separate strategy" | DF-3 dẫn `supabase/supabase-js` `docs/TESTING.md` | **không tìm thấy** — xem N-14 |
| 3.24 | Con số **8/10 hãng có cửa hậu chính thức** | tổng hợp DF-3 | **khớp** (nhưng phải thay chỗ dựa của Supabase) — xem N-15 |
| 3.25 | `clerk/javascript#7891` — **closed 2026-04-03**, `state_reason: completed`, đóng bởi `jacekradko` | api.github.com/repos/clerk/javascript/issues/7891 | **khớp** |
| 3.26 | Issue có **đúng 2 comment, cả hai của `m13v`, `author_association: NONE`** ⇒ **không một comment nào của hãng** | như trên | **khớp** |
| 3.27 | "One practical workaround while waiting for a proper fix: pre-authenticate each worker with a dedicated test user and save the storage state to separate JSON files, then load each worker's state from its own file rather than calling signIn() concurrently." | comment của `m13v`, 2026-03-23T21:26:01Z | **đã lệch** (bị nâng cấp thành "cách chữa chính") — xem N-16 |
| 3.28 | "When using `@clerk/testing` with Playwright and `--workers=2` (or more), all tests fail with `TimeoutError: page.waitForFunction: Timeout 15000ms exceeded` at `clerk.signIn()`. With `--workers=1`, authentication works 100% reliably." | thân issue #7891 | **khớp** |

---

## 4. Vật liệu nội bộ run-3..run-6

| # | Trích nguyên văn / phát biểu | Nguồn | path:line | SHA | Trạng thái |
|---|---|---|---|---|---|
| 4.1 | `curl -s -i --max-time 10 http://127.0.0.1:3000/ | head -40; echo "=== 8925 health ==="; curl -s --max-time 10 http://127.0.0.1:8925/api/v1/healthz | head -20` | `authstunt` (transcript thô đã công bố) | `docs/transcripts/sessions/run-3.jsonl`, message `assistant` lúc `2026-08-16T10:09:54.700Z`, `tool_use` id `toolu_01LwUaWNBMB5CfJPq15Eh2PX` | `d77025a` | **khớp** — xem N-17 |
| 4.2 | `**Provisional.** Everything else under `/api/v1`: ending a run, the evidence` / `route, the healthz body, and the `totp` claim kind.` | `authstunt` README trước bản vá | `README.md:662-663` @ `b5d8f8c^` | `b5d8f8c^` | **khớp** — và mạnh hơn: xem N-18 |
| 4.3 | `The HTTP API authenticates callers with a project bearer, and serve refuses to` / `bind the API for a project that has none.` | `authstunt` README trước bản vá | `README.md:690-691` @ `b5d8f8c^` | `b5d8f8c^` | **khớp** |
| 4.4 | `healthz` xuất hiện **đúng 1 lần** trong README trước vá | `authstunt` | `README.md` @ `b5d8f8c^` (911 dòng) | `b5d8f8c^` | **đã lệch** (số dòng README) — xem N-19 |
| 4.5 | Bearer hợp lệ + route không tồn tại → **404** · không bearer → **401** · route thật + sai method → **405** | `authstunt-internal` | `p1-transcripts/c9-three-run-protocol.md:61-70` | `132c7b92` | **đã lệch** (hạng bằng chứng) — xem N-20 |
| 4.6 | `{"lease_id":"e8ac35aba194","identity_id":"b037e808a01b","addr":"control-e83fd1fa116f@demo.test","role":"control",…}` | `authstunt-internal` | `p1-transcripts/run6-session.jsonl` (tool_result lease control) | `5e0d9e3` (không đổi tới `132c7b92`) | **khớp** |
| 4.7 | `=== control: the OTHER account's code 859912 ===` → `That code is not valid. Codes expire after 10 minutes, and asking for a new one retires the old one.` → `[http 400]` | `authstunt-internal` | `p1-transcripts/run6-session.jsonl` (tool_result của lệnh đối chứng chéo) | `5e0d9e3` | **khớp** |
| 4.8 | Mã `859912` là mã thật của `signup-3ed883cb37ed@demo.test`, claim `b6544711e43f`, message `e11592ce4372`, `reason: claim_ok` | như trên | `p1-transcripts/run6-session.jsonl` | `5e0d9e3` | **khớp** |
| 4.9 | run-4/5/6 đều gọi `/healthz` (không tiền tố) và **0 lần** gọi `/api/v1/healthz` | `authstunt-internal` | `p1-transcripts/c9-three-run-protocol.md:88-92` + đếm lại trên `run{4,5,6}-session.jsonl` | `132c7b92` | **khớp** |
| 4.10 | B-26: **4/4 run** không định vị được mình ở nhánh mail yếu | `authstunt-internal` | `run-3-reading.md:276` · `run-4-reading.md:104` · `run-5-reading.md:75` · `run-6-reading.md:82` | `132c7b92` | **khớp** |

---

## 5. Ràng buộc claim ↔ message — **chiều ngược, đo bằng instance thật**

Câu hỏi: **hai claim có bind được cùng một message không?** Đã đọc schema, đọc đường code, và
**dựng instance riêng chạy thật** (probe test tạm trong `internal/personas`, đã xoá sau khi chạy;
repo sạch, `go vet` xanh).

### 5a. Cơ chế — đọc từ nguồn

| # | Trích nguyên văn | Nguồn | path:line | SHA | Trạng thái |
|---|---|---|---|---|---|
| 5.1 | `    message_id      TEXT REFERENCES messages (id) ON DELETE CASCADE,` | `authstunt` | `internal/store/schema_v4.sql:74` | `882b0d06` | **khớp** (đúng như trích dẫn đã có) |
| 5.2 | `CREATE UNIQUE INDEX claims_one_per_message_kind ON claims (message_id, kind)` / `    WHERE message_id IS NOT NULL;` | `authstunt` | `internal/store/schema_v4.sql:88-89` | `882b0d06` | **khớp — CÓ UNIQUE, nhưng trên CẶP `(message_id, kind)`** |
| 5.3 | `-- One-time. A message backs at most one claim of a kind, across every` / `-- lease and every run, so a duplicate delivery leaves the second copy` / `-- visible and unclaimed instead of backing a second handover.` | `authstunt` | `internal/store/schema_v4.sql:85-87` | `882b0d06` | **khớp** |
| 5.4 | `		       EXISTS (SELECT 1 FROM claims c` / `		               WHERE c.message_id = m.id AND c.kind = ?) AS claimed` | `authstunt` | `internal/store/claims.go:68-69` | `882b0d06` | **khớp** — code cùng phạm vi với index |
| 5.5 | `		case claimed:` / `			refused.AlreadyClaimed++` | `authstunt` | `internal/store/claims.go:112-113` | `882b0d06` | **khớp** — nơi sinh ra lý do |
| 5.6 | `	case refused.AlreadyClaimed > 0:` / `		return Claimed{Reason: store.ReasonAlreadyClaimed}, nil` | `authstunt` | `internal/personas/claim.go:345-346` | `882b0d06` | **khớp** |
| 5.7 | `	ReasonAlreadyClaimed  = "claim_already_claimed"` | `authstunt` | `internal/store/models.go:257` | `882b0d06` | **khớp** |

⇒ **Chặn hai lớp, cùng một phạm vi:** unique index ở DB **và** cờ `claimed` trong truy vấn ứng
viên, cả hai đều tính theo **`(message_id, kind)`**, không phải theo `message_id`.

### 5b. Đo thật — ba kịch bản, instance riêng, data-dir riêng

| Kịch bản | Dựng | Kết quả đo | Kết luận |
|---|---|---|---|
| **A. Hai lease khác nhau · CÙNG kind · một message** | 1 mail gửi tới cả hai địa chỉ đã lease | lease A → `claim_ok`, msg `379537793229`, value `313131` · lease B → **`claim_already_claimed`**, không message, không value | **Exclusivity GIỮ.** Đây là ca nguy hiểm nhất và nó chặn được. |
| **B. Một lease · HAI kind · một message** | mail chứa cả OTP lẫn magic link | `email_otp` → `claim_ok`, msg `e996041532f6`, `424243` · `magic_link` → `claim_ok`, **cùng msg `e996041532f6`** | **Một message BACK ĐƯỢC hai claim** khi khác kind. |
| **C. Hai lease khác nhau · HAI kind · một message** | mail chứa cả hai, gửi tới cả hai địa chỉ | lease A `email_otp` → `claim_ok`, msg `7e43bb49fdbe`, `515151` · lease B `magic_link` → `claim_ok`, **cùng msg `7e43bb49fdbe`** | Hai lease cùng rút được — **nhưng xem 5b-bis: cả hai địa chỉ đều nằm trên envelope.** |

### 5b-bis. Ca C dựng lại cho đúng — CÙNG hay KHÁC địa chỉ?

Ca C ở trên **không tách được** hai khả năng: (i) lease chạm được mail của địa chỉ khác, hay
(ii) mail thật sự được gửi tới cả hai địa chỉ. Đã dựng lại thành ba kịch bản tách bạch.

**Đường bind, đọc từ nguồn:** `BindRecipients` duyệt **từng người nhận trên envelope** và với mỗi
địa chỉ gọi `t.leaseAt(ctx, addr, receivedAt)` để giải ra lease đang giữ **đúng địa chỉ đó** tại
thời điểm mail đến; địa chỉ không giải được thì vào `unbound`, **không bind cho ai**.

| # | Trích nguyên văn | path:line | SHA | Trạng thái |
|---|---|---|---|---|
| 5.8 | `		owner, err := t.leaseAt(ctx, addr, receivedAt)` / `		if errors.Is(err, ErrNotFound) {` / `			unbound = append(unbound, addr)` | `internal/store/bindings.go:50-52` | `882b0d06` | **khớp** — đây là chỗ ranh giới địa chỉ được kiểm |
| 5.9 | `		  FROM message_bindings b` / `		  JOIN messages m ON m.id = b.message_id` / `		 WHERE b.lease_id = ?` | `internal/store/claims.go:70-72` | `882b0d06` | **khớp** — claim chỉ thấy message đã bind cho chính lease đó |

**Đo lại, ba kịch bản:**

| # | Dựng | Bindings | Kết quả | Phán quyết |
|---|---|---|---|---|
| **D1** | Hai địa chỉ **KHÁC nhau**, **cả hai đều trên envelope** (`Recipients: [addrA, addrB]`) | leaseA=1 · leaseB=1 | A/`email_otp` → `claim_ok` msg `e660e9129ee7` · B/`magic_link` → `claim_ok` **cùng msg** | Mỗi lease bind vì **địa chỉ của CHÍNH NÓ** có trên envelope. Đây là **CC**, không phải vượt ranh giới. |
| **D2** | Mail gửi **CHỈ tới addrA**; lease B (địa chỉ khác, chưa từng trên envelope) thử claim | leaseA=1 · **leaseB=0** | A/`email_otp` → `claim_ok` msg `c5784cce6514` · B/`magic_link` → **`claim_no_binding`**, không message, không value | **RANH GIỚI GIỮ.** Lease không chạm được mail chưa từng gửi cho nó. |
| **D3** | **CÙNG một địa chỉ** (`pooled-shared@demo.test`) qua một lượt handover pooled; lease sau thử claim kind khác trên mail của lease trước | lease2 thấy **0 binding** | lease1/`email_otp` → `claim_ok` msg `1d2d96f1648d` · lease2/`magic_link` → **`claim_no_binding`** | **RANH GIỚI GIỮ QUA HANDOVER.** Binding gắn với **lease**, giải theo `leaseAt(addr, receivedAt)`, nên người giữ sau không thừa kế mail của người giữ trước. |

> ### ⇒ **KHÔNG CÓ LỖ RÀNG BUỘC.**
> Trả lời đúng ba câu hỏi đã đặt:
> **(a)** Hai lease ở ca C là **KHÁC địa chỉ**.
> **(b)** Không có cơ chế nào cho lease chạm mail của địa chỉ khác — ranh giới được kiểm ở
> `bindings.go:50`, và D2 chứng minh bằng đo: `claim_no_binding`. Ca C chạm được **chỉ vì
> envelope liệt kê cả hai địa chỉ**, tức mail thật sự gửi cho cả hai.
> **(c)** Khác địa chỉ + khác kind + cùng message: **chỉ xảy ra khi mail gửi cho cả hai** (D1);
> nếu không thì bị chặn (D2). Cùng địa chỉ + khác kind + cùng message qua handover: **bị chặn**
> (D3).
>
> ⇒ Tính duy nhất theo cặp `(message_id, kind)` là **chi tiết schema**, **không phải lỗ**.

### 5c. Phát biểu được phép dùng khi viết bài

✅ **Được viết:** *"Một message chống lưng cho **nhiều nhất một claim của một kind**, xuyên suốt
mọi lease và mọi run — chặn ở cả unique index lẫn đường code."* Đây là nguyên văn ý đồ thiết kế
(5.3), và ca A chứng minh nó giữ đúng ngay cả khi một mail đi tới hai địa chỉ đã lease.

❌ **KHÔNG được viết:** *"mỗi message thuộc về đúng một người"* · *"hai claim không bao giờ bind
cùng một message"* · bất kỳ diễn đạt nào bỏ chữ **"của một kind"**.

✅ **Cũng được viết, và nên viết:** *"một lease chỉ thấy mail được gửi tới đúng địa chỉ nó đang
giữ"* — đúng cả với địa chỉ khác (D2) lẫn với người giữ trước cùng địa chỉ (D3), cả hai đều trả
`claim_no_binding`.

⚠️ **Sắc thái duy nhất phải nói kèm:** một email mang **cả OTP lẫn magic link** và được gửi tới
**hai địa chỉ đã lease** (CC) sẽ trao mỗi bên một loại bí mật. Đây **không phải vượt ranh giới** —
mail thật sự gửi cho cả hai — nhưng nó là ngoại lệ duy nhất cho câu "một message, một người giữ",
và nó có thật, đã đo (D1).

📌 Điều kiện dựng D1: mail phải có **nhiều người nhận** (CC / catch-all) mà **cả hai đều đang được
lease**. Ở đường chạy thường, mỗi lease ephemeral có địa chỉ riêng ⇒ một message bind một lease.
Đừng để người đọc tưởng đây là hành vi mặc định.

---

## 6. Playwright `acquireAccount` — sắc thái đã chốt

| # | Trích nguyên văn | Nguồn | path:line | SHA (lúc kiểm) | Trạng thái |
|---|---|---|---|---|---|
| 6.1 | `    const account = await acquireAccount(id);` | `microsoft/playwright` | `docs/src/auth.md:178` | `d5a185a894ab3ab17ff77a44e116a1339c6bdaed` | **khớp** |
| 6.2 | `    const account = await acquireAccount(id);` | `microsoft/playwright` | `docs/src/auth.md:383` | `d5a185a894ab3ab17ff77a44e116a1339c6bdaed` | **khớp** |
| 6.3 | `acquireAccount` **được gọi 2 lần, không bao giờ được định nghĩa** (grep định nghĩa = 0 kết quả trong `auth.md`) | như trên | `docs/src/auth.md` | `d5a185a…` | **khớp** |
| 6.4 | "This is the **recommended** approach for tests that **modify server-side state**. In Playwright, worker processes run in parallel. In this approach, each parallel worker is authenticated once. All tests ran by worker are reusing the same authentication state. We will need multiple testing accounts, one per each parallel worker." | như trên | `docs/src/auth.md:135` | `d5a185a…` | **khớp** |
| 6.5 | `    // Acquire a unique account, for example create a new one.` / `    // Alternatively, you can have a list of precreated accounts for testing.` / `    // Make sure that accounts are unique, so that multiple team members` / `    // can run tests at the same time without interference.` | như trên | `docs/src/auth.md:174-177` (lặp lại ở `379-382`) | `d5a185a…` | **khớp** |

✅ **Câu được phép dùng:** *"doc đưa call site, không đưa implementation."*
❌ **Câu KHÔNG được dùng:** bất cứ diễn đạt nào hàm ý Playwright **ra lệnh** ("you must write this
yourself"). Doc **không có** câu mệnh lệnh đó; hướng dẫn duy nhất nằm trong **comment của sample
code** (6.5).

---

## 6bis. BỔ SUNG 2026-08-17 — vật liệu tìm được khi SCRIPT HOÁ LẠI hai con số

**Vì sao có mục này.** Phiên xác minh 2026-08-16 để lại **văn xuôi mà không để lại script**, nên
`34/284` và chuỗi `43 → 6 → 1` không ai chạy lại được, kể cả người viết. Ngày 2026-08-17 hai phép
đo đó được dựng lại thành script chạy được (`release/post-1-repro-kit/scripts/20..24`), tải lại
toàn bộ corpus và kiểm độ đủ từng repo. Vòng chạy lại **tìm thêm một repo mà cả hai lượt trước đều
sót**. Mục này ghi nó vào sổ, vì Luật 1 cấm bài trích thứ không có ở đây.

| # | Trích nguyên văn | Nguồn | path:line | SHA | Trạng thái |
|---|---|---|---|---|---|
| 6b.1 | `export const userCookies: UserCookies[] = [` | `sefi-uzan/yanshuf-ai` | `tests/e2e/fixtures/config.ts:8` | `26638529ef61e906e8a61d24edc8d1176d5023d3` | **khớp** |
| 6b.2 | `    name: "next-auth.session-token",` | như trên | `tests/e2e/fixtures/config.ts:10` | như trên | **khớp** |
| 6b.3 | `      process.env.USER_COOKIE ||` — dòng 13 ngay dưới là **một JWT cắm cứng dài 492 ký tự**, cố ý **không chép lại vào sổ**; kiểm bằng path:line + SHA | như trên | `tests/e2e/fixtures/config.ts:11-13` | như trên | **khớp** |
| 6b.4 | `    return this.page.context().addCookies(userCookies);` — chỗ tiêm | như trên | `tests/e2e/pages/website.ts:13` | như trên | **khớp** |

**Phán quyết: backdoor tự dựng THẬT.** Một session token bịa sẵn, cắm thẳng vào trình duyệt trước
khi test chạy — đúng hạng FORGE.

**Vì sao hai lượt trước sót.** Tham số của `addCookies` là **biến** (`userCookies`), không phải
object literal, nên trong file spec **không có chuỗi literal nào** cạnh lời gọi để regex bắt. Đây
đúng cùng một kiểu sót đã ghi ở §2b với `kil-dev/kil.dev` (tên cookie là hằng số import). Regex của
DF-4 lẫn của lượt xác minh đều đòi khoá/tên nằm trong bán kính ký tự quanh lời gọi.

**Repo không trôi:** commit gần nhất `2025-11-07T12:28:00Z`, tức trước ngày đo, và bản tải ngày
2026-08-17 kiểm đủ **118/118 file**.

### Con số đổi theo

| | |
|---|---|
| DF-4 công bố | **31/284** |
| Lượt xác minh 2026-08-16 (§2b) | **34/284** = 31 + 3 ca sổ đã ghi |
| **Chạy lại 2026-08-17** | **35/284** = 34 + `sefi-uzan/yanshuf-ai` |

Tập sau **chứa trọn** tập trước ở cả hai bước; không repo nào của DF-4 rụng. Sai số vẫn nghiêng về
**đếm thiếu**, y như §B.2 đã nhận xét — nay có thêm một ca nữa chứng minh điều đó.

⚠️ **Một dương tính giả phải khai kèm.** Lượt chạy lại đầu ra **36**, dôi thêm
`QRun-IO/qqq-frontend-next` mà hit duy nhất là `AUTH_META = { name: 'mockAuth', ... }` trong file
mock route của Playwright — **mock phản hồi API, không phải cờ tắt xác thực**. Nó khớp chỉ vì bản
chép lại regex có thêm token `MOCK|FAKE`, thứ **không có** trong định nghĩa hạng này ("cờ hoặc biến
môi trường tắt xác thực") lẫn trong regex gốc của DF-4. Đã **gỡ token rồi chạy lại toàn bộ** thay vì
loại riêng repo đó — gỡ token là hệ thống và kiểm được, loại repo là phán quyết một ca ai cũng cãi
được. Gỡ xong rụng đúng một repo, không kéo theo cái nào. **Cả hai con số đều công bố** trong kit
(`selfbuilt-bypass-with-mockfake.txt` = 36 · `selfbuilt-bypass-v2.txt` = 35).

### Chuỗi 43 → 6 → 1 khi chạy lại: **15 → 6 → 1**

Stage 1 hẹp hơn hẳn (15 so với 43) nhưng **stage 2 ra đúng sáu repo giống hệt**, và stage 3 vẫn là
`stytchauth/stytch-browser` với SHA khớp sổ. 28 ứng viên dôi của lượt cũ rụng hết ở stage 2 — đúng
hành xử của một điều kiện **cần nhưng chưa đủ**. Đây là **hội tụ**, không phải lệch: hai tấm lưới
thưa dày khác nhau kéo qua cùng 284 repo và vớt lên cùng sáu con cá.

**Được phép viết:** *"con số 1/284 được xác nhận bằng ba đường độc lập"*. Phải kèm: stage 1 của
đường thứ ba **không** tái lập được con số 43.

---

## 7. CẤM LÊN BÀI

| Mục | Lý do |
|---|---|
| Câu Supabase "The magic-link path … cannot be clicked from an inbox in CI" | Không tồn tại ở nguồn được dẫn (`supabase/supabase-js` `docs/TESTING.md`, code-search toàn repo = 0). Xem N-14. |
| Câu Firebase "When signing in with a test phone number, no SMS verification code is sent; instead, enter the code you registered when creating the test number." | Là diễn giải, không phải nguyên văn. Dùng 3.11 thay thế. |
| Con số "5 repo viết thẳng ra là không test được" | Số đúng là 3. Xem N-8. |
| Các tên biến trần `BYPASS_AUTH` / `SKIP_AUTH` / `ALLOW_LOCAL_DEV_IDENTITY` | Không repo nào dùng đúng chuỗi đó. Xem N-6. |
| Con số "ghim serial 50,4%" | Chính worker DF-4 đánh dấu là kết luận yếu nhất báo cáo, owner đã đồng ý không mang lên public. |
| Bất kỳ con số nào của giaoanai (40 bug / 32 luồng mail) ở phần lập luận | Luật §8sexies của DF-1: hạng C, tự phục vụ. |

---

## 8. Ghi chú bắt buộc (N-*)

**N-1.** Báo cáo DF-4 và `00-synthesis.md` §8octies ghi `e2e/global.setup.ts:16-17`. Đường dẫn
thật là `apps/web/e2e/global.setup.ts:16-17`. Nội dung khớp từng ký tự.

**N-2.** Báo cáo ghi dải `117-124`. Dải thật của khối comment là **120-127**; `test.skip(` bắt đầu
ở dòng **117**.

**N-3.** Báo cáo ghi `:33`. Dòng 33 là `// Step 2: Read the code the emulator generated via the
escape-hatch API.`; câu được trích (`In real apps this would come from the email inbox.`) nằm ở
dòng **34**.

**N-4.** Báo cáo ghi `:36`. Câu "Unique email per run…" ở dòng **35**. Dòng 36 là
`// Uses +clerk_test so 424242 works as the verification code.` — **phải trích kèm**, nếu không
người đọc sẽ tưởng đây là chống đụng email thật; thực chất repo dùng cửa hậu chính hãng.
Báo cáo cũng ghi `:77` cho chỗ nhập `424242`; dòng 77 là comment, lệnh thật ở dòng **80**.

**N-5.** ⚠️ **Chính phiên xác minh này suýt kết luận sai — ghi lại để không lặp.** Vòng đầu tôi
tìm `stytch_session_token` **chỉ trong `df4/`** (evidence, data, round2, và regex của
`10-selfbuilt-bypass.py`) và ra **0 lần** ⇒ đã ghi là *"không tìm thấy, cấm dùng"*. Sai. Khi 284
repo tải xong và quét lại trên **mã nguồn thật**, chuỗi này **có mặt**, trong đúng hình dạng mà
`00-synthesis.md` mô tả: `tensr-xyz/tensr-platform-web` `tests/fixtures/e2e-auth.ts:13-14, 26`.

**Bài học phương pháp:** `df4/evidence/` là **bản trích theo grep của script 03**, không phải bản
sao repo. Vắng mặt trong `evidence/` **không chứng minh** vắng mặt trong repo. Muốn kết luận
"không tìm thấy" thì phải quét **mã nguồn thật**, không quét sử liệu trích xuất.
(`clerk-db-jwt` — bị nghi cùng lỗi — cũng **có thật**, xem 1.15. Cả hai ví dụ trong
`00-synthesis.md:251` / `05-roadmap.md:194` **đều đúng**; chỉ là chúng chưa từng được ghi vào
`round2` vì output ở đó cắt chuỗi khớp và chỉ giữ hit đầu tiên mỗi repo.)

📌 Lưu ý phụ: `tensr-xyz/tensr-platform-web` **đã nằm trong danh sách 31** của DF-4, nhưng dưới
một hit khác (`playwright.launch.config.ts :: AUTH_BYPASS`). Tức không phải repo mới; chỉ là bằng
chứng đắt hơn cho cùng repo đó.

**N-6.** Không repo nào dùng đúng chuỗi `BYPASS_AUTH`, `SKIP_AUTH` hay `ALLOW_LOCAL_DEV_IDENTITY`.
Tên thật đều có tiền tố: `VITE_E2E_BYPASS_AUTH` · `NEXT_PUBLIC_BYPASS_AUTH` · `VITE_E2E_SKIP_AUTH`
· `WORKBENCH_ALLOW_LOCAL_DEV_IDENTITY`. Viết bài thì hoặc trích đúng tên đầy đủ kèm repo, hoặc
nói rõ đây là **họ tên biến** chứ không phải chuỗi nguyên văn.

**N-7.** `amirrudd/flyerboard` nói về **Descope SMS OTP**, không phải mail. Không được xếp chung
với nhóm "không đọc được hộp thư" mà không nói rõ.

**N-8.** Bảng §13.1 của báo cáo DF-4 ghi loại D = **5** và chỉ nêu tên 2. Output máy của chính
đợt đó (`df4/round2/round2-verify-g-independent.txt`) liệt kê **3**:
`amirrudd/flyerboard` · `drifter089/orgOS` · `intelogroup/ugent`. Quét độc lập lại toàn bộ
**284 repo tải mới** ra 4 hit, trong đó 1 là false positive rõ ràng
(`tindevelopers/equipmentbalkans` `apps/auction/src/lib/seller-roles.test.ts:126` — chuỗi khớp là
**tên một test**: `test("returns null when cookie email cannot be verified", …)`, không phải lời
tuyên bố không test được) ⇒ còn **đúng 3 repo đó**. Thêm một mâu thuẫn nội bộ: cùng bảng đó xếp
`amirrudd/flyerboard` sang hàng *"Mock/component test, loại"*, trong khi output máy xếp nó vào D.
**Số dùng được: 3.**

**N-9.** DF-3 trích `"no email with the verification code will be sent"`. Câu đầy đủ:
`"When testing email verification codes, no email with the verification code will be sent."`

**N-10.** Cảnh báo `"This is an insecure method…"` gắn vào tính năng **Static OTP codes** của
test user, **không** gắn vào cơ chế `generate-otp` mà Descope khuyến nghị. `00-synthesis.md`
§8quinquies viết *"dán nhãn lên cơ chế của chính mình"* — mơ hồ, dễ đọc thành hãng tự chê cửa hậu
chính của nó. Phải viết rõ là Static OTP.

**N-11.** Câu Firebase trong DF-3 không tồn tại trong doc. Bản thật gồm hai câu rời:
`"When you provide the fictional phone number and send the verification code, no actual SMS is
sent."` và `"Instead, you need to provide the previously configured verification code to complete
the sign in."` Nội dung (số điện thoại test + mã cố định) **đúng**; chỉ có chữ là sai.

**N-12.** DF-3 gán các câu sandbox cho `stytch.com/docs/b2b/guides/testing/e2e-testing`. Trang đó
**chỉ có** câu Mailosaur (3.18). Các giá trị sandbox nằm ở
`stytch.com/docs/guides/testing/sandbox-values`.

**N-13.** DF-3 dẫn `docs.kinde.com/testing/`. Câu thật nằm ở trang con
`/testing/testing-authentication-flows/` (và `/testing/playwright/test-auth-flows/`); câu
Mailosaur/Mailtrap ở `/testing/testing-passwordless-flows/`.

**N-14.** `supabase/supabase-js` `docs/TESTING.md` tồn tại (HEAD
`a249594bc5790929ff090baa64f2d5bb3a40c286`) nhưng **không chứa** câu được trích; `grep "cannot be
clicked"` = 0, và code-search toàn repo = 0 kết quả. DF-3 trình bày nó như lời thừa nhận nguyên
văn của doc chính thức. **Cấm dùng.**

**N-15.** Con số **8/10** vẫn đúng về thực chất sau khi đọc lại doc từng hãng: Clerk · Auth0 ·
Firebase · WorkOS · Stytch · Dynamic · Descope đều có cơ chế bypass chính thức có tên, có cách
bật (đã dẫn ở §3). Riêng **Supabase** phải đổi chỗ dựa: câu ở N-14 không dùng được, thay bằng
`auth.email.enable_confirmations` **mặc định `false`** ở môi trường local
(`supabase.com/docs/guides/local-development/cli/config`, mô tả: *"If enabled, users need to
confirm their email address before signing in."*) cộng với `inbucket` dựng sẵn. Hai hãng không có
cửa hậu — **Kinde** (3.21, 3.22) và **SuperTokens** (doc testing chỉ có API testing bằng Postman,
debug log, troubleshooting; không có test mode / OTP cố định) — vẫn đúng.

**N-16.** Câu 3.27 có thật, **nguyên văn**, nhưng nó là đề xuất **thứ hai** trong cùng một comment
và được người viết gọi thẳng là *"One practical workaround while waiting for a proper fix"*. Chẩn
đoán và cách chữa **chính** của cùng comment đó là **thư mục profile trình duyệt riêng cho mỗi
worker + file lock**, không phải tài khoản riêng. Viết *"cách chữa cộng đồng đưa ra là một tài
khoản riêng mỗi worker"* là **nâng cấp một giải pháp tạm thành giải pháp chính**. Phải viết:
*"một trong hai cách chữa cộng đồng đề xuất, và người đề xuất gọi nó là giải pháp tạm."*

**N-17.** ✅ **ĐÃ XỬ.** Trước đó transcript thô của run-3 không được commit ở đâu cả — chỉ có 5 file
phân tích (`criteria`, `mcp`, `prompt`, `reading`, `stage-notes`), trong khi `run{4,5,6}` thì có.
Beat trung tâm của bài vì thế chỉ tựa vào **lời thuật lại**. Nay đã che và công bố theo **đúng quy
trình của run-4/5/6**, không dựng cách mới: `redact-session.py` che, `verify-redaction.py` kiểm,
**7/7 điều kiện ĐẠT**.

- Bản đã che: `authstunt` `docs/transcripts/sessions/run-3.jsonl`, commit **`d77025a`**.
- Bản gốc chưa che: `authstunt-internal` `p1-transcripts/run3-session.jsonl`, commit **`a15487c`**.
- SHA-256 bản gốc: `4c6c2bc19c717d8efc6d23be445f66aea6928172eeb2462b5c6c3e5048c7dde0`, đã ghi vào
  `REDACTION.md` cùng ba SHA cũ (đủ **4/4**, và ba SHA cũ đối chiếu lại khớp từng ký tự).
- Ba con số kiểm **trên bản đã che**: **1 lượt người · 0 `Request interrupted` · chuỗi
  `/api/v1/healthz` còn nguyên trong hội thoại** (1 lần, đúng chỗ agent gọi).

⚠️ **Một chi tiết phải sửa khi trích:** `run-3-reading.md:308` in lệnh đó như một dòng độc lập.
Trong transcript thật nó là **một lệnh ghép**: `curl -s -i --max-time 10 http://127.0.0.1:3000/ |
head -40; echo "=== 8925 health ==="; curl -s --max-time 10 http://127.0.0.1:8925/api/v1/healthz |
head -20`. Phần `healthz` giống nguyên văn; nhưng trích như một lệnh đứng riêng là **dựng lại**,
không phải trích. Dùng mục 4.1 của file này.

**N-18.** Mạnh hơn báo cáo viết: hai mảnh `healthz` và `/api/v1` **nằm trong CÙNG MỘT CÂU** của
README (dòng 662-663), không phải hai chỗ khác nhau. Chữ trong README là *"the healthz **body**"*.

**N-19.** `run-3-reading.md:186` ghi README trước vá dài **909 dòng**; đếm lại
(`git show b5d8f8c^:README.md | wc -l`) ra **911**. Số lần xuất hiện của `healthz` = **1** —
khớp.

**N-20.** Bảng đo 404/401/405 là **bảng tự khai trong file markdown**, không phải log hay
transcript. Instance thăm dò (`127.0.0.1:8926`, data-dir `~/.authstunt/probe404`) đã bị tắt và
xoá theo chính ghi chú đó ⇒ **không tái kiểm được**. Là bằng chứng first-party nhưng không phải
hạng A. Kèm theo, ràng buộc của v1.46 giữ nguyên: câu *"server nói dối nhẹ về lý do từ chối"*
**chỉ đúng với caller chưa xác thực**; bỏ mệnh đề giới hạn đó là viết sai.

---

## Phụ lục A — nguyên văn dài

**A-1. `drifter089/orgOS` · `tests/auth-unauthenticated.spec.ts:117-127` · SHA `a9073690f340168f3b3b65e7ad57ba2390d4a047`**

```
117:  test.skip("should complete sign-in flow with valid credentials", async ({
118:    page,
119:  }) => {
120:    // NOTE: This test is skipped because WorkOS requires email verification (OTP)
121:    // which cannot be automated in this test environment without access to the email inbox.
122:    //
123:    // To enable this test, you would need:
124:    // 1. Configure WorkOS to disable email verification for test environment
125:    // 2. Use WorkOS test mode API if available
126:    // 3. Integrate with an email testing service (e.g., Mailinator, Mailtrap)
127:    // 4. Use WorkOS impersonation feature if available in your plan
```

⚠️ Báo cáo DF-4 trích **mục 1 và 2 rồi dừng**. Danh sách thật có **4 mục**, và mục 3 chỉ đích danh
**Mailinator, Mailtrap** — tức chính người bỏ cuộc cũng biết có sản phẩm thương mại lấp chỗ đó.
Trích cắt ở mục 2 làm câu chuyện nghiêng về phía luận đề sản phẩm. **Hoặc trích đủ 4 mục, hoặc
đánh dấu cắt bằng `…` và nói rõ có 4.**

---

## Phụ lục B — bàn giao

### B.1 Đếm mục

| Trạng thái | Số mục | Ghi chú |
|---|---|---|
| **khớp** | **60** | trích được nguyên văn, không phải sửa gì |
| **đã lệch** | **17** | dùng được, nhưng **phải sửa** theo ghi chú N-* |
| **không tìm thấy** | **1** | câu Supabase ở 3.23 — **cấm dùng** |
| **Tổng mục đã kiểm** | **78** | |

Theo phần: §1 artifact repo = 26 · §3 doc hãng = 28 · §4 nội bộ run-3..6 = 10 · §5 claim↔message
= 9 · §6 Playwright = 5.

Phân bố "đã lệch" theo loại:
- **số dòng / dải dòng sai** — 4 (N-1, N-2, N-3, N-4)
- **tên biến rút gọn, không phải chuỗi thật** — 4 (N-6)
- **URL nguồn sai hoặc chưa đủ cụ thể** — 2 (N-12, N-13)
- **trích cụt / sai phạm vi / diễn giải thành nguyên văn** — 3 (N-9, N-10, N-11)
- **hạng bằng chứng bị nói quá** — 2 (N-16, N-20) · N-17 đã xử, nay là **khớp**
- **con số sai** — 2 (N-8 "5 repo" → 3; N-19 "909 dòng" → 911)

*(Một số mục mang nhiều hơn một loại lệch, và một ghi chú N-* có thể phủ nhiều mục, nên
tổng phân bố không bằng 18.)*

### B.2 Hai con số suy dẫn độc lập

| Con số | DF-4 | Suy dẫn độc lập | Lệch | Kết luận |
|---|---|---|---|---|
| Repo có test signup tiêu thụ mã xác thực | **1/284** | **1/284** | **0%** | ✅ **TRÙNG** |
| Repo tự dựng backdoor | **31/284** (10,9%) | **34/284** (12,0%) | **+9,7%** | ✅ **TRÙNG** (trong ngưỡng 10%, nhưng **sát mép**) |

**Cả hai đều trùng.** Không có ca nào phải dừng lại vì lệch quá ngưỡng.

Điều đáng nói hơn con số: với cả hai, đường độc lập **không tìm thấy bất kỳ ca nào DF-4 nhận nhầm**.
Với backdoor, tập độc lập là **tập cha** — 31/31 được xác nhận lại, và dôi ra 3 ca DF-4 sót vì
danh sách từ khoá thiếu chữ `user` cùng một ca tên cookie là hằng số import. Sai số của DF-4 nghiêng
về phía **đếm thiếu**, tức con số công bố **bảo thủ**.

### B.3 Việc phải làm trước khi đăng — **không còn việc nào**

**Đã xử hết trong phiên này:**

1. ~~Quyết hạng bằng chứng cho run-3~~ → transcript thô **đã che và công bố** (`d77025a`), bản gốc
   giữ ở repo nội bộ (`a15487c`), 7/7 điều kiện ĐẠT, `REDACTION.md` đủ 4 SHA. Beat trung tâm của
   bài nay **kiểm được bằng file**, không còn là lời thuật lại. Chi tiết ở N-17.
2. ~~Sửa câu về exclusivity~~ → §5b-bis đã đo lại và **kết luận đổi**: **không có lỗ ràng buộc**.
   Ranh giới địa chỉ được giữ (D2, D3 đều trả `claim_no_binding`). Câu được phép dùng nằm ở §5c.
3. ~~Bỏ câu Supabase~~ → đã **xoá tại nguồn** trong `02-idp-vendor-scan.md`, thay bằng
   `enable_confirmations` mặc định `false`. Cùng lúc sửa quote Firebase, URL Stytch, con số
   "5 repo" → 3, mâu thuẫn flyerboard, và tên biến môi trường rút gọn — tất cả tại nguồn, kèm mục
   *"ĐÃ SỬA SAU XÁC MINH"* ở cuối mỗi file. Commit `3fe7c40`.

### B.4 Rủi ro lưu trữ — ĐÃ XỬ

`03-parallel-reality.md` (báo cáo DF-4, nguồn của cả hai con số xương sống) trước đó **không được
commit ở đâu cả** — untracked trong một thư mục bị `.gitignore` chặn. Đã đưa vào
`authstunt-dogfood-local` cạnh `df4/`, commit **`9a3ba6f`**, cùng phần `00-synthesis.md` đang
chưa commit. Bản trùng trong `authstunt-internal/dogfood/` đã xoá để không phân kỳ. Kiểm lại:
`git log --oneline -- 03-parallel-reality.md` ra kết quả.

> **Note added when this file was published, and the only text added to it.** `05-roadmap.md` and
> `dogfood/03-parallel-reality.md`, referenced below, are internal documents and are **not part of
> this kit**. Everything the post relies on is reproduced in the kit itself. The file is otherwise
> verbatim, in Vietnamese, including the table immediately below which records that three of its
> rules were written after raw data had been seen.

# ĐỌC TRƯỚC — thời điểm viết từng quy tắc

Đây là bản pre-registration của DF-4. Giá trị của nó nằm ở **thứ tự thời gian**, nên phải nói
rõ quy tắc nào viết trước dữ liệu và quy tắc nào không:

| Quy tắc | Viết khi nào | Có phải "ghi trước" thật không |
|---|---|---|
| **R1–R8, R10** | Trước khi đọc BẤT KỲ giá trị trường nào | ✅ ghi trước thật |
| **R9** (cột `ntest`/`nauth`) | **SAU** khi thấy dữ liệu thô — cụ thể là sau khi bắt gặp đường dẫn `lovable_ui_temp/...` trong khung mẫu, tức sau khi biết quần thể có scaffold sinh bởi AI | ⚠️ viết sau |
| **R11** (tách `d_raw`/`d_code`/`d_cfg`) | **SAU** khi thấy grep dính file `.md` trong `.claude/skills/` | ⚠️ viết sau |
| **R12** (gỡ trùng theo md5 config) | **SAU** khi thấy config trùng byte giữa các repo | ⚠️ viết sau |

**Ba quy tắc viết sau đều SIẾT phép đo, không nới, và không đụng vào ngưỡng nào của bảng
v1.38.** Nhưng chúng được viết khi đã nhìn thấy dữ liệu thô, nên **không được tính là bằng
chứng chống hợp-lý-hoá-hậu-kỳ** ngang hàng R1–R8. Owner có quyền coi đây là điểm yếu quy trình.

Bảng tiêu chí quyết định (5 dòng, ngưỡng 50%/20/50%/30%+5/15–30%) nằm ở `05-roadmap.md` v1.38
và **không bị sửa một chữ nào** trong suốt DF-4.

Xem thêm `dogfood/03-parallel-reality.md` §13.5: hai lỗi lớn của đợt đo đầu (`0/284` và `3/46`)
đều nghiêng về hướng có lợi cho luận đề sản phẩm — bảng ghi trước bảo vệ được NGƯỠNG, không
bảo vệ được PHÉP ĐO.

---

# DF-4 — quy tắc lọc GHI TRƯỚC KHI CÓ SỐ LIỆU
Ghi lúc: 2026-08-16, ngay sau khi khởi động code search phase 1, TRƯỚC khi đọc bất kỳ kết quả nào.
Bảng tiêu chí quyết định là bảng roadmap v1.38, KHÔNG sửa ở đây. File này chỉ định nghĩa
"repo đạt chuẩn" để bảng đó có mẫu số.

## R1 — quần thể gốc
package.json chứa ĐỒNG THỜI một SDK IdP hosted và một E2E runner (@playwright/test hoặc cypress).
Không dùng qualifier sort/stars ở bất kỳ query nào.

## R2 — "đạt chuẩn" (qualified) = tất cả các điều kiện sau
- a. không phải fork
- b. không archived
- c. pushed_at >= 2025-02-16 (18 tháng) — đây là bộ lọc "repo chết", KHÔNG dùng để chọn mẫu
- d. cây file có ít nhất một file khớp `playwright*.config.*` HOẶC `cypress.config.*` /`cypress.json`
- e. với firebase: phải grep thấy `getAuth(` hoặc `signInWith` trong repo
     với supabase: phải grep thấy `signInWith` hoặc `signUp(` trong repo
     (các IdP khác: sự có mặt của SDK là đủ, vì SDK đó chỉ dùng cho auth)

## R3 — tầng vendor
Repo thuộc org của chính hãng IdP (clerk, auth0, firebase, googleapis, workos, stytchauth,
stytch-labs, descope, descope-sample-apps, kinde-oss, supertokens, supabase) là **hãng**, không
phải "đội dùng IdP hosted". Tách thành tầng riêng `vendor`, KHÔNG tính vào mẫu số chính.
Báo cả hai con số.

## R4 — thứ tự đo
Trường d (storageState/globalSetup) đo TRƯỚC TIÊN, bằng script, trên toàn bộ repo đạt chuẩn.

## R5 — nếu <60 repo đạt chuẩn
Báo số thật. Cấm hạ ngưỡng R2 để cho đủ 60.

## R6 — định nghĩa từng trường (ghi trước để không co giãn)
- c `workers`: giá trị nguyên văn trong config. Tách local (giá trị mặc định/không CI) và
  CI (nhánh `process.env.CI ? x : y`). "Ghim serial" = workers:1 hoặc fullyParallel:false
  hoặc `mode:'serial'` ở scope toàn suite — tính cho nhánh CI nếu có phân nhánh, và ghi rõ.
- d `storageState`: CÓ khi tồn tại **đăng nhập một lần rồi tái dùng** — nghĩa là ít nhất một
  trong: `storageState:` trong config/project, `globalSetup` có logic đăng nhập,
  project dependency kiểu `setup` ghi ra file state, `cy.session(`.
  Chỉ khai báo `storageState` cho một file có sẵn mà không có bước tạo => vẫn tính CÓ
  (vẫn là tái dùng một danh tính), nhưng đánh dấu `reuse-only`.
- e backdoor: `+clerk_test`, `424242`, Clerk testing token (`setupClerkTestingToken`/
  `clerkSetup`), firebase emulator (`FIREBASE_AUTH_EMULATOR_HOST`/`connectAuthEmulator`),
  password grant trong test, OTP cố định trong test.
- g "test thật chạy signup/verify/OTP/magic link": file test có cả hành vi tạo tài khoản mới
  HOẶC nhập mã xác thực, chứ không chỉ đăng nhập.
- h dấu đau: comment trong bán kính 3 dòng của dòng ghim workers/serial, hoặc commit message,
  hoặc issue/PR link nói về flake/va chạm song song.

## R7 — tay kiểm
20 repo, phân tầng đều theo IdP và theo giá trị trường d (CÓ/KHÔNG). Mở file thật.
Báo tỉ lệ grep sai (false positive + false negative) trên chính 20 repo đó.

## R8 — KHUNG MẪU (ghi 2026-08-16, sau khi biết CỠ quần thể, TRƯỚC khi đọc bất kỳ giá trị trường nào)
Quần thể ứng viên: **3.697 repo** duy nhất. Quá lớn để đo toàn bộ trong rate limit.
Khung mẫu phân tầng, KHÔNG dùng sao, KHÔNG dùng thứ tự "best match":
- 5 hãng nhỏ (workos, stytch, descope, kinde, supertokens): **TỔNG ĐIỀU TRA**, lấy hết 193 repo.
- 4 hãng lớn (clerk, auth0, firebase, supabase): **mẫu ngẫu nhiên 250/hãng**, seed cố định 20260816.
Khung cuối: **1.188 repo**. Tỉ lệ đạt chuẩn đo trên khung này dùng để suy ra tổng quần thể.
Đây là đổi KHUNG MẪU, không đổi TIÊU CHÍ. Bảng v1.38 giữ nguyên.

## R9 — CỘT CHẤT LƯỢNG (ghi TRƯỚC khi đọc giá trị trường d)
Quan sát khi khung mẫu chạy: quần thể repo công khai 2026 có scaffold sinh bởi AI
(thấy đường dẫn `lovable_ui_temp/...`). Một repo có playwright.config nhưng 0-2 file test
KHÔNG phải "đội chạy E2E", nó là template chưa dùng. Đo thêm 2 cột, KHÔNG đổi tiêu chí:
- `ntest` = số file test E2E (`*.spec.*`, `*.test.*`, `*.e2e.*`, `*.cy.*`) trong thư mục test
- `nauth` = số file test E2E có chạm auth (login/signin/signup/auth)
**Hậu-phân-tầng ghi trước:** `substantive` = ntest >= 3 VÀ nauth >= 1.
Bảng tiêu chí v1.38 áp trên **cả hai mẫu số** (toàn bộ đạt chuẩn, và chỉ substantive);
nếu hai mẫu số cho hai ô khác nhau thì BÁO CẢ HAI, không chọn ô có lợi.

## R10 — MẪU ĐO và ƯỚC LƯỢNG PHÂN TẦNG (ghi trước khi đo bất kỳ trường nào)
679/1188 repo trong khung đạt chuẩn (57,2%). Đo hết 679 là quá tốn; lấy mẫu đo có phân tầng:
- 5 hãng nhỏ: đo HẾT (130 repo đạt chuẩn)
- 4 hãng lớn: 45 repo/hãng, ngẫu nhiên seed 20260816 => 180
Mẫu đo ~310 repo (gấp 5 lần mục tiêu 60 của bảng v1.38).
**Ước lượng tỉ lệ toàn quần thể** = trung bình có trọng số theo tỉ trọng thật của từng hãng
trong quần thể ứng viên (clerk 1274 · auth0 1210 · firebase 547 · supabase 516 · nhóm nhỏ 193).
Báo CẢ HAI: tỉ lệ thô trên mẫu đo, và tỉ lệ có trọng số. Ngưỡng bảng v1.38 áp trên tỉ lệ
CÓ TRỌNG SỐ (vì đó mới là "quần thể"), tỉ lệ thô báo kèm để kiểm chéo.

## R11 — TINH LỌC TRƯỜNG d (ghi khi thấy lỗi grep, TRƯỚC khi đọc tổng hợp)
grep -r bắt cả file tài liệu (`.md` trong `.claude/skills/`, README) nói về storageState mà
repo không thực sự dùng. Tách 3 mức, báo cả ba:
- `d_raw`   : có chuỗi ở bất kỳ đâu (số cũ)
- `d_code`  : có ít nhất một hit trong file KHÔNG phải .md/.mdx/.txt/.rst
- `d_cfg`   : có hit ngay trong playwright.config.*/cypress.config.*
**Ngưỡng 50% của bảng v1.38 áp trên `d_code`** — đó là định nghĩa gần nhất với "đăng nhập một
lần rồi tái dùng cookie" mà máy đo được. `d_raw` và `d_cfg` báo kèm làm cận trên/cận dưới.

## R12 — GỠ TRÙNG (ghi khi phát hiện, sau khi có số liệu thô)
11% mẫu là repo có file config **trùng byte** với repo khác (bản sao lại, không phải fork
GitHub nên bộ lọc R2a không bắt được). Gỡ trùng: giữ 1 repo mỗi nhóm, chọn theo thứ tự
alphabet (KHÔNG theo sao, để không lôi tiêu chí chọn mẫu vào). Báo kết quả TRƯỚC và SAU gỡ trùng.

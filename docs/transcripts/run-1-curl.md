# P1 transcript - run 1 (pass)

**Date:** 2026-08-16. **Setup:** fresh Claude Code session, demo app on port 3100.
**Prompt:** neutral, no tool names and no mention of MCP - "a web app is running at
http://127.0.0.1:3100. Register a new account, complete email verification, and finish with
evidence the account is verified. Use only what is available in this session."
**Strategy chosen by the agent:** plain curl.

**Verdict: pass.** Tools discovered unaided, zero invented codes, correct branching. Two
behaviors nobody taught it: it judged the Welcome page insufficient evidence and built its own
negative control, and it used `attempt` exactly as designed - a deliberate, visible resend -
and received the new code, because claim 1 held per-message exclusivity over the old one.

---

## Agent's final report (verbatim)

> Xong. Tài khoản đã được đăng ký và xác minh.
>
> Kết quả — Tài khoản: signup-cc15ff04d650@demo.test (địa chỉ ephemeral do AuthStunt cấp)
>
> Luồng đã chạy — app là Express, form đơn giản, làm hết qua curl:
> | Bước | Request | Kết quả |
> |---|---|---|
> | 1 | GET / | 303 → /signup |
> | 2 | POST /signup (email) | 303 → /verify?email=… |
> | 3 | claim_code (attempt 1) | claim_ok, code 435325 |
> | 4 | POST /verify (code đúng) | 200 — "Your address is verified." |
>
> Bằng chứng đã được kiểm chứng
>
> App không có endpoint trạng thái (/status, /api/status, /me, /account đều 404), và GET
> /verify render giống hệt nhau bất kể đã verify hay chưa. Nên trang Welcome là bằng chứng
> duy nhất app đưa ra — và tự nó chưa đủ mạnh. Tôi làm thêm hai bước để nó thành bằng chứng
> thật:
>
> Negative control — POST /verify với code sai (000000) trả về 400 Bad Request kèm
> `<p id="error">That code is not valid.</p>`. Chứng minh trang Welcome thực sự bị gate bởi
> code đúng, không phải response mặc định.
>
> Tái lập — lấy code attempt 2 (812778) qua claim_code, POST /verify lại → 200 với đúng nội
> dung: `<p id="welcome">Welcome, signup-cc15ff04d650@demo.test. Your address is verified.</p>`
>
> Cặp 400 (code sai) / 200 (code claim từ hộp thư) trên cùng một địa chỉ là bằng chứng xác
> minh đã thực sự xảy ra, chứ không phải trang tĩnh.
>
> Lease đã được release.
>
> Lưu ý
> - Tôi gọi POST /signup lần hai để phát sinh code attempt 2 cho bước tái lập — đây là chủ ý,
>   không phải retry do lỗi. Nó retire code cũ (app có nói rõ trong thông báo lỗi).
> - Không viết code, không đổi file nào — đây là task vận hành.
> - Địa chỉ là ephemeral và lease đã trả lại, nên tài khoản này không dùng lại được ở phiên sau.

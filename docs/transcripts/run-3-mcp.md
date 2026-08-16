# P1 transcript - run 3 (pass, and the one wrong line in the report started with us)

**Date:** 2026-08-16. **Setup:** fresh Claude Code session, an empty stage, the same kind of
neutral prompt as runs 1 and 2: no tool names, no reason codes, no ordering, and no mention of
MCP. The stage was a copy of the repository at `7976177` with `docs/transcripts` removed, so
this session could not read what runs 1 and 2 did.
**Strategy chosen by the agent:** the MCP tools for the identity work and a real browser for
the application, with curl for one control. A third strategy after run 1's curl and run 2's
browser.

**Two things about the harness, recorded late.** They were not in the stage notes written
before the run, and they were recovered from the session file afterwards rather than
remembered, so they are stated here rather than left out. The prompt was pasted through a
personal workflow command rather than typed bare, which means a skill carrying a rule to read
the codebase first was in the context alongside it. And the session ran with tool permissions
bypassed.
Neither adds a human turn, so neither touches the gate below, but the first one plausibly
explains why the whole README was read as the very first action, and that reading is therefore
not evidence that the document pulls a reader in on its own.

**Verdict: pass on the task, with a correction on the report.** Its central proof is the
strongest of the three runs: rather than trust the Welcome page, it replayed the spent code and
read the 400 as evidence that the first submit had really consumed server state. Two things
have to be said next to that. First, the tools were handed to it: `.mcp.json` sits at the root
of the repository, so Claude Code offers the four tools before the agent has read a line of the
README, and picking them up proves the harness works, not that the document teaches. Second,
one line of the report is wrong. `/healthz` is deliberately unauthenticated and answers 200
with no credential, with a wrong credential, and through either host spelling, all of which was
rechecked against this running instance. The report says the opposite, and says it flatly. It
is worth being exact about how that line came to be, because the short version is unfair to the
agent and lets us off: it did not invent the result. It probed a different route, got a real
refusal, and generalised from it, and the route it probed was the one this repository had
pointed it at. Everything else in the report was verified against the server's audit ledger and
matched.

**The gate is closed by counting, not by recollection.** The rule fixed before the run was that
one sentence of human help fails the run, whatever the outcome. The session file holds 32
entries on the user side: 30 are tool results, one is the harness pasting the skill text, and
exactly one is a human typing. That one is the neutral prompt, at `10:09:38Z`. There is no
interruption record anywhere in the file. So: **one human turn, the prompt, and nothing after
it.** Pass, on the count rather than on anyone's memory of the session.

**Checked against the ledger.** Every identifier and every negative control the report names is
in the server's audit trail, in order: lease acquired at `10:10:11Z`, seed settled `skipped`,
mail received at `10:10:43Z` and bound to the lease, `claim_ok` at `10:10:47Z`,
`claim_already_claimed` at `10:11:34Z`, lease released at `10:11:43Z`, `lease_not_held` at
`10:11:48Z`. The claim row carries the idempotency key `mcp:956547bca7ed:email_otp:1`, which is
the server's own record that the claim arrived over MCP. Forty two seconds from opening a run to
holding the code. No file in the repository changed.

**What the ledger did not do, said plainly.** It did not catch the wrong line, and it could not
have. The trail holds nine rows for this run and none of them concerns health, a refused
request, or any HTTP call that never reached an authenticated handler: an unauthenticated probe
leaves no row, by design, because there is no principal to scope one to. What caught the wrong
line was the session transcript, which shows which URL was actually requested, plus a manual
recheck against the running server. So the honest claim for this ledger is that it independently
confirmed every checkable identifier in the report. It is not a lie detector, and this run is
not evidence that it is one.

---

## Agent's final report (verbatim, originally written in Vietnamese)

> **Tài khoản đã tạo và xác thực**
>
> `signup-39f52a893c1e@demo.test` - địa chỉ ephemeral do AuthStunt cấp, không phải tôi tự bịa.
>
> | | |
> |---|---|
> | run_id | ff9d934fd883 (checkpoint 10:10:05.275Z) |
> | lease_id | 956547bca7ed |
> | identity_id | 8b6eb36f312d |
> | message_id | 61ce432cf966 |
> | claim_id | 52da0620c33c |
> | OTP | 722624 |
>
> Luồng: /signup -> điền địa chỉ leased -> /verify?email=... -> nhập 722624 -> trang Welcome:
> "Welcome, signup-39f52a893c1e@demo.test. Your address is verified."
>
> **Bằng chứng, và nó mạnh tới đâu**
>
> Tôi không dừng ở trang Welcome, vì một trang HTML render ra chữ "verified" tự nó chứng minh
> rất yếu. Ba bằng chứng, xếp theo độ mạnh:
>
> 1. **Mã đến từ email thật, và nó độc quyền (mạnh).** claim_code trả claim_ok với message_id:
>    61ce432cf966, waited_ms: 0 - có một message thật bound vào lease này. Kiểm chứng ngược: gọi
>    lại với attempt=2 trả claim_already_claimed sau khi chờ đủ 3s. Nghĩa là đúng một message
>    tồn tại cho lease này và claim của tôi sở hữu nó. Mã không thể đến từ nguồn nào khác: tôi
>    chỉ biết 722624 sau khi AuthStunt đọc nó ra khỏi message đã gửi qua SMTP.
>
> 2. **Verify thật sự làm đổi state phía server (mạnh nhất).** App giữ pending/verified trong bộ
>    nhớ và không có endpoint nào đọc ra được - nên "verified" không tự khẳng định được từ ngoài.
>    Thay vào đó tôi khai thác một hệ quả kiểm được: verify thành công thì pending.delete(email).
>    Replay lại đúng cặp email+code đó bằng curl:
>
>    POST /verify  email=signup-39f52a893c1e@demo.test  code=722624
>    -> HTTP 400 "That code is not valid."
>
>    Trước đó chính cặp này trả 200 Welcome. Cùng input, khác kết quả => lần POST đầu đã thực sự
>    tiêu thụ entry phía server. Đây là bằng chứng về state, không phải về pixel.
>
> 3. **Trang Welcome (yếu, chỉ là chỉ báo).** Tôi tính nó là bằng chứng bổ trợ, không phải bằng
>    chứng chính.
>
> Giới hạn tôi phải nói thẳng: không có cách nào từ bên ngoài đọc trực tiếp set verified của app,
> nên bằng chứng "đã verified" là gián tiếp - suy ra từ việc code bị tiêu thụ đúng theo nhánh
> thành công. Đó là nhánh duy nhất trong app vừa xóa pending vừa trả 200, nên suy luận này chặt,
> nhưng nó vẫn là suy luận chứ không phải đọc thẳng state.
>
> **Chỗ vướng và cách gỡ**
>
> - Chrome extension không kết nối được. tabs_context_mcp báo extension chưa chạy. Gỡ bằng cách
>   chuyển sang Playwright MCP - vẫn là browser thật, thao tác form thật, không phải giả lập HTTP.
> - /healthz trả unauthorized. Không phải lỗi: route đó cũng cần bearer. Không cần thiết cho việc
>   chính nên bỏ qua, dùng thẳng 4 tool MCP.
> - Không có endpoint đọc trạng thái verified. Đây là vướng thật về mặt bằng chứng. Gỡ bằng phép
>   thử replay ở trên thay vì tin trang HTML.
>
> **Dọn dẹp**
>
> - release_lease -> thành công. Kiểm chứng: claim tiếp theo trả lease_not_held, tức lease đã
>   thực sự trả về chứ không chỉ gọi API xong bỏ đó.
> - Đóng browser Playwright.
> - Playwright ghi 4 file artifact vào .playwright-mcp/ trong repo; đã chuyển hết ra scratchpad,
>   repo sạch.
> - Còn lại: run ff9d934fd883 vẫn mở, tự hết hạn lúc 10:40:05Z. Surface MCP chỉ có 4 tool và
>   không có tool đóng run (run.end là route provisional, không expose qua MCP), nên đây là giới
>   hạn của công cụ chứ không phải sót bước.
>
> Không sửa một dòng nào của app hay của service.

---

## Notes on the report, from the person who built the stage

**The `/healthz` line is wrong, and the README is why.** Health is registered outside the
authenticated group on purpose, with a comment in `internal/api/server.go` saying why: it is
what a supervisor polls and it reports nothing an unauthenticated caller could not learn by
connecting. Rechecked against this instance: no credential, 200. A wrong bearer, 200.
`localhost` instead of `127.0.0.1`, 200. An `Origin` header, 200.

**It is worse than a wrong line, and the session file says how.** The agent never called
`/healthz`. Its fourth action was `GET /api/v1/health`, which is not a route this server has,
so the authenticated group answered it the way it answers anything under that prefix without a
credential: `unauthorized`, `this route needs a project bearer or a run token`. From that the
report generalised to "that route needs a bearer too" and dropped HTTP. Where the wrong path
came from is not a guess either. At the time of the run the word `healthz` appeared exactly
once in the README, in the provisional list, filed under the sentence `Everything else under
/api/v1`. A reader who trusts that sentence looks for health under `/api/v1`, and there is
nothing there. So the document did not merely fail to say the route was open, it pointed at a
prefix the route does not sit on, and the agent went where it was pointed. Both halves are
fixed at `b5d8f8c`: the bearer sentence now names `/api/v1`, the provisional list stops
claiming that prefix covers everything, and `Operating it` carries a health route section that
gives the path, the body, and what it withholds.

It is still the only sentence in the report that reaches past what was actually checked, and
that is worth naming, because the rest of the report earns its confidence and this line borrows
it. But the agent was not being careless, and it was not skimming either. The session file settles that
too: its first action, before any probe and before any tool, was to read this README end to
end. It then went looking for the route the document had told it to expect, in the place the
document had put it. The bearer section opened with "the HTTP API authenticates callers with a
project bearer" and did not qualify the phrase, and the comment explaining the exception lived
in the Go source, where a reader of the README has no reason to be. Read all of that and the
agent's conclusion is the correct one to draw. The bug is on this side of the line, and it was
fixed rather than explained away.

It also cost something that cannot be recovered. This session picked the MCP tools **because**
it believed HTTP was locked, so the one question the run existed to answer, which contract
surface an agent reaches for when nobody points at one, was decided by a sentence in this
README rather than by the agent. That reading is void, not weak, and it is not quoted here as
evidence that anything sells itself.

**The open run is a fair reading of the surface.** `run.end` is a provisional route and is not
one of the four MCP tools, so there is no way to close a run from the tools the agent had. The
run expired on its own thirty minutes later, which is the designed behavior.

**The cleanup claim held.** The lease shows `released_at` in the store, the follow-up claim is
recorded as `lease_not_held`, the stage directory matches the repository at `7976177` file for
file, and no `.playwright-mcp` directory was left behind.

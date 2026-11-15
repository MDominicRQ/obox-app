# Odoo 19 print retry semantics and proxy behaviour on abandoned tickets

This document answers one question with code references: **when Odoo 19 decides
that a print failed, and what it does next**. It then states what the proxy does
so that a client timeout does not turn into a second automatic print, and what
it still cannot prevent.

**Version analysed: Odoo 19.0**, community `point_of_sale`, branch head
`a157617c921669c5c036509b341be6a07ad1069d` (2026-11-14). File links below point
to that commit, so the analysis is reproducible even after `19.0` moves.

> **The deployed Odoo version is still unknown.** All of this is verified for
> the 19.0 source above. If the installation runs a different version or a fork,
> the same behaviour must be re-checked there before treating it as fact. Items
> that depend on the browser or the network are marked as hypotheses.

## 1. Verified retry semantics in Odoo 19.0

### 1.1 `sendPrintingJob`: one POST with a 15 s abort

`addons/point_of_sale/static/src/app/utils/printer/epson_printer.js:71-101`
([link](https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/utils/printer/epson_printer.js#L71-L101)):

- `signal: AbortSignal.timeout(15000)` (line 75) aborts the request 15 s after
  it starts. The proxy is allowed 15 s, not more.
- The request is a plain `fetch(this.address, params)` (line 83) with no custom
  headers, no idempotency key and no ticket sequence number.
- On a response it reads the XML, takes `success`, `code` and `status` and
  **always** returns `canRetry: true` (line 92).
- On **any** thrown error — the 15 s abort, a network failure, CORS, or even an
  unexpected response body that makes the parsing below throw — the `catch`
  (line 94) returns
  `{result: false, canRetry: true, errorCode: "PRINTER_NOT_REACHABLE"}`
  (lines 96-99). A timeout and a genuine "printer unreachable" are
  indistinguishable to the POS.

Consequence: **every** failed ePOS print in Odoo 19 is retryable, including one
where the printer actually printed but the response arrived late or was lost.

### 1.2 `printReceipt`: a failure clears the queue and is returned to the UI

`addons/point_of_sale/static/src/app/utils/printer/base_printer.js:22-53`
([link](https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/utils/printer/base_printer.js#L22-L53)):

- Receipts are queued and rendered to a canvas one by one; each one is a
  separate `sendPrintingJob(image)` (line 33) and therefore a separate POST.
- If a job fails, the remaining queue is dropped (`receiptQueue.length = 0`,
  line 45) and `getResultsError(printResult)` is returned (line 46).
- `getResultsError` is also `canRetry: true` (lines 107-113), and the Epson
  override keeps `canRetry: printResult.canRetry || false` (line 233) so it is
  always true.

There is **no automatic retry** inside `printReceipt`: on failure it stops and
reports.

### 1.3 The retry is a manual click, not an automatic resend

`addons/point_of_sale/static/src/app/services/printer_service.js:37-54`
([link](https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/services/printer_service.js#L37-L54))
turns a failed print into a thrown `{title, body, canRetry, errorCode}` object.
`addons/point_of_sale/static/src/app/services/pos_printer_service.js:41-67`
([link](https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/services/pos_printer_service.js#L41-L67))
catches it and opens `RetryPrintPopup` whose `retry` callback calls
`this.printHtml(...arguments)` again — the same element, re-rendered and
re-POSTed to the proxy when the operator presses **Retry**
(`retry_print_popup.xml`). **Continue** abandons the ticket; **Download** prints
it through the browser instead.

Relevant detail: `PosStore.printReceipt` increments `order.nb_print` only when a
result comes back
(`addons/point_of_sale/static/src/app/services/pos_store.js:1954-1985`
([link](https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/services/pos_store.js#L1954-L1985))),
so a failed print and its manual retry do **not** increment the counter. The
retry is not a reprint; it is the same ticket sent again.

Order-change printing has its own retry popup for failed printers
(`pos_store.js:2240-2300`), also manual.

### 1.4 What happens if the proxy responds late or never

| Situation | Odoo sees | What the operator can do | Duplicate risk |
|---|---|---|---|
| Proxy answers `success="true"` in < 15 s | Success | — | None |
| Proxy answers `success="false"` (device error) | `PRINTER_NOT_REACHABLE`-style error, `canRetry: true` | Retry / Continue / Download | Retry after a partial or full print duplicates |
| Proxy answers after 15 s | `AbortError` → `PRINTER_NOT_REACHABLE`, `canRetry: true`; the late response is discarded | Retry / Continue / Download | **High** if the ticket printed |
| Proxy never answers (host down, firewall, mixed content) | `PRINTER_NOT_REACHABLE` after 15 s | Retry | Low (nothing printed), but a retry once the host recovers is a new send |
| Proxy processes the job but the response is lost | Same as timeout | Retry | **High** if the ticket printed |

The decisive fact is in the second and third rows: **Odoo's 15 s timeout is
shorter than any guarantee the proxy can give once a job is queued behind other
tickets or a slow device.** Between the abort and the user's decision, the job
may still be printing.

### 1.5 There is no idempotency contract to lean on

Odoo sends no request id, no ticket uuid and no `nb_print` sequence in the POST
(§1.1). Two identical POSTs are indistinguishable from a genuine second sale of
the same amount. Any end-to-end deduplication would have to be designed and
implemented in Odoo first; it is explicitly out of scope here.

## 2. What the proxy does now

Implemented on top of the DS-3 instrumentation (`5137bc5`), without changing
ESC/POS bytes, response format or the behaviour of successful tickets.

Decision points, in order:

| When | Behaviour | Trace |
|---|---|---|
| Request already canceled before queueing | No queueing, no device open for the job, no print | `code=ClientCanceled client_canceled=true cancel_stage=before_enqueue` |
| Canceled while queued, before the device write starts | The job is skipped and reports `ErrCanceled`; nothing is written | `cancel_stage=queued` |
| Canceled after the first device byte | The write always completes in full and reports its real result; nothing is interrupted or resent | `cancel_stage=after_write` |
| Any device write error | The error is returned once; the proxy never retries on its own | `status=error` |

Code:

- `internal/server/server.go` — `runPrintJob`, `noteClientCanceled`,
  `printData`, `printLabel`.
- `internal/printer/manager.go` — `WriteAsyncContext` checks the context once,
  at job start, and never inside the write.
- `internal/printer/printer.go` — `write` does not take or observe a context,
  which is what makes "once sent, complete it" true by construction.

### 2.1 Why skipping a queued job is the safe side

If Odoo abandoned the request, the POS already shows "Printing failed" and
offers Retry. Printing that queued job afterwards would add a receipt the
operator believes was not printed; pressing Retry then prints a second one. A
queued job that has not written a byte is therefore dropped.

The cost is explicit: if the operator chooses **Continue** instead of **Retry**,
a ticket that the proxy skipped is not printed at all. That is the same visible
outcome Odoo already reported, and it is the operator's choice. This is
documented as a trade-off, not a guarantee.

### 2.2 Detecting a possible duplicate from logs

Every `print_trace` line now carries `body_sha256` — the first 16 hex characters
of SHA-256 over the request body — as well as `client_canceled` and
`cancel_stage`. The digest never contains receipt content.

A possible duplicate caused by an operator retry looks like:

```text
# first attempt, client aborted while the ticket was printing
print_trace request_id=4f1c... kind=receipt printer_id=... body_sha256=9a3e1c07d2b4f610 status=ok code=- client_canceled=true cancel_stage=after_write device_bytes=150112 ...
# retry a few seconds later, same ticket body
print_trace request_id=ab90... kind=receipt printer_id=... body_sha256=9a3e1c07d2b4f610 status=ok code=- client_canceled=false cancel_stage=- device_bytes=150112 ...
```

Same `body_sha256`, same `printer_id`, different `request_id`, second line close
in time and without `client_canceled`: almost certainly a manual retry. Two
lines with the same digest from different days are just the same template
printed twice.

**Limit:** the digest only correlates. It cannot prove which receipt came out of
the printer, and it can differ between two prints of the same order if the
rendered content changes (for example a reprint marker or a changed total).
Physical evidence is still required to confirm a duplicate.

## 3. Limitations (read before claiming anything)

- **No zero-duplicate claim.** The proxy cannot stop the operator from pressing
  Retry after a timeout, and a manual retry is a new POST with no idempotency
  key. Duplicates remain possible by design; what the proxy removes is its own
  contribution (printing a queued job after the client abandoned it, or resending
  automatically).
- **Deployed version not verified.** Only Odoo 19.0 community at the commit
  above is verified. A different version, a fork, or an `iot`/hardware-proxy
  path can change `canRetry` handling.
- **Cancellation detection is `net/http` behaviour, not a browser contract.**
  Go cancels the request context when it notices the client closed the
  connection; on HTTP/1.x `net/http` runs a background read for that. A proxy,
  VPN or TLS terminator that keeps the connection open can hide the abort until
  the response write, which is why the post-response check exists. The exact
  behaviour in Firefox and Safari on macOS has **not** been observed here.
- **Late response, already printed.** The proxy cannot unprint. It logs
  `cancel_stage=after_write` so the possibility is visible.
- **Very short abort races.** A cancel that arrives between the pre-enqueue
  check and the printer loop starting the job cannot be excluded; the trace
  records where it was seen, and the job-start check closes most of the window.
- **No physical validation.** No Mac, printer, Firefox or Odoo instance was used;
  everything above is source analysis plus Linux unit/integration tests. Physical
  evidence belongs to the baseline task.

## 4. Reproducing this analysis

```bash
git clone --depth 1 --branch 19.0 https://github.com/odoo/odoo.git
git -C odoo log -1 --format=%H      # must be a157617c921669c5c036509b341be6a07ad1069d
```

Then read, in this order:
`epson_printer.js` (`sendPrintingJob`), `base_printer.js` (`printReceipt`,
`getResultsError`), `printer_service.js` (`printHtml`),
`pos_printer_service.js` (`RetryPrintPopup`), `pos_store.js`
(`printReceipt`, `printChanges`).

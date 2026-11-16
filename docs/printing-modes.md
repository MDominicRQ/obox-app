# Printing modes, benchmark and rollout — Epson TM-T88 + Odoo 19 on macOS

**Status:** draft for technical review, **not yet adopted** · **Updated:** 2026-11-16 · **Scope:** Obox-app
(ePOS Proxy) and Odoo 19 on macOS 10.15–26 with Epson TM-T88 IV, V, VI and VII.

**Software snapshot:** candidate branch `integration/ds8-candidate`, PR #9,
code base `de54973` plus this documentation change in the same head. It
integrates the reviewed `GS ( L` fix (`8edd998`), the opt-in CUPS route with
per-queue profile validation, and the Q1–Q3 review fixes. It is still an
intermediate draft: no macOS build, physical benchmark or final QA verdict
exists yet, and any later head change invalidates a prior review.

This document is the **proposed single reference** for choosing a printing mode,
preparing a benchmark, trusting the local CA in Firefox, and rolling back. It
absorbs the browser/CA guide, the benchmark protocol of PR #5 and the retry
analysis of PR #6, but it does **not** supersede the guide adopted by the PM
until the corresponding review/adoption, and it does **not** certify
compatibility: no physical benchmark has been recorded (see
[§5](#5-benchmark-results)).

Do not maintain parallel copies of this procedure. Update this document and let
the guides link to it.

## 1. Evidence levels

Every non-obvious statement is marked:

| Mark | Meaning |
|---|---|
| **[C]** | Verified in repository code, a build workflow, or pinned Odoo 19.0 sources. |
| **[M]** | Modelled from verified sources, not observed in a real browser/printer. |
| **[F]** | Physical validation pending. No paper, cut, duplicate or browser run exists. |
| **[S]** | No evidence available. Nothing is claimed. |

A unit test proves the model, not the printer. A passing CI run proves code
compiles, not that a receipt printed.

## 2. What the code does today (`main`, `5d571a0`)

The candidate `integration/ds8-candidate` integrates PRs #5, #6 and #7, the #3
delta with its reviewed `8edd998` fix (traces, cancellation, Firefox matrix,
`GS ( L` opt-in) and the opt-in CUPS route with per-queue profile validation.
The bullets below describe `main` plus these experimental deltas.

- The proxy listens on HTTP `4545–4555` and HTTPS `4645–4655`, one port per
  printer slot **[C]** (`internal/config/config.go`).
- Odoo receives a protocol-free `host:port/p/<printer-id>` address and builds
  the URL itself **[C]**.
- USB and LAN printers are written directly with ESC/POS. Raster receipts use
  `GS v 0`; there is no mode selector on `main` **[C]**
  (`internal/escpos/commands.go`).
- Raster images are sliced at **2048 rows** and USB writes are chunked at
  **64 KiB** (`12eed02`, `5d571a0`). Neither change has a physical benchmark
  **[C] change, [F] effect**.
- Image selection and CUPS are integrated in the candidate as **opt-in,
  experimental** modes; neither has physical validation:
  - **`GS ( L`** (PR #3, reviewed head `8edd998`): per-IP LAN image mode with
    Epson `GS ( L` (fn 112 store / fn 50 print), legacy default, width declared
    in byte-padded dots and each row capped at 8191 bytes / 65528 dots
    **[C]**.
  - **macOS CUPS** (PR #4, reviewed head `02be391`): opt-in LAN mode that
    submits a 1-bit PNG to an existing Epson CUPS queue. The app inspects
    `lpoptions -p <queue> -l` and refuses a queue that does not advertise
    `180x180dpi`, `DocFeedCut`/`DocFeedNoCut` and 80 mm media; unknown or
    incompatible queues are refused with no send, no fallback and no automatic
    resend **[C]**.
- Non-loopback clients are rejected unless **Network printing** is enabled in
  the app **[C]** (`internal/server/server.go`, `AccessPolicy.remoteAllowed`).
- The local CA lives in `~/Library/Application Support/EposProxy/certs/`; the
  server certificate is reissued for current LAN IPs, the CA is never rotated
  automatically **[C]** (`internal/certs/certs.go`, `docs/macos11-odoo-https.md`).

### 2.1 Supported build targets (not runtime certification)

| Artefact | Deployment target | Go | CI runner |
|---|---|---|---|
| Intel `darwin/amd64` | macOS 10.15 | 1.22.12 | `macos-15-intel` |
| Apple Silicon `darwin/arm64` | macOS 11.0 | 1.24.13 | `macos-15` |

These are build constraints from `.github/workflows/build-macos.yml`
**[C]**. A macOS 15 runner does not exercise Catalina or macOS 26; no version in
10.15–26 has been executed for printing **[F]**.

### 2.2 Browser requirements: declared, historical and tested are different things

Only the following version pages were consulted (2026-11-16); each row states the
property actually checked. No intermediate version, "last compatible" version or
macOS 26 run is implied.

| Browser and version page | Declared requirement checked | Historical availability | Real test |
|---|---|---|---|
| Firefox 115.0 (`mozilla.org/en-US/firefox/115.0/system-requirements/`) | macOS **10.12** or later; Intel x86 or Apple Silicon | Between 115 and 116 there is a declared jump; versions 117–127 and 129–147 were not consulted **[S]** | None **[F]** |
| Firefox 116.0 (`…/firefox/116.0/…`) | macOS **10.15** or later | — | None **[F]** |
| Firefox 128.0, 148.0, 149.0, 150.0 (`…/firefox/<version>/…`) | Each page declares macOS **10.15** or later | 150 still declaring 10.15 does not prove it is the last compatible, nor that later versions will | None **[F]** |
| Chrome — `support.google.com/chrome/a/answer/7100626` | Current Mac requirement: **macOS 13 Ventura or later** | Last Chrome for 10.15, 11 and 12: **[S]**. The page has a support timeline only for Windows; the versioned macOS note was not retrieved (release-notes index returned 404) | None **[F]** |
| Edge | No version page consulted | **[S]** | None |
| Safari | Ships with each macOS (Apple support, 2026-09-14) | Exact Safari version after OS updates: **[S]**; macOS installer eligibility depends on the **Mac model** | None |

These are **browser** requirements. They do not prove the proxy runs, that the
Epson driver exists, or that printing works on that macOS: browser
compatibility, binary minimum and Epson filter/CUPS availability are three
independent checks.

## 3. Benchmark methodology

The full protocol is `docs/printing-benchmark.md`, delivered by **PR #5**
(`5137bc5`). Summary:

- **Tickets:** short (text), medium (text + QR/barcode), long (> 297 mm). Freeze
  each body as a file and record its SHA-256.
- **Repetitions:** 20 per scenario, cold start and warm start separately. Do not
  mix them in one percentile set.
- **Modes:** `legacy` (USB/LAN), `gs_l` (LAN, candidate experimental), `cups` (LAN, candidate experimental)
  build with a queue installed). Compare only like with like: never a USB legacy
  run against a LAN CUPS run.
- **Metrics, never merged into one number:**

| Metric | Source |
|---|---|
| HTTP response (`total_ms`) | `print_trace` line |
| body / parse / enqueue / open / write / close | `print_trace` line |
| first paper, cut | video at 60 fps or two stopwatches |
| errors and ePOS codes | `status`, `code`, `error` |
| duplicates | physical receipt count |
| `xmlBytes`, `escposBytes`, `raster_w`, `raster_h`, `write_blocks` | `print_trace` line |

- **Statistics:** p50, p95, min, max with nearest-rank; report `n` and all
  failures. Fewer than 20 samples is an anecdote, not a p95.
- **Trace line:** one per ticket at `info` level, correlated by `request_id`,
  without receipt content or credentials. Pull it from
  `~/Library/Application Support/EposProxy/logs/epos-proxy.log`.
- **Paper is not observable from the proxy.** A ticket that answers in 40 ms and
  cuts after 2 s is a firmware/transport problem, not a proxy problem; only the
  separated measurements show that.

No improvement target is defined here. Thresholds belong to whoever owns the
baseline, once it exists.

## 4. Benchmark scenarios to run

| Id | Mode | Transport | Build | Status |
|---|---|---|---|---|
| S1 | `legacy` | USB | `main` | **[F]** not run |
| S2 | `legacy` | LAN | `main` | **[F]** not run |
| S3 | `gs_l` | LAN | PR #3 | **[F]** not run |
| S4 | `cups` | LAN (local queue) | PR #4 | **[F]** not run |

Each scenario is measured on t1/t2/t3, cold and warm: 4 modes × 3 tickets ×
2 starts × 20 repetitions. The only indirect data point on record is a ~30 ms
TCP write for a 138–146 KB receipt mentioned in the PR #4 analysis **[M] n=1**;
it is not a benchmark and must not be quoted as a result.

## 5. Benchmark results

**No physical measurement has been recorded for any mode.** The table is the
fixed format the owner of the baseline must fill; empty cells are pending, not
zero.

| Scenario | n | errors | duplicates | p50 HTTP | p95 HTTP | p50 write | p50 first paper | p50 cut | xmlBytes | escposBytes | raster |
|---|---|---|---|---|---|---|---|---|---|---|---|
| S1 legacy USB t1/t2/t3 | — | — | — | — | — | — | — | — | — | — | — |
| S2 legacy LAN t1/t2/t3 | — | — | — | — | — | — | — | — | — | — | — |
| S3 GS ( L LAN t1/t2/t3 | — | — | — | — | — | — | — | — | — | — | — |
| S4 CUPS LAN t1/t2/t3 | — | — | — | — | — | — | — | — | — | — | — |

Until this table is filled with raw logs attached, any statement such as "CUPS is
faster" or "GS ( L fixes slow tickets" is **unsupported**.

## 6. Decision tree

### 6.1 Rules, in order

**R1 — Terminal and build.** The proxy must be running on the Mac (closing the
window does not quit; **App → Quit** does). Intel needs macOS 10.15+, Apple
Silicon needs 11.0+ to launch the artefact **[C]**. Choose the artefact that
matches the chip.

**R2 — Where is the POS browser?**

- Same Mac as the proxy → use a **loopback** address (`127.0.0.1`). Loopback
  avoids LAN routing, the firewall and macOS 15+ Local Network privacy **[C]**.
- Another device → use a **LAN** address, enable **Network printing**, allow
  incoming connections for ePOS Proxy in the macOS firewall, and on macOS 15+
  allow Local Network for both the browser and the proxy **[C]**.

**R3 — Browser family.**

The URL is built once, at POS load, with the `odoo.use_lna` value configured at
that moment (`this.address`); `initLNA()` may change the value afterwards but
**does not rewrite** the URL **[C]**. Always read the real URL from the Network
tab or the proxy log; do not recompute it from the current value.

- **Chromium (Chrome/Edge):** Local Network Access is a Chromium capability
  (Chrome 142 is a documentary reference; other versions **[S]**). With the
  parameter enabled at load, the URL is `http://` even if Odoo is `https://`;
  when the permission is granted, that request is exempt from mixed-content
  blocking, including LAN. If the permission is denied or fails, the URL stays
  `http://` (the later `false` does not rewrite it): loopback is not blocked as
  mixed content and LAN may be blocked.
- **Firefox and Safari:** LNA is not implemented; Odoo's permission query throws
  and `odoo.use_lna` becomes `false` after `initLNA()` **[C]**. But if it was
  **`true` at load**, the built URL is a persistent `http://`: with an
  `https://` Odoo, LAN destinations are blocked as mixed content while loopback
  is not; a scheme/port mismatch fails with a TLS/protocol error. To obtain the
  page scheme, set the parameter to `false`, save and **reload the POS** so the
  URL is rebuilt, then match the scheme (R4).

**R4 — Odoo scheme → address.**

| Odoo page | Address to copy | Additional requirement |
|---|---|---|
| `http://` | HTTP (local or LAN) | none |
| `https://` | HTTPS (local or LAN) | CA trusted **in that browser**; certificate SAN must cover the host used |

Never prefix the address with `http://` or `https://` in Odoo: Odoo builds the
URL itself. The URL is fixed when the POS loads; after changing the parameter or
the address, save and **reload the POS** **[C]**.

**R5 — Firefox/Safari on an `https://` Odoo.** Trust `epos-proxy-ca.crt` in
Firefox's own NSS store (see [§8](#8-firefox-and-the-local-ca)). Safari uses the
Keychain. Verify by opening the HTTPS **base** address (no `/p/<id>`): it must
return `status: ok` without a warning. A local `curl`/`openssl` check does not
prove a remote browser profile trusts it **[C]**.

**R6 — Printing mode.**

- **USB printer → legacy.** GS ( L and the CUPS route are LAN-only **[C]**.
- **LAN printer → legacy by default.** GS ( L or CUPS only after the conditions
  in [§7](#7-mode-selection) and a physical validation; an unknown model stays
  on legacy **[C]**.

**R7 — macOS 15+.** Allow **Local Network** for ePOS Proxy (its outgoing
connections to the printer) and for the browser when it targets a LAN address.
This is separate from incoming access, which the Application Firewall controls
**[C]**.

### 6.2 Diagram

```mermaid
flowchart TD
    A[Start: Odoo 19 cannot print] --> B{Proxy running?}
    B -- No --> B1[Start app; App > Quit is the only stop]
    B -- Yes --> C{Browser on same Mac as proxy?}
    C -- Yes --> D[Use 127.0.0.1 address]
    C -- No --> E[Use LAN address + enable Network printing + firewall + macOS 15 Local Network]
    D --> F{Browser family?}
    E --> F
    F -- Chromium --> G{LNA permission granted?}
    G -- Yes --> G1[HTTP address; mixed content exempt]
    G -- No --> G2[HTTP URL persists if enabled at load; LAN may block]
    F -- Firefox/Safari --> H{use_lna at POS load?}
    H -- true --> H1[HTTP URL persists: HTTPS+LAN blocked; loopback allowed]
    H -- false --> I{Odoo scheme}
    I -- http:// --> J[HTTP address]
    I -- https:// --> K[HTTPS address + trust CA in Firefox/Safari]
    G1 --> L{Printer connection}
    G2 --> L
    H1 --> L
    J --> L
    K --> L
    L -- USB --> M[legacy GS v 0]
    L -- LAN --> N{Validated mode?}
    N -- No --> M
    N -- GS L validated --> O[Opt-in GS L per printer IP]
    N -- CUPS validated --> P[Opt-in CUPS with matching local queue]
```

## 7. Mode selection

| Mode | Applies to | Build | Requirements | Risks | Evidence |
|---|---|---|---|---|---|
| **Legacy `GS v 0`** | USB and LAN receipt printers | `main` | none | 2048-row slices and 64 KiB USB chunks unbenchmarked; some firmware may prefer 255 rows | **[C]** default, **[F]** performance |
| **Epson `GS ( L`** | LAN receipt printers only, selected experimentally per printer IP | candidate | Experimental opt-in in the app; a physical test is required on that exact unit and generation. Naming a model (IV, V, VI or VII) is **not** a sufficient condition: no generation has been validated, and there is no optimisation promise | `GS ( L` fn 112/50 not verified on any generation; TCP acceptance ≠ printing; never auto-fall-back (duplicate risk) | **[C]** code/CI, **[F]** physical |
| **macOS CUPS** | macOS + LAN + local queue | candidate | Existing Epson queue whose device URI is `socket://<printer-ip>:9100`; queue named in the app. The queue must itself advertise `180x180dpi`, `DocFeedCut`/`DocFeedNoCut` and 80 mm media; only advertised options are applied and no model profile is assumed | Unknown/incompatible queue is refused without sending; custom media > 297 mm must be advertised or the ticket is refused; `lp` accepted ≠ paper; no fallback or automatic retry | **[C]** code/CI, **[F]** physical |

Rules that apply to both experimental modes:

- They are **opt-in per printer IP**. The safe default remains **Compatible
  (GS v 0)**; unidentified models do not change behaviour **[C]**.
- A successful TCP write or `lp` exit code proves acceptance by the transport or
  CUPS, **not** that the printer understood the data or produced a cut receipt
  **[C]**.
- Do not configure automatic fallback from one mode to another: a second copy of
  the same ticket is worse than a failed one.

## 8. Firefox and the local CA

Firefox keeps its own certificate authorities. Sample properties verified in
DS-4: `security.enterprise_roots.enabled` is `false` in 115 ESR and `true` in
120, 128 ESR, 140, 145 and 146 **[C]**. Versions not sampled are **[S]**.

**Order of operations**

1. Get the **real URL** Odoo calls from the browser **Network** tab. Do not
   reconstruct it from `window.odoo.use_lna`; the URL was built at POS load and
   may not reflect the current value **[C]**.
2. Only if that URL is `https://`, trust the CA. Path:
   `~/Library/Application Support/EposProxy/certs/epos-proxy-ca.crt`.
3. Firefox 120+ with enterprise roots on: mark **ePOS Proxy Local CA** *Always
   Trust* in Keychain Access, quit Firefox completely and reopen **[C]**.
4. 115 ESR, enterprise roots disabled, or CA not *Always Trust*: **Settings →
   Privacy & Security → Certificates → View Certificates… → Authorities →
   Import…**, choose the `.crt`, tick **Trust this CA to identify websites**,
   accept, restart Firefox **[C]**.
5. Verify with the HTTPS base address, typed explicitly with `https://`
   (`https://127.0.0.1:4645/` when the browser and proxy share the Mac, or
   `https://<proxy-host>:4645/` otherwise). It must return JSON with
   `"status":"ok"` and no warning. Check that the SAN covers the host you used.
6. Safari uses the Keychain; the Firefox import is not needed there **[M]**.

**Error → cause** (do not treat any message as a single-cause diagnosis):

| Message | Most likely cause |
|---|---|
| `SEC_ERROR_UNKNOWN_ISSUER` | HTTPS URL, CA not trusted in this Firefox profile |
| `SSL_ERROR_RX_RECORD_TOO_LONG` | TLS sent to the HTTP port; address scheme ≠ Odoo scheme |
| Blocked mixed active content | `https://` Odoo + `http://` LAN address + no effective LNA |
| Connection reset / HTTP 400 on an `http://` page | Plain HTTP sent to the TLS port |
| "printer not reachable" after ~15 s | Odoo's `AbortSignal.timeout(15000)`; groups latency, network, CORS and mixed content |

The local CA is never installed silently; every trust change is a visible
action in Keychain or in Firefox **[C]**.

## 9. CUPS requirements and activation

The app **does not install drivers or create queues**. A CUPS run exists only on
a Mac where the queue already exists and its owner authorised its use.

**Requirements**

- macOS, LAN printer only (the CUPS route does not cover USB, IPP/AirPrint or a
  remote print server) **[C]**.
- A queue whose device URI is `socket://<printer-ip>:9100`, matching the
  configured printer IP. The app lists only queues whose socket IP matches
  **[C]**.
- Record before testing: `lpstat -v`, `lpoptions -p <queue> -l`, queue name,
  driver/PPD and package version, printer model and firmware **[C]** commands,
  **[F]** values.
- The raster profile is **validated per queue**, not assumed from a model. The
  queue must advertise `Resolution=180x180dpi`, `TmtPaperSource` with
  `DocFeedCut`/`DocFeedNoCut`, and `RP80x297` or an 80 mm `Custom.80x…`/
  `Custom.WIDTHx…` media **[C]**. `TmtSpeed`, `TmtPaperReduction` and drawer
  options are applied only when the queue exposes them. A queue without these
  capabilities is refused with an explanatory error, without sending the job
  and without falling back **[C]**. This proves the configured driver options,
  not physical paper, cut or drawer behaviour on IV–VII **[F]**.

**Activation (only after physical validation)**

1. Enable network printing for the LAN printer and confirm the direct path
   works first.
2. Select **Epson system driver (macOS CUPS) — experimental** for that printer IP and pick the
   matching queue.
3. Print the same Odoo ticket on legacy and on CUPS: compare response, first
   paper and cut; verify QR/barcode 10/10, cut and drawer 20/20, and a > 297 mm
   ticket end-to-end.
4. On any CUPS error, confirm no automatic resend and no duplicate, and return
   to Compatible.

## 10. Rollback

There are three **separate** rollbacks: the application binary, the printing
configuration and — optionally — certificate trust. Do not merge them: undoing
one must not force the others.

### 10.1 Before installing the candidate

1. Quit the running app (**App → Quit**, not just closing the window).
2. Keep the previous `.app` identified and reachable: record its version and
   move it to a safe local folder (for example `~/Applications/EposProxy-previous.app`)
   instead of overwriting it in place.
3. Make a local copy of the configuration directory
   `~/Library/Application Support/EposProxy/` (config and `certs/`). Keep it out
   of tickets and reports; it may contain local paths and certificate material.
4. Record the candidate SHA and its SHA-256 (§12).

### 10.2 Returning to the previous application

1. Quit the candidate.
2. Restore the identified previous `.app` to its original location.
3. Launch it and verify the version and the effective mode (`Compatible / legacy`
   unless the previous configuration said otherwise).
4. Keep the data directory, `certs/`, the CA and every system queue. **Do not**
   delete the directory to "start clean". If a migration requires restoring the
   configuration, do it with the app stopped and from the copy of step 10.1.3;
   never wipe the whole directory.

| Change | Rollback | Verify |
|---|---|---|
| `GS ( L` or CUPS per printer | Select **Compatible / legacy (GS v 0)** for that printer IP; alternatively stop the app, remove the IP from `lan_image_modes`/`lan_cups_queues` in `~/Library/Application Support/EposProxy/config.json`, and start it | A test ticket prints; `lpstat -o <queue>` shows no job submitted by this ticket |
| **CUPS queue** | **Keep it by default.** Disabling the opt-in and returning to legacy does **not** require deleting any queue. Never remove a pre-existing, shared or other-application queue. Only a temporary queue created specifically for this test, identified as such and authorised by its owner, may be removed with `lpadmin -x <queue>` | `lpstat -v` still lists every pre-existing queue; no print jobs submitted by this ticket |
| Address/scheme | Copy the other address (HTTP ↔ HTTPS, loopback ↔ LAN) and **reload the POS** | Network tab shows the expected URL; base address returns `status: ok` |
| Remote access | Disable **Network printing** | Non-loopback clients receive 403 again |

### 10.3 Certificate trust is a separate, optional step

Reinstalling the previous app must **not** remove CA trust or delete its keys
automatically. Removing trust is an independent, explicit action: it can break
HTTPS for the previous app version and for other browser profiles that rely on
the same CA. If it is really needed, delete or edit trust in Firefox
(Settings → Certificates → Authorities) or in the Keychain, then restart the
browser; document that HTTPS will warn again afterwards.

Do **not** delete `certs/` to "reset" HTTPS: the CA is not rotated
automatically and a new one would have to be trusted everywhere again.

Rollback of the *default* needs no action: legacy is already the default.

## 11. Known limitations

- **No physical baseline.** No response/paper/cut measurement, duplicate count
  or browser run exists for any mode. Every performance statement is pending
  **[F]**.
- **CI has no hardware.** CI proves compilation, byte-level ESC/POS tests and the
  Odoo contract model only. macOS runners are 15; Catalina and macOS 26 are not
  executed **[C]/[F]**.
- **Long sheets.** > 297 mm receipts are unvalidated on all three modes. CUPS
  custom media may be rejected or rescaled by the driver **[C]/[F]**.
- **Models.** No TM-T88 IV, V, VI or VII unit has been physically validated;
  `GS ( L` fn 112/50 support is documented for IV in PR #3's README and must be
  confirmed per generation **[C]/[S]**.
- **Firefox is not universally supported.** Only the combinations in §6/§8 have
  a modelled outcome; versions between the sampled ones are **[S]**. An
  `https://` Odoo with a LAN `http://` address is blocked without LNA, and a
  scheme mismatch fails before the certificate.
- **Browser historical limits are incomplete.** The Firefox pages consulted
  (115, 116, 128, 148, 149, 150) only declare a macOS minimum; the last
  compatible version, intermediate versions and effective behaviour on macOS 26
  are **[S]**. For Chrome only the current requirement (macOS 13+) was
  verifiable; the last Chrome for 10.15/11/12 is **[S]** and Edge has no
  consulted version. These browser declarations prove neither proxy execution
  nor Epson driver availability **[S]**.
- **Firewall and Local Network privacy.** macOS 15+ can block outgoing LAN
  connections and incoming connections to the proxy; ad-hoc signing weakens
  identity tracking across builds **[C]**.
- **Duplicates are still possible.** Odoo 19.0 returns `canRetry: true` on every
  failure and has no idempotency key; a manual **Retry** after a timeout is a new
  POST and may print twice. The proxy no longer prints a queued job after the
  client abandoned it and never resends on its own, but it cannot prevent the
  operator's retry **[C]**. `body_sha256` correlates attempts, it does not
  deduplicate.
- **Deployed Odoo version unknown.** The retry analysis covers 19.0 at
  `a157617`; a different version or fork must be re-checked **[C]**.
- **2048-row slices / 64 KiB USB chunks** were changed without a benchmark;
  they may be a regression on some firmware **[M]/[F]**.

## 12. Verification checklist (when evidence exists)

1. Record the environment block of `docs/printing-benchmark.md` §3.1 (printer,
   firmware, transport, Mac, macOS, Odoo version and scheme, `use_lna` at load,
   browser, proxy commit, address, paper).
2. Fill the four scenarios of §4 with 20 repetitions each and attach raw logs.
3. Fill the results table in §5; keep "HTTP response", "first paper" and "cut"
   separate.
4. Validate Firefox combinations 20/20 only for the modelled-supported cases;
   record the blocked combinations as 0/3, never as failures to hide.
5. Run a smoke test on Chrome, Edge and Safari, 5 tickets each.
6. Update §5 and the limitation list with what was actually observed, and only
   then recommend a default.

## 13. Evidence index

| Claim area | Source |
|---|---|
| Default legacy raster, slicing, USB chunking | `internal/escpos/commands.go`, `internal/printer/printer.go`; `12eed02`, `5d571a0` |
| Ports, addresses, certificate layout | `internal/config/config.go`, `internal/certs/certs.go`, `docs/macos11-odoo-https.md` |
| Remote access policy | `internal/server/server.go` (`AccessPolicy`) |
| Build minimums and CI runners | `.github/workflows/build-macos.yml` in `d739255` |
| Benchmark protocol and trace fields | `docs/printing-benchmark.md`, PR #5 `5137bc5` |
| Odoo 19 retry semantics, duplicate limits | `docs/odoo-print-retry.md`, PR #6 `d739255` (Odoo `a157617`) |
| Firefox URL rules, CA steps, error mapping | `internal/compat/`, PR #7 `d450643`; DS-4 diagnosis |
| `GS ( L` selection, padded dot width and legacy default | PR #3 `8edd998`, `internal/escpos/commands.go` in the candidate |
| CUPS per-queue profile validation, queue matching, no auto retry | PR #4 `02be391`, `internal/cups/cups.go` in the candidate |
| Accepted browser/CA guide (absorbed, not superseded until adoption) | DS-2 comment `01a1df9a-bd4a-724f-ac8b-9cdaf4eec329` |

### 13.1 Official sources consulted (2026-11-16)

| URL | What it proves | Limit / gap |
|---|---|---|
| `mozilla.org/en-US/firefox/115.0/system-requirements/` | Declared macOS 10.12+; Intel/Apple Silicon | Version specific; not a print or Catalina test |
| `mozilla.org/en-US/firefox/116.0/system-requirements/` | Declared macOS 10.15+ | Version specific |
| `mozilla.org/en-US/firefox/128.0/system-requirements/` (also 148.0, 149.0, 150.0) | Each declares macOS 10.15+ | Does not establish the last compatible version or macOS 26 behaviour |
| `support.google.com/chrome/a/answer/7100626` | Current Mac requirement: macOS 13+ | No macOS timeline; last Chrome for 10.15/11/12 remains **[S]** |
| `chromeenterprise.google/release-notes/` | Attempt to retrieve a versioned retirement note | **404**; no versioned macOS statement obtained |
| `support.apple.com/en-us/122727` (published 2026-09-14) | macOS includes Safari; installer eligibility depends on the Mac model | Does not identify the Safari version on a given machine |
| `support.apple.com/en-us/122867` (published 2026-09-14) | macOS Tahoe 26 supported Mac models (Intel and Apple Silicon listed) | Architecture alone is not enough |
| `download.epson-biz.com/modules/pos/index.php?page=soft&scat=32` | Old POS portal closed on 2024-06-16; redirects to the new portal | No package/OS/model matrix |
| `support.epson.net/setupnavi/?PINF=category&OSC=WS&GROUP=pos_product` | — | **403**; no Epson matrix (package, PPD, OS, architecture) |

Use these declarations only for what the row states: none of them certifies
printing, the proxy artefact or the Epson filter. The benchmark table stays
empty until measurements exist.

**Rollout order.** Merge and validate in this order: #5 (instrumentation) → #6
(retry hardening) → #7 (Firefox/CA), keeping Vite 6.4.6. PR #3 and #4 stay
opt-in drafts until their physical criteria are met; no default changes on the
strength of this document.

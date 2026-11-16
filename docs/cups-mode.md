# macOS Epson CUPS mode — operations and validation

Status: **prepared, physical validation pending**. This document describes what
the opt-in macOS CUPS mode requires, what it verifies automatically, how to
roll it back, and the exact physical test still needed before promoting PR #4.
It does not claim any speed improvement or model support without hardware
evidence.

Scope: macOS only, local LAN queue with `socket://<printer-ip>:9100`. USB,
IPP/AirPrint and shared/remote print servers are out of scope.

## Requirements

1. An Epson TM-T88 receipt queue already installed on the Mac, created by the
   user with the official Epson driver. This app never installs a driver, a
   PPD or a queue, and never changes system trust settings.
2. The queue's device URI must be `socket://<printer-ip>:9100`, the same IP as
   the LAN printer configured in the app.
3. The queue must expose, in its real PPD:
   - a `Resolution` choice `180x180dpi`;
   - `TmtPaperSource` with `DocFeedCut` and `DocFeedNoCut`;
   - `RP80x297` (or a `Custom.*` 80 mm media) so long receipts can request a
     custom page length.
4. Odoo must be reachable and configured for the same printer IP; the Odoo URL
   does not change when CUPS mode is enabled.

The 180 dpi / `Tmt*` / `RP80x297` profile was verified on **one TM-T88IV
queue**. It is not a universal Epson driver profile. A queue with other
choices is refused with a clear error instead of being printed with these
values.

## What the app verifies automatically

- Queue IP: only queues whose `lpstat -v` URI is `socket://<ip>` (port 9100 or
  omitted) can be selected for that printer; a queue for another store is
  rejected.
- Queue profile: before saving the mode, and again before each submission
  (cached for 60 s), the driver options are read with
  `lpoptions -p <queue> -l`. Missing 180 dpi, missing cut control or missing
  80 mm media is a hard error.
- Long receipts: above 287 mm the job requests `media=Custom.80x<length>mm`.
  A queue without custom media support refuses the ticket instead of cropping
  it.
- Options not present in the PPD are never forced. `-o raw` is never used.
- Cancellation: a request cancelled **before** submission never invokes `lp`;
  once a job is handed to `lp` it finishes even if Odoo aborts the HTTP request.
- Failures are never re-sent through ESC/POS and never trigger a second `lp`
  attempt.

## Environment data to capture (Mac with the queue installed)

```sh
sw_vers
uname -m
system_profiler SPPrintersDataType
lpstat -v
lpstat -p <queue>
lpoptions -p <queue> -l
pkgutil --pkgs | grep -i -E 'epson|seiko'
# version of the installed Epson package, when available:
pkgutil --pkg-info <epson-package-id>
```

Record the printer model and firmware (self-test page), the cable/network
topology (LAN switch, VLAN, firewall), Odoo version and the browser used.

## Physical test protocol (not yet executed)

Use the benchmark defined by DS-3: three tickets (short, medium with
QR/barcode, long >297 mm), 20 repetitions per scenario, cold and warm start,
legacy (`GS v 0`) versus CUPS with the **same** ticket. Measure separately:

- time to HTTP response (from `Print timing ... cupsSubmit=` / `queueAndSend=`
  logs; it is software time, not paper time);
- time to first paper (video at 240 fps or stopwatch);
- time to cut;
- errors, retries and duplicate receipts;
- `xmlBytes`, PNG dimensions and printed length in mm.

Per scenario check: complete ticket without cropping, legible QR/barcode
10/10, cut in the right place, drawer opens once (drawer remains a direct
ESC/POS `<pulse/>`), and no duplicate on CUPS failure or ambiguity. Report
p50/p95/min/max and attach the raw logs.

## Rollback

1. In the app, select **Compatible / legacy (GS v 0)** or **GS ( L — experimental**
   for that printer. This clears the stored CUPS queue and returns to direct
   ESC/POS on the next ticket (no restart needed).
2. Confirm the next ticket logs `imageMode=legacy` (or `epson_graphics`) and
   not `imageMode=cups_driver`.
3. If the CUPS mode misbehaves, keep it disabled for that printer and leave
   the installed queue untouched; the queue can be removed later by the user
   with the macOS printer settings. Removing the queue does not affect the
   direct ESC/POS path.
4. To remove all traces from the config, delete the printer from the app or
   clear `lan_image_modes` / `lan_cups_queues` for that IP in the
   configuration file while the app is closed.

## Known limitations

- `lp` accepting a job means CUPS accepted it, not that paper was printed or
  cut. There is no end-to-end job completion confirmation.
- The driver may ignore or rescale custom media; only a physical long receipt
  proves it. This is a primary reason the mode stays opt-in.
- The profile is validated only against the PPD choices; it does not prove the
  driver's internal rasterization matches the ESC/POS output pixel by pixel.
- Unknown models, other firmware versions and USB queues are unsupported.
- Odoo 19 offers a manual Retry after any failure; the proxy cannot prevent a
  duplicate caused by an operator retry, only its own automatic resends
  (there are none).

## Pending evidence

| Criterion | Status |
|---|---|
| CUPS vs legacy on the same ticket, p50/p95, errors, duplicates | pending physical test |
| Long >297 mm ticket complete, cut and drawer correct | pending physical test |
| QR/barcode legible 10/10 | pending physical test |
| No duplicate on CUPS failure/ambiguity, no automatic resend | code-level covered by tests; physical confirmation pending |
| Opt-in per IP, validated queue, documented and tested rollback | implemented and unit-tested |
| PR description updated with evidence and limits | static evidence only; physical pending |

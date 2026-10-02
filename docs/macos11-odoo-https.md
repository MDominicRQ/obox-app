# macOS + Odoo 19 local printing

ePOS Proxy exposes both HTTP and HTTPS while keeping the actual USB/LAN printer path unchanged.

- **HTTP / LNA** is the recommended path for Odoo 19 on current Chromium browsers.
- **HTTPS / Legacy** remains available for older macOS/browser combinations.
- Closing the application window does **not** stop the proxy. The window is hidden and the HTTP/HTTPS listeners continue running in the background.
- Use **App → Quit** to actually stop ePOS Proxy.

## Addresses shown by the app

Each printer can expose up to four protocol-free Odoo addresses:

- **Local HTTP / LNA**: `127.0.0.1:4545-4555/p/<printer-id>`
- **Local HTTPS / Legacy**: `127.0.0.1:4645-4655/p/<printer-id>`
- **LAN HTTP / LNA**: `<lan-ip>:4545-4555/p/<printer-id>`
- **LAN HTTPS / Legacy**: `<lan-ip>:4645-4655/p/<printer-id>`

Copy the address exactly as shown. Do **not** prefix it with `http://` or `https://` in the Odoo printer field; Odoo selects the protocol itself.

If Odoo is open in a browser on the same Mac as ePOS Proxy, prefer **Local HTTP / LNA**. Loopback avoids LAN routing and macOS firewall/interface-selection issues. Use a LAN address only when the POS browser is on another device.

## Odoo 19

Modern Chromium with Local Network Access:

```text
point_of_sale.use_lna = True
Epson Printer IP Address = <Local HTTP / LNA address>
```

On Odoo 19.1+ use the printer's **Use Local Network Access** option when available.

Current Chrome/Chromium requires permission for the Odoo site to access local/loopback network targets. If the browser reports that Local Network Access is denied, check:

```text
Settings → Privacy and security → Site settings
→ Additional permissions → Local network
```

Allow the Odoo database origin and reload the POS.

Legacy browser/macOS 11 path:

```text
point_of_sale.use_lna = False
Epson Printer IP Address = <HTTPS / Legacy address>
```

Odoo then calls a URL similar to:

```text
https://192.168.1.77:4645/p/<printer-id>/cgi-bin/epos/service.cgi?devid=local_printer
```

## Browser connectivity check

ePOS Proxy supports the same browser check recommended for Epson/Odoo devices.

The app now has **Open HTTP** and **Open HTTPS** buttons. They open the exact configured base address in the system browser.

A working base address returns JSON containing `"status":"ok"` and the printer ID. This is also the URL Odoo tells users to open when manually accepting an HTTPS certificate, so it no longer returns 404.

You can still test the Epson-compatible service route directly:

```text
http://<host>:<http-port>/p/<printer-id>/cgi-bin/epos/service.cgi?devid=local_printer
```

That GET request returns HTTP 200 without printing; actual print jobs use POST.

The route without `/p/<printer-id>` is also available for connectivity checks and auto-printer mode:

```text
http://<host>:<http-port>/cgi-bin/epos/service.cgi?devid=local_printer
```

## LAN address selection

The app no longer assumes that the default route to the internet is the correct LAN interface. It enumerates local IPv4 interfaces and prefers physical/private interfaces such as Wi-Fi or Ethernet over VPN/virtual interfaces such as `utun`, Tailscale, WireGuard, bridges, Docker, and similar adapters.

All useful local IPv4 addresses are added to the HTTPS server certificate SANs. This avoids a VPN or secondary interface causing the certificate to be valid for the wrong address on newer Macs.

## Local CA and certificates

Certificates are stored under the application's existing configuration directory in `certs/`.

The local CA is generated once and is not rotated when the Mac's LAN IP changes. The server certificate is regenerated when needed and contains:

- `localhost`
- `127.0.0.1`
- all currently detected useful local IPv4 addresses

Private keys use mode `0600`; public certificates use `0644`.

If only one file of the CA pair is present or the CA/key pair is invalid, the app does not silently create a new CA. HTTPS is disabled and HTTP continues running so an already-trusted CA is not unexpectedly replaced.

## Trusting the CA on macOS

Use **App → Install HTTPS Certificate**.

The app opens the public CA certificate and shows explicit trust instructions. It never runs a privileged trust command silently.

In Keychain Access:

1. Import/open **ePOS Proxy Local CA**.
2. Open the certificate and expand **Trust**.
3. Set **When using this certificate** to **Always Trust**.
4. Authenticate when macOS asks.

After changing trust, restart the browser if it had already cached the certificate error.

## Background mode

Closing the red window button hides the window instead of quitting. The proxy continues serving print jobs.

- **App → Show Window** restores the UI.
- Launching ePOS Proxy a second time also brings the existing window back because the application uses a single-instance lock.
- **Auto Start** launches the application with `--background`, so the proxy can start without opening the main window.
- **App → Quit** is the explicit action that stops the process and both listeners.

## Modern macOS LAN privacy and firewall

On **macOS 15 and later**, Local Network privacy applies to outgoing connections made by ePOS Proxy to LAN printers. A LAN printer such as `192.168.1.33` therefore requires **ePOS Proxy** to be allowed under:

```text
System Settings → Privacy & Security → Local Network
```

The application proactively probes configured LAN printers on startup so macOS can present the permission prompt while the UI is running.

This permission is separate from incoming access to the proxy. A remote Odoo workstation reaches ePOS Proxy through an incoming TCP connection. If the LAN URL works locally on the Mac but cannot be opened from the Odoo workstation, check:

```text
System Settings → Network → Firewall → Options
```

and allow incoming connections for ePOS Proxy. macOS can deny incoming connections until the firewall prompt is accepted.

Use **App → Test Proxy Connections** to check:

- local HTTP,
- local HTTPS,
- HTTP through the Mac's selected LAN address,
- every configured LAN printer on TCP port 9100.

The LAN printer result is especially important on macOS 15+: it verifies the path `ePOS Proxy → printer`, not just `browser → ePOS Proxy`.

For CI/testing, the generated app is currently ad-hoc signed. Apple recommends an Apple-issued signing identity for reliable Local Network privacy identity tracking across builds. A production distribution should therefore use Developer ID signing/notarization rather than relying permanently on ad-hoc signing.

## Troubleshooting

1. Keep ePOS Proxy running; closing the window is safe, but **App → Quit** stops printing.
2. Confirm the in-app **Test** button prints. It intentionally uses the local HTTP proxy and validates the proxy-to-printer path.
3. For current Chromium/Odoo 19 on the same Mac, enable LNA and use **Local HTTP / LNA** (`127.0.0.1`).
4. Use **Open HTTP**. The browser must show the ePOS Proxy `status: ok` diagnostic page.
5. Check the browser's **Local network** permission for the exact Odoo database origin if Odoo still reports the printer unreachable.
6. Use LAN addresses only when the POS browser is running on another device; on modern macOS, allow Local Network access for the browser/app and check the macOS Application Firewall.
7. If HTTPS is required, disable LNA in Odoo, trust the local CA, and use **Open HTTPS**. The browser must show the same `status: ok` diagnostic page without a certificate warning.
8. If the Mac LAN IP changes, restart ePOS Proxy so the server certificate can be reissued with current SANs. The CA remains unchanged.
9. Check application logs for `EPOS HTTP Server Error`, `EPOS HTTPS Server Error`, or certificate preparation errors.

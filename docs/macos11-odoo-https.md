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

For current Chrome/Chromium, prefer the **LAN HTTP / LNA** address when available. Chrome 145+ distinguishes LAN and loopback permissions, so `127.0.0.1` may require a separate loopback permission.

## Odoo 19

Modern Chromium with Local Network Access:

```text
point_of_sale.use_lna = True
Epson Printer IP Address = <LAN HTTP / LNA address>
```

On Odoo 19.1+ use the printer's **Use Local Network Access** option when available.

Chrome/Chromium 142+ requires permission for the Odoo site to access the local network. In Chrome, check:

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

Open:

```text
http://<host>:<http-port>/p/<printer-id>/cgi-bin/epos/service.cgi?devid=local_printer
```

A blank page with HTTP 200 means the proxy endpoint is reachable. This GET check does not print anything; printing continues to use POST.

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

## Troubleshooting

1. Keep ePOS Proxy running; closing the window is safe, but **App → Quit** stops printing.
2. Confirm the in-app **Test** button prints. It intentionally uses the local HTTP proxy and validates the proxy-to-printer path.
3. For current Chromium/Odoo 19, enable LNA and use **LAN HTTP / LNA**.
4. Check Chrome's **Local network** permission for the exact Odoo database origin.
5. Open the browser connectivity-check URL above. It must return a blank page rather than 404.
6. If LAN access fails but `127.0.0.1` works, check the macOS Application Firewall and the selected LAN address.
7. If HTTPS is required, trust the local CA and then test the HTTPS address.
8. If the Mac LAN IP changes, restart ePOS Proxy so the server certificate can be reissued with current SANs. The CA remains unchanged.
9. Check application logs for `EPOS HTTP Server Error`, `EPOS HTTPS Server Error`, or certificate preparation errors.

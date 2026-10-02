# macOS 11 + Odoo 19 HTTPS mode

The proxy keeps the existing HTTP listener for modern browsers using Odoo Local Network Access and also starts a second HTTPS listener for older macOS/browser combinations.

## Addresses shown by the app

- **HTTP / LNA**: `<host>:4545-4555/p/<printer-id>`
- **HTTPS / Legacy macOS**: `<host>:4645-4655/p/<printer-id>`

Copy the address exactly as shown. Do **not** prefix it with `http://` or `https://` in Odoo; Odoo selects the protocol itself.

## Odoo 19

Modern browser with Local Network Access:

```text
point_of_sale.use_lna = True
Epson Printer IP Address = <HTTP / LNA address>
```

Legacy browser/macOS 11 path:

```text
point_of_sale.use_lna = False
Epson Printer IP Address = <HTTPS / Legacy macOS address>
```

Odoo will then call a URL similar to:

```text
https://192.168.1.77:4645/p/<printer-id>/cgi-bin/epos/service.cgi?devid=local_printer
```

## Local CA and certificates

Certificates are stored under the application's existing configuration directory in `certs/`.

The local CA is generated once and is not rotated when the Mac's LAN IP changes. The server certificate is regenerated when needed so its SAN contains:

- `localhost`
- `127.0.0.1`
- the current LAN IP

Private keys use mode `0600`; public certificates use `0644`.

If only one file of the CA pair is present or the CA/key pair is invalid, the app does not silently create a new CA. HTTPS is disabled and HTTP continues running so an already-trusted CA is not unexpectedly replaced.

## Trusting the CA on macOS

Use **App → Install HTTPS Certificate**.

The app opens the public CA certificate in Keychain Access and then shows the trust instructions. It never runs a privileged trust command silently.

In Keychain Access:

1. Import/open **ePOS Proxy Local CA**.
2. Open the certificate and expand **Trust**.
3. Set **When using this certificate** to **Always Trust**.
4. Authenticate when macOS asks.

After changing trust, restart the browser if it had already cached the certificate error.

## Troubleshooting

1. Confirm the normal in-app **Test** button still prints. It intentionally uses HTTP and validates the existing proxy-to-printer path.
2. Confirm the HTTPS address shown by the app uses the expected Mac IP and a port in `4645-4655`.
3. Check that the CA is trusted in Keychain Access.
4. If the Mac LAN IP changed, restart ePOS Proxy so the server certificate can be reissued with the new SAN. The CA remains the same.
5. Check application logs for `EPOS HTTPS Server Error` or certificate preparation errors.
6. If HTTPS cannot initialize, HTTP remains available; this isolates TLS problems from ESC/POS/printer connectivity.

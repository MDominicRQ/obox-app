# ePOS Proxy

A lightweight desktop app that exposes your USB Epson printer as a local HTTP endpoint, making it compatible with Odoo Point of Sale.

## Installation

### Download

Download the latest release for your platform from the [Releases](../../releases) page.

## Usage

- Connect your printer via USB to your computer
- Launch ePOS Proxy - the application will detect your printer automatically

![screenshot](readme/printer.png)

- Copy the printer address shown in the UI
- Configure Odoo Point of Sale to use this address 

![screenshot](readme/config.png)

-  Paste the printer IP in the "Epson Printer IP Address" field.
-  Check Use Local Network access option
-  Save and open your POS session


### Linux

Before running the application, you may need to make it executable and install a required dependency.
```bash
sudo apt update
sudo apt install libwebkit2gtk-4.1-0
```

## macOS: start automatically at login

Install the extracted `ePOS Proxy.app` in `/Applications` before enabling
**App → Auto Start**. Do not register a copy launched directly from a mounted
DMG or a temporary Gatekeeper App Translocation path.

Auto Start runs **when you log in**, not before the macOS login screen. It uses
`--background`, so no window is displayed. After logging in, verify the proxy
rather than looking for its window:

```sh
pgrep -fl 'ePOS Proxy'
curl -fsS http://127.0.0.1:4545/healthz
plutil -p "$HOME/Library/LaunchAgents/epos-proxy.plist"
launchctl print "gui/$(id -u)/epos-proxy"
```

The HTTP port may differ if configured; use the port shown by the app.
`launchctl print` may fail before the next login if the agent has only been
written to disk. If macOS blocks background items, review **System Settings →
General → Login Items & Extensions** (the name varies by macOS release).
A checkbox in the app verifies the agent file and its executable path, but
cannot override a system-level block.

If you move or replace the `.app`, disable and enable Auto Start again to
update its executable path.

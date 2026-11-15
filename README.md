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

Local Network Access only exists in Chromium. In Firefox or Safari Odoo uses the scheme of the Odoo page, so copy the HTTP address for an `http://` Odoo and the HTTPS address for an `https://` Odoo. See [macOS + Odoo 19 local printing](docs/macos11-odoo-https.md) for the full matrix and the Firefox certificate steps.


### Linux

Before running the application, you may need to make it executable and install a required dependency.
```bash
sudo apt update
sudo apt install libwebkit2gtk-4.1-0
```


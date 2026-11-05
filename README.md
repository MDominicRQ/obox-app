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



## Epson TM-T88IV / V / VI / VII — LAN receipt graphics

Odoo 19's Epson printer integration rasterizes a **complete HTML receipt** into
one monochrome `<image>` command. This proxy can transmit those image pixels using
Epson buffered graphics (**GS ( L**, function 112 to store, function 50 to print),
instead of the older **GS v 0** raster command.

- **LAN receipt printers:** Epson buffered graphics is selected by default.
  The app lists a **LAN receipt image mode** dropdown for each printer. Select
  **Compatible (GS v 0)** when using another brand or firmware which does not
  support Epson's GS ( L graphics commands. Mode changes are saved per IP and
  apply to the next print job without changing the Odoo URL.
- **USB printers and raw label printing:** unchanged.
- The optimized mode divides the image into 192-row graphics chunks (or fewer
  rows when necessary for the 16-bit command length). It does not drop image
  rows, modify pixels, disable cutting, or change paper density.
- The application logs `Print timing ... imageMode=... xmlBytes=...
  escposBytes=... parse=... queueAndSend=...` for each Odoo ticket. These are
  **software processing and TCP acceptance times**, *not* measured paper
  output times. If the network send is fast but paper movement remains slow,
  compare the same full receipt on GS ( L and the legacy mode; the printer's
  firmware, thermal head power, DIP settings, print density, and image content
  can still determine physical speed.
- Epson TM-T88IV/M129H is documented as supporting GS ( L function 112 and
  the paired function 50. Other Epson TM-T88 models support this command
  family, but exact performance must be confirmed on each hardware generation.

Epson command reference:
[GS ( L function 112](https://download4.epson.biz/sec_pubs/pos/reference_en/escpos/gs_lparen_cl_fn112.html),
[GS ( L function 50](https://download4.epson.biz/sec_pubs/pos/reference_en/escpos/gs_lparen_cl_fn50.html).

**Compatibility note:** The proxy cannot conclusively detect unsupported
graphics commands over write-only TCP/9100. A successful TCP write confirms
network acceptance, not that the printer understood or printed the graphics.
For an unknown model use Compatible mode until GS ( L has been tested. Do not
send an automatic duplicate raster fallback after a successful TCP write:
it may print the same ticket twice.

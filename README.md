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

- **LAN receipt printers:** **Compatible (GS v 0)** remains the default.
  For a verified Epson TM-T88IV/V/VI/VII, select **Epson optimized (GS ( L)**
  in the app's **LAN receipt image mode** dropdown. The option is stored per
  printer IP and takes effect on the next ticket without changing the Odoo URL.
  For unknown models, retain Compatible mode until explicitly validated.
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


### Experimental macOS Epson CUPS mode for slow Odoo receipts

When an Epson TM-T88IV prints the same receipt faster through the installed
macOS driver than via direct ESC/POS TCP/9100, obox-app can now try that
existing driver **without changing the Odoo URL or LAN printer IP**.

1. The Epson CUPS printer queue must already be installed on the Mac and
   configured with a `socket://PRINTER_IP:9100` device URI. Check with
   `lpstat -v` and `lpoptions -p QUEUE -l`. This integration does **not**
   install a driver or create a printer queue.
2. For the configured LAN receipt printer, choose **Epson system driver
   (macOS CUPS)**. The app lists only queues whose socket IP matches that
   printer, preventing accidental routing to another store. The choice is
   stored for this printer IP; the safe default remains **Compatible**.
3. Print a full Odoo test receipt and verify physical speed, correct paper
   length (particularly receipts longer than 297 mm), QR/barcode fidelity,
   cut and cash-drawer behavior. Return to **Compatible** immediately if
   anything differs.

Implementation: For a standard Odoo `<epos-print><image>...<cut/></epos-print>`
job the original 1-bit bitmap is reproduced in a 1-bit PNG. The PNG is
submitted to the selected local Epson queue using `lp`, *without* `-o raw`,
with `TmtSpeed=Auto`, `TmtPaperReduction=Both`, `TmtPaperSource=DocFeedCut`
and disabled driver drawer pulses. Bitmap-only images without cut use
`DocFeedNoCut`. Odoo sends a cash-drawer `<pulse/>` separately; this
remains a direct ESC/POS operation. Other XML jobs that cannot safely be
rendered as one raster receipt stay on the direct ESC/POS path.

For images taller than approximately 287 mm, the integration requests a
custom 80 mm roll-paper length. Epson drivers vary in their supported
custom page sizes; some may reject or rescale such jobs. **Do not roll out
to every store before validating real long receipts.**

`lp` acknowledging a job means **CUPS accepted it**, not that a physical
receipt was completed. A CUPS failure is returned to Odoo without an
automatic ESC/POS retry, to avoid duplicate customer receipts. Temporary
ticket PNGs have OS-private permissions and are deleted after submission.
The system driver mode is macOS-only; other platforms remain unchanged.

Official CUPS media selection:
https://www.cups.org/doc/options.html

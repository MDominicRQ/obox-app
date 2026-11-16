import { main } from "../../wailsjs/go/models";

export async function sendEposPrint(
  printer: main.Printer,
  openCashDrawer = false,
  testGraphics = false,
) {
  // A short 512-dot monochrome receipt image checks whether the selected
  // LAN graphics mode is understood by this printer (it is not a speed test).
  let imageContent = "";
  if (testGraphics && !openCashDrawer && printer.isLAN) {
    const widthBytes = 64;
    const rows = 192;
    const bitmap = new Uint8Array(widthBytes * rows);
    for (let row = 0; row < rows; row++) {
      bitmap.fill(row % 24 < 4 ? 0xff : row % 24 < 8 ? 0x55 : 0x00, row * widthBytes, (row + 1) * widthBytes);
    }
    const encoded = btoa(String.fromCharCode(...bitmap));
    imageContent = `<image align="center" width="512" height="192">${encoded}</image><cut type="feed" />`;
  }

  const content = openCashDrawer
    ? "<pulse />"
    : imageContent ||
      `
        <feed line="1" />
        <text font="font_e" em="true"/>
        <text align="center">This is a test receipt ${printer.name}</text>
        <feed line="10" />
        <cut type="feed" />
      `;

  return await fetch(`http://${printer.ip}/cgi-bin/epos/service.cgi`, {
    method: "POST",
    body: `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
          <s:Body>
            <epos-print xmlns="http://www.epson-pos.com/schemas/2011/03/epos-print">
            ${content}
            </epos-print>
          </s:Body>
        </s:Envelope>`,
  });
}

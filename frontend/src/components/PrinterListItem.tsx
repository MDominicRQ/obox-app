import { main } from "../../wailsjs/go/models";
import { PrinterContext } from "../contexts/PrinterContext";
import { useContext } from "react";
import PrinterActions from "./PrinterActions";
import LibusbFixDialog from "./LibusbFixDialog";
import CloseButton from "./CloseButton";

type PrinterListItemProps =
  | {
      printer: main.Printer;
      isOnline: true;
    }
  | {
      printer: main.UnavailablePrinter;
      isOnline: false;
    };

export default function PrinterListItem({
  printer,
  isOnline,
}: PrinterListItemProps) {
  const printerContext = useContext(PrinterContext);

  const getPrinterStatusClass = (printer: main.Printer) => {
    if (!printer.isLAN) {
      return printer.online ? "bg-success" : "bg-danger";
    }
    const status = printer.lanIp
      ? printerContext.data.lanStatus[printer.lanIp]
      : undefined;

    if (status === "online") {
      return "bg-success";
    }

    if (status === "offline") {
      return "bg-danger";
    }

    return "bg-warning";
  };

  const hasLibUsbErrorFix = (error = "") => {
    return error.toLowerCase().includes("libusb");
  };

  return (
    <>
      {isOnline ? (
        <li
          key={printer.id}
          className="text-left first:pt-0 py-6 last:pb-0 relative"
        >
          <div className="flex items-center justify-between gap-2">
            <span
              className={`w-3 h-3 rounded-full shrink-0 ${getPrinterStatusClass(printer)}`}
            />
            <span className="min-w-0 font-medium text-gray-900 break-all flex-1">
              {printer.name}
            </span>
            {printer.isLAN && (
              <CloseButton
                onClick={() => printerContext.actions.removeLanPrinter(printer)}
              />
            )}
          </div>
          <div className="mt-2 text-sm">
            <div className="text-xs font-medium uppercase tracking-wide text-gray-500">
              Local HTTP / LNA ON
            </div>
            <div className="text-gray-600 break-all">{printer.ip}</div>

            {printer.httpsIp && (
              <>
                <div className="mt-2 text-xs font-medium uppercase tracking-wide text-gray-500">
                  Local HTTPS / LNA OFF
                </div>
                <div className="text-gray-600 break-all">{printer.httpsIp}</div>
              </>
            )}

            {printer.networkIp && (
              <>
                <div className="mt-3 text-xs font-medium uppercase tracking-wide text-gray-500">
                  LAN HTTP / LNA ON
                </div>
                <div className="text-gray-600 break-all">{printer.networkIp}</div>
              </>
            )}

            {printer.networkHttpsIp && (
              <>
                <div className="mt-2 text-xs font-medium uppercase tracking-wide text-gray-500">
                  LAN HTTPS / LNA OFF
                </div>
                <div className="text-gray-600 break-all">
                  {printer.networkHttpsIp}
                </div>
              </>
            )}

            <div className="mt-2 text-xs text-gray-500">
              For Odoo 19 on Chrome 145+, prefer LAN HTTP with LNA enabled.
              Chrome now separates Local Network access from loopback access:
              127.0.0.1 requires the separate “Apps on device” permission.
            </div>

            <div className="mt-1 text-xs text-gray-500">
              Local 127.0.0.1 remains available as a fallback. If you use it in
              Chrome 145+, allow both the Odoo site's Local Network permission
              and its Apps on device / loopback permission.
            </div>

            {!printerContext.data.networkPrintingEnabled && (
              <div className="mt-2 text-xs text-amber-700">
                Enable App → Allow Network Printing to expose the recommended
                LAN HTTP address for current Chromium.
              </div>
            )}
          </div>
          <PrinterActions printer={printer} />
        </li>
      ) : (
        <li
          key={printer.name}
          className="text-left first:pt-0 py-6 last:pb-0 relative"
        >
          <div className="flex items-center gap-2">
            <span className="w-3 h-3 rounded-full shrink-0 bg-danger" />
            <span className="min-w-0 font-medium text-gray-900">
              {printer.name}
            </span>
          </div>
          <div className="text-danger mt-1 text-wrap">
            Unable to communicate with this printer: {printer.errorMsg}
          </div>
          {hasLibUsbErrorFix(printer.errorMsg) && <LibusbFixDialog printerName={printer.name} />}
        </li>
      )}
    </>
  );
}

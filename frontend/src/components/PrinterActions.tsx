import { useContext, useEffect, useState } from "react";
import { ToastContext } from "../contexts/ToastContext";
import { main } from "../../wailsjs/go/models";
import { errorText } from "../error";
import { executePrint } from "../functions/executePrint";
import { copyText } from "../functions/copyText";
import { SetLANPrinterImageMode } from "../../wailsjs/go/main/App";

interface PrinterActionsProps {
  printer: main.Printer;
}

export default function PrinterActions({ printer }: PrinterActionsProps) {
  const toastContext = useContext(ToastContext);
  const [copiedTarget, setCopiedTarget] = useState<
    "http" | "https" | "lan-http" | "lan-https" | null
  >(null);
  const [isTestPrinting, setIsTestPrinting] = useState(false);
  const [isCashDrawerOpening, setIsCashDrawerOpening] = useState(false);
  const [imageMode, setImageMode] = useState(printer.lanImageMode || "legacy");
  const [savingImageMode, setSavingImageMode] = useState(false);

  useEffect(() => {
    setImageMode(printer.lanImageMode || "legacy");
  }, [printer.lanImageMode]);
  const httpsIp = printer.httpsIp;
  const networkIp = printer.networkIp;
  const networkHttpsIp = printer.networkHttpsIp;

  async function onCopy(
    text: string,
    target: "http" | "https" | "lan-http" | "lan-https",
  ) {
    try {
      await copyText(text);
      setCopiedTarget(target);
      setTimeout(() => setCopiedTarget(null), 2000);
    } catch (err) {
      toastContext.actions.showToast(
        `Copy failed: ${errorText(err, "unknown error")}`,
        "danger",
      );
    }
  }

  async function onTest() {
    setIsTestPrinting(true);
    try {
      await executePrint(printer);
      toastContext.actions.showToast(
        `Test print sent to ${printer.name}`,
        "success",
      );
    } catch (err) {
      toastContext.actions.showToast(
        errorText(err, "Test print failed"),
        "danger",
      );
    } finally {
      setIsTestPrinting(false);
    }
  }

  async function onCashDrawerOpen() {
    setIsCashDrawerOpening(true);
    try {
      await executePrint(printer, true);
      toastContext.actions.showToast(
        `Cash drawer opened for ${printer.name}`,
        "success",
      );
    } catch (err) {
      toastContext.actions.showToast(
        errorText(err, "Failed to open the cash drawer"),
        "danger",
      );
    } finally {
      setIsCashDrawerOpening(false);
    }
  }

  async function changeImageMode(nextMode: string) {
    if (!printer.isLAN || !printer.lanIp) {
      return;
    }
    const previous = imageMode;
    setImageMode(nextMode);
    setSavingImageMode(true);
    try {
      await SetLANPrinterImageMode(printer.lanIp, nextMode);
      toastContext.actions.showToast("LAN image mode saved for " + printer.name, "success");
    } catch (err) {
      setImageMode(previous);
      toastContext.actions.showToast(errorText(err, "Unable to save image mode"), "danger");
    } finally {
      setSavingImageMode(false);
    }
  }

  return (
    <>
    <div className="flex gap-2 mt-4 flex-wrap">
      <button
        onClick={() => onCopy(printer.ip, "http")}
        className={`flex-1 border text-sm rounded-lg px-3 py-2 cursor-pointer whitespace-nowrap ${
          copiedTarget === "http"
            ? "bg-success text-white"
            : "bg-odoo text-white hover:bg-odoo-dark"
        }`}
      >
        {copiedTarget === "http" ? "✓ Copied!" : "Copy HTTP"}
      </button>

      {httpsIp && (
        <button
          onClick={() => onCopy(httpsIp, "https")}
          className={`flex-1 border text-sm rounded-lg px-3 py-2 cursor-pointer whitespace-nowrap ${
            copiedTarget === "https"
              ? "bg-success text-white"
              : "bg-odoo text-white hover:bg-odoo-dark"
          }`}
        >
          {copiedTarget === "https" ? "✓ Copied!" : "Copy HTTPS"}
        </button>
      )}

      {networkIp && (
        <button
          onClick={() => onCopy(networkIp, "lan-http")}
          className={`flex-1 border text-sm rounded-lg px-3 py-2 cursor-pointer whitespace-nowrap ${
            copiedTarget === "lan-http"
              ? "bg-success text-white"
              : "bg-odoo text-white hover:bg-odoo-dark"
          }`}
        >
          {copiedTarget === "lan-http" ? "✓ Copied!" : "Copy LAN HTTP"}
        </button>
      )}

      {networkHttpsIp && (
        <button
          onClick={() => onCopy(networkHttpsIp, "lan-https")}
          className={`flex-1 border text-sm rounded-lg px-3 py-2 cursor-pointer whitespace-nowrap ${
            copiedTarget === "lan-https"
              ? "bg-success text-white"
              : "bg-odoo text-white hover:bg-odoo-dark"
          }`}
        >
          {copiedTarget === "lan-https" ? "✓ Copied!" : "Copy LAN HTTPS"}
        </button>
      )}

      <button
        onClick={onTest}
        disabled={isTestPrinting}
        className="flex-1 border rounded-lg text-sm px-3 py-2 cursor-pointer border-gray-300 text-gray-600 hover:bg-gray-50 hover:border-gray-400 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {isTestPrinting ? "Printing..." : "Test"}
      </button>

      {printer.type === "receipt" && (
        <button
          onClick={onCashDrawerOpen}
          disabled={isCashDrawerOpening}
          className="flex-1 break-keep border rounded-lg text-sm px-3 py-2 cursor-pointer border-gray-300 text-gray-600 hover:bg-gray-50 hover:border-gray-400 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isCashDrawerOpening ? "Opening..." : "Cash Drawer"}
        </button>
      )}
    </div>
    {printer.isLAN && printer.lanIp && printer.type === "receipt" && (
      <div className="mt-3 text-sm">
        <label className="block text-gray-700 font-medium" htmlFor={`image-mode-${printer.id}`}>
          LAN receipt image mode
        </label>
        <select
          id={`image-mode-${printer.id}`}
          value={imageMode}
          disabled={savingImageMode}
          onChange={(event) => void changeImageMode(event.target.value)}
          className="mt-1 block w-full border border-gray-300 rounded-lg p-2 bg-white text-gray-800 disabled:opacity-50"
        >
          <option value="epson_graphics">Epson optimized (GS ( L) — TM-T88IV/V/VI/VII</option>
          <option value="legacy">Compatible (GS v 0) — fallback for other models</option>
        </select>
        <p className="mt-1 text-xs text-gray-500">
          Compatible is the safe default. Enable Epson optimized only for a verified
          TM-T88 model, and compare a real Odoo receipt. Changes affect new tickets.
        </p>
      </div>
    )}
    </>
  );
}

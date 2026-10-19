import { useEffect, useRef, useState } from "react";
import {
  ConfirmMerge,
  ConnectGoogleDrive,
  DisconnectGoogleDrive,
  GetSyncStatus,
  SyncNow,
} from "../../wailsjs/go/main/App";
import { EventsOn, LogDebug } from "../../wailsjs/runtime/runtime";
import { errorText, useNotices } from "@/components/Notices";
import type { SyncStatus } from "@/types";

// Google Drive sync state: the backend owns it and pushes every change as a
// "sync:status" event. Coming back to the window asks for a sync.
export function useSync() {
  const { notify } = useNotices();
  const [status, setStatus] = useState<SyncStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const statusRef = useRef<SyncStatus | null>(null);

  function apply(s: unknown) {
    if (s == null) return; // a void binding must never blank the status
    const next = s as SyncStatus;
    statusRef.current = next;
    setStatus(next);
  }

  useEffect(() => {
    let cancelled = false;
    GetSyncStatus()
      .then((s) => {
        if (!cancelled && statusRef.current === null) apply(s);
      })
      .catch((e) => LogDebug(`[Sync] status error: ${e}`));
    const off = EventsOn("sync:status", apply);
    return () => {
      cancelled = true;
      off();
    };
  }, []);

  // Back in the window: pick up what the other devices did meanwhile.
  useEffect(() => {
    function onFocus() {
      const s = statusRef.current;
      if (s && s.connected && s.state !== "merge_pending") {
        SyncNow().catch((e) => LogDebug(`[Sync] focus sync error: ${e}`));
      }
    }
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, []);

  // Runs a binding that returns the new status; no timeout, the user may be
  // in the browser for as long as they like.
  async function run(call: () => Promise<unknown>, failure: string) {
    setBusy(true);
    try {
      apply(await call());
    } catch (e) {
      LogDebug(`[Sync] ${failure}: ${e}`);
      notify("error", `Couldn't ${failure}: ${errorText(e)}`);
    } finally {
      setBusy(false);
    }
  }

  return {
    status,
    busy,
    connect: () => run(ConnectGoogleDrive, "connect to Google Drive"),
    confirmMerge: (merge: boolean) => run(() => ConfirmMerge(merge), "finish the first sync"),
    disconnect: () =>
      run(async () => {
        await DisconnectGoogleDrive();
        return GetSyncStatus();
      }, "disconnect from Google Drive"),
    syncNow: () => run(SyncNow, "sync"),
  };
}

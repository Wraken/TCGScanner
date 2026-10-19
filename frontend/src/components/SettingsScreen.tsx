import { ConfirmDialog } from "@/components/ConfirmDialog";
import type { useSync } from "@/hooks/useSync";
import type { SyncStatus } from "@/types";

const INTRO_TAIL =
  " folder in your Google Drive. Only that folder is accessible to the app.";

function when(s: string): string {
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString();
}

function statusLine(s: SyncStatus): string | null {
  switch (s.state) {
    case "up_to_date":
      return `Up to date · ${s.last_sync}`;
    case "syncing":
      return "Syncing…";
    case "offline":
      return "Offline, will retry";
    case "error":
      return `Sync failed: ${s.message}`;
    case "signed_out":
      return "Disconnected, sign in again";
    default:
      return null;
  }
}

export function SettingsScreen({ sync }: { sync: ReturnType<typeof useSync> }) {
  const { status, busy, connect, confirmMerge, disconnect, syncNow } = sync;
  const unconfigured = status?.state === "unconfigured";
  const connected = !!status && status.connected && status.state !== "signed_out" && !unconfigured;
  const line = status ? statusLine(status) : null;
  const merge = status?.state === "merge_pending" ? status.merge : null;

  return (
    <div className="h-full overflow-y-auto" style={{ padding: "36px 44px" }}>
      <h1 className="text-[32px] leading-tight">Settings</h1>

      <section className="flex flex-col gap-4 mt-8 max-w-xl" aria-labelledby="settings-sync">
        <h2 id="settings-sync" className="text-lg">Sync with Google Drive</h2>
        <p className="text-sm n-n400">
          Keep this collection in sync with your other devices through a{" "}
          <code translate="no">TCGScanner</code>
          {INTRO_TAIL}
        </p>

        {unconfigured && (
          <p className="text-sm n-n500">This build has no Google client id; see STATUS.md.</p>
        )}

        {status && connected ? (
          <>
            <p className="text-sm">Connected as {status.email}</p>
            {line && <p className="text-sm n-n400" role="status">{line}</p>}
            {status.devices.length > 0 && (
              <ul className="flex flex-col gap-1 text-sm n-n400">
                {status.devices.map((d) => (
                  <li key={d.id}>
                    {d.this_device
                      ? `${d.name} (this PC)`
                      : d.last_change
                        ? `${d.name} · last change ${when(d.last_change)}`
                        : d.name}
                  </li>
                ))}
              </ul>
            )}
            <div className="flex gap-2">
              <button className="n-btn n-btn-secondary" onClick={syncNow} disabled={busy || status.state === "merge_pending"}>
                Sync now
              </button>
              <button className="n-btn n-btn-secondary" onClick={disconnect} disabled={busy}>
                Disconnect
              </button>
            </div>
          </>
        ) : (
          <>
            {line && <p className="text-sm n-n400" role="status">{line}</p>}
            <div>
              <button className="n-btn n-btn-primary" onClick={connect} disabled={busy || unconfigured || !status}>
                Connect Google Drive
              </button>
            </div>
          </>
        )}
      </section>

      <ConfirmDialog
        open={merge !== null}
        title="Merge with your Drive collection?"
        description={
          merge
            ? `Your Drive already holds a collection: ${merge.remote_copies} copies from ${merge.remote_devices.join(", ")}. This PC has ${merge.local_copies} copies. Merge both into one collection? Nothing is removed; copies present on both sides are kept as separate copies.`
            : ""
        }
        confirmLabel="Merge"
        cancelLabel="Not now"
        onConfirm={() => confirmMerge(true)}
        onOpenChange={(open) => {
          if (!open) confirmMerge(false);
        }}
      />
    </div>
  );
}

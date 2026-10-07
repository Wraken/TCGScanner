import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { CircleAlert, CircleCheck, X } from "lucide-react";

// App-wide notices (toasts): the one place where an action's outcome lands
// when it has no inline spot of its own, so nothing fails silently.
// Successes are announced politely and fade after a few seconds; errors are
// announced assertively and stay until dismissed.

type Tone = "success" | "error";

interface Notice {
  id: number;
  tone: Tone;
  text: string;
}

interface NoticeApi {
  notify: (tone: Tone, text: string) => void;
}

const NoticeContext = createContext<NoticeApi>({ notify: () => {} });

const SUCCESS_MS = 5000;

export function useNotices(): NoticeApi {
  return useContext(NoticeContext);
}

// Wails rejects with a plain string; Error objects carry the reason in message.
export function errorText(e: unknown): string {
  const s = e instanceof Error ? e.message : String(e);
  return s.replace(/^Error:\s*/, "");
}

export function NoticeProvider({ children }: { children: ReactNode }) {
  const [notices, setNotices] = useState<Notice[]>([]);
  const nextId = useRef(1);

  const dismiss = useCallback((id: number) => {
    setNotices((prev) => prev.filter((n) => n.id !== id));
  }, []);

  const notify = useCallback((tone: Tone, text: string) => {
    const id = nextId.current++;
    // Newest last; keep the stack short.
    setNotices((prev) => [...prev.slice(-2), { id, tone, text }]);
  }, []);

  return (
    <NoticeContext.Provider value={{ notify }}>
      {children}
      {/* Two live regions, present from the start so screen readers pick up
          what lands in them. */}
      <div className="n-notices">
        <div role="status" aria-live="polite" className="flex flex-col gap-2">
          {notices.filter((n) => n.tone === "success").map((n) => (
            <NoticeItem key={n.id} notice={n} onDismiss={dismiss} />
          ))}
        </div>
        <div role="alert" aria-live="assertive" className="flex flex-col gap-2">
          {notices.filter((n) => n.tone === "error").map((n) => (
            <NoticeItem key={n.id} notice={n} onDismiss={dismiss} />
          ))}
        </div>
      </div>
    </NoticeContext.Provider>
  );
}

function NoticeItem({ notice, onDismiss }: { notice: Notice; onDismiss: (id: number) => void }) {
  useEffect(() => {
    if (notice.tone !== "success") return;
    const t = setTimeout(() => onDismiss(notice.id), SUCCESS_MS);
    return () => clearTimeout(t);
  }, [notice, onDismiss]);

  const Icon = notice.tone === "error" ? CircleAlert : CircleCheck;
  return (
    <div className="n-notice" data-tone={notice.tone}>
      <Icon
        className={`w-4 h-4 shrink-0 mt-[5px] ${notice.tone === "error" ? "n-danger" : "n-brand"}`}
        aria-hidden="true"
      />
      <p className="n-notice-text">{notice.text}</p>
      <button className="n-btn n-btn-ghost n-btn-icon-sm shrink-0" onClick={() => onDismiss(notice.id)}>
        <X className="w-4 h-4" aria-hidden="true" />
        <span className="sr-only">Dismiss</span>
      </button>
    </div>
  );
}

import { Camera, Layers, LayoutGrid, Settings, Upload, WalletCards } from "lucide-react";

export type Screen = "home" | "scan" | "collection" | "settings";

interface Props {
  screen: Screen;
  onNavigate: (s: Screen) => void;
  // Opens the export dialog (an action, not a screen).
  onExport: () => void;
}

const SCREENS: { id: Screen; label: string; Icon: typeof Camera }[] = [
  { id: "home", label: "Home", Icon: LayoutGrid },
  { id: "scan", label: "Scan", Icon: Camera },
  { id: "collection", label: "Collections", Icon: Layers },
];

export function Sidebar({ screen, onNavigate, onExport }: Props) {
  return (
    <aside
      className="flex flex-col shrink-0 w-[216px] border-r"
      style={{ borderColor: "var(--n-divider)", padding: "20px 14px" }}
    >
      <div className="flex items-center gap-2.5 px-2 mb-6">
        <div
          className="flex items-center justify-center w-[30px] h-[30px] rounded-lg"
          style={{ border: "1px solid var(--n-accent)" }}
        >
          <WalletCards className="w-4 h-4 n-brand" aria-hidden="true" />
        </div>
        <span className="text-[15px] font-medium" translate="no">TCGScanner</span>
      </div>

      <nav aria-label="Main" className="flex flex-col gap-1">
        {SCREENS.map(({ id, label, Icon }) => (
          <button
            key={id}
            className="n-nav"
            aria-current={screen === id ? "page" : undefined}
            onClick={() => onNavigate(id)}
          >
            <Icon className="w-[18px] h-[18px]" aria-hidden="true" />
            {label}
          </button>
        ))}
        <button className="n-nav" onClick={onExport} aria-haspopup="dialog">
          <Upload className="w-[18px] h-[18px]" aria-hidden="true" />
          Export
        </button>
      </nav>

      <button
        className="n-nav mt-auto"
        aria-current={screen === "settings" ? "page" : undefined}
        onClick={() => onNavigate("settings")}
      >
        <Settings className="w-[18px] h-[18px]" aria-hidden="true" />
        Settings
      </button>
    </aside>
  );
}

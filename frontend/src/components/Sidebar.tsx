import { Camera, Layers, LayoutGrid, Settings, Upload, WalletCards } from "lucide-react";

export type Screen = "home" | "scan" | "collection" | "detail";

interface Props {
  screen: Screen;
  onNavigate: (s: Screen) => void;
  onCollections: () => void;
  onExport: () => void;
}

export function Sidebar({ screen, onNavigate, onCollections, onExport }: Props) {
  return (
    <aside
      className="flex flex-col shrink-0 w-[216px] border-r"
      style={{ borderColor: "var(--n-divider)", padding: "20px 14px" }}
    >
      {/* Brand */}
      <div className="flex items-center gap-2.5 px-2 mb-6">
        <div
          className="flex items-center justify-center w-[30px] h-[30px] rounded-lg"
          style={{ border: "1px solid var(--n-accent)" }}
        >
          <WalletCards className="w-4 h-4 n-brand" />
        </div>
        <span className="text-[15px] font-medium">TCGScanner</span>
      </div>

      {/* Primary nav */}
      <nav className="flex flex-col gap-1">
        <button className="n-nav" data-active={screen === "home"} onClick={() => onNavigate("home")}>
          <LayoutGrid className="w-[18px] h-[18px]" />
          Accueil
        </button>
        <button className="n-nav" data-active={screen === "scan"} onClick={() => onNavigate("scan")}>
          <Camera className="w-[18px] h-[18px]" />
          Scanner
        </button>
        <button className="n-nav" onClick={onCollections}>
          <Layers className="w-[18px] h-[18px]" />
          Collections
        </button>
        <button className="n-nav" onClick={onExport}>
          <Upload className="w-[18px] h-[18px]" />
          Export
        </button>
      </nav>

      {/* Settings pinned to bottom */}
      <button className="n-nav mt-auto" disabled title="Bientôt disponible">
        <Settings className="w-[18px] h-[18px]" />
        Réglages
      </button>
    </aside>
  );
}

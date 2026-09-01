import { Camera, Layers, LayoutGrid, Settings, Upload, WalletCards } from "lucide-react";

export type Screen = "home" | "scan" | "collection";

interface Props {
  screen: Screen;
  onNavigate: (s: Screen) => void;
}

export function Sidebar({ screen, onNavigate }: Props) {
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
          <WalletCards className="w-4 h-4 n-brand" />
        </div>
        <span className="text-[15px] font-medium">TCGScanner</span>
      </div>

      <nav className="flex flex-col gap-1">
        <button className="n-nav" data-active={screen === "home"} onClick={() => onNavigate("home")}>
          <LayoutGrid className="w-[18px] h-[18px]" />
          Home
        </button>
        <button className="n-nav" data-active={screen === "scan"} onClick={() => onNavigate("scan")}>
          <Camera className="w-[18px] h-[18px]" />
          Scan
        </button>
        <button className="n-nav" data-active={screen === "collection"} onClick={() => onNavigate("collection")}>
          <Layers className="w-[18px] h-[18px]" />
          Collections
        </button>
        <button className="n-nav" onClick={() => onNavigate("collection")}>
          <Upload className="w-[18px] h-[18px]" />
          Export
        </button>
      </nav>

      <button className="n-nav mt-auto" disabled title="Coming soon">
        <Settings className="w-[18px] h-[18px]" />
        Settings
      </button>
    </aside>
  );
}

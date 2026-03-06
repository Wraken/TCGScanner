import { Check, Loader2 } from "lucide-react";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface Props {
  models: string[];
  selectedModel: string;
  modelLoading: boolean;
  modelLoaded: boolean;
  modelError: string;
  onModelChange: (name: string) => void;
}

export function ModelSelector({ models, selectedModel, modelLoading, modelLoaded, modelError, onModelChange }: Props) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        Model
      </Label>
      <div className="flex items-center gap-2">
        <Select value={selectedModel} onValueChange={onModelChange} disabled={modelLoading}>
          <SelectTrigger>
            <SelectValue placeholder="Select a model" />
          </SelectTrigger>
          <SelectContent>
            {models.map((m) => (
              <SelectItem key={m} value={m}>{m}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        {modelLoading && <Loader2 className="w-4 h-4 shrink-0 animate-spin text-muted-foreground" />}
        {modelLoaded && !modelLoading && <Check className="w-4 h-4 shrink-0 text-emerald-400" />}
      </div>
      {modelError && <p className="text-xs text-destructive">{modelError}</p>}
    </div>
  );
}

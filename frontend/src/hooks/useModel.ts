import { useEffect, useState } from "react";
import { ListModels, LoadModel } from "../../wailsjs/go/main/App";

export function useModel() {
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>("");
  const [modelLoading, setModelLoading] = useState(false);
  const [modelError, setModelError] = useState<string>("");
  const [modelLoaded, setModelLoaded] = useState(false);

  useEffect(() => {
    refreshModels();
  }, []);

  async function refreshModels() {
    try {
      const list = await ListModels();
      setModels(list ?? []);
    } catch {
      setModelError("Failed to list models.");
    }
  }

  async function handleModelChange(name: string) {
    setSelectedModel(name);
    setModelLoaded(false);
    setModelError("");
    setModelLoading(true);
    try {
      await LoadModel(name);
      setModelLoaded(true);
    } catch (e) {
      setModelError(`Failed to load model: ${e}`);
    } finally {
      setModelLoading(false);
    }
  }

  return { models, selectedModel, modelLoaded, modelLoading, modelError, handleModelChange, refreshModels };
}

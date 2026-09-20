import { create } from "zustand";

// Unused starter store retained for future UI-only state. Production jobs and
// preferences are owned by the Go service and SQLite, not this placeholder.

interface AppState {
  // Placeholder state only; add real fields before using this store.
  example: string[];
  setExample: (example: string[]) => void;
}

export const useAppStore = create<AppState>((set) => ({
  example: [],
  setExample: (example) => set({ example }),
}));

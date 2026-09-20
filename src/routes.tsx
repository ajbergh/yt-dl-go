import type { ReactElement } from "react";
import { HomeIcon, type LucideIcon } from "lucide-react";
import { HomePage } from "@/pages/home";

export interface AppRoute {
  /** "/" is the index route; others are paths under the shell. */
  path: string;
  element: ReactElement;
  /** Optional descriptive metadata; the current HomePage owns its own tabs. */
  label?: string;
  icon?: LucideIcon;
}

// App.tsx builds the router from this list. The queue, library, and settings
// tabs are views inside HomePage, not separate URL routes.
export const routes: AppRoute[] = [
  { path: "/", element: <HomePage />, label: "Home", icon: HomeIcon },
];

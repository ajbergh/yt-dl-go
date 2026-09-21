import { Outlet } from "react-router-dom";

// Shared route frame. The downloader owns its header and tabs inside HomePage.
export function AppShell() {
  return (
    <div className="flex min-h-screen flex-col">
      <main className="flex flex-1 flex-col">
        <Outlet />
      </main>
    </div>
  );
}

export default AppShell;

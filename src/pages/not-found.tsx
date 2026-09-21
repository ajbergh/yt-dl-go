import { Link } from "react-router-dom";

export function NotFoundPage() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 py-24">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <p className="text-muted-foreground">That page doesn't exist.</p>
      <Link
        to="/"
        className="rounded-lg bg-rose-600 px-4 py-2 text-sm font-semibold text-white hover:bg-rose-500"
      >
        Go home
      </Link>
    </div>
  );
}

export default NotFoundPage;

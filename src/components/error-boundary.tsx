import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { reportPrivateRuntimeError } from "@/lib/console-capture";
import { postAppRuntimeErrorToCoworkParent } from "@/lib/cowork-parent-transport";

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Keep error details in local diagnostics; only the fixed crash signal is
    // sent to a compatible embedding host.
    reportPrivateRuntimeError("[App Error]", error, info.componentStack);

    // Tell a compatible embedding host the preview crashed, using a fixed,
    // detail-free signal. No error name/message/stack/URL crosses the boundary.
    try {
      postAppRuntimeErrorToCoworkParent();
    } catch (postError) {
      reportPrivateRuntimeError("[App Error] failed to post crash signal", postError);
    }
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex min-h-screen items-center justify-center p-8">
          <div className="max-w-lg rounded-lg border border-destructive/20 bg-destructive/10 p-6">
            <h2 className="mb-2 text-lg font-semibold text-destructive">
              Something went wrong
            </h2>
            <p className="font-mono text-sm text-destructive/80">
              {this.state.error?.message}
            </p>
            <Button
              variant="destructive"
              className="mt-4"
              onClick={() => this.setState({ hasError: false, error: null })}
            >
              Try Again
            </Button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

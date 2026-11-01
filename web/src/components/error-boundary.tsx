import * as React from "react";
import i18n from "@/i18n";
import { Button } from "@/components/ui/button";

interface State {
  error: Error | null;
}

/** ErrorBoundary prevents a render error from blanking the whole SPA. */
export class ErrorBoundary extends React.Component<{ children: React.ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("render error", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="flex min-h-full items-center justify-center p-6">
          <div className="w-full max-w-md space-y-4 rounded-lg border border-destructive/40 bg-destructive/5 p-6 text-center">
            <h1 className="text-lg font-semibold">{i18n.t("errors.generic")}</h1>
            <p className="break-words text-sm text-muted-foreground">{this.state.error.message}</p>
            <div className="flex justify-center gap-2">
              <Button onClick={() => this.setState({ error: null })}>{i18n.t("common.retry")}</Button>
              <Button variant="outline" onClick={() => window.location.reload()}>
                {i18n.t("common.refresh")}
              </Button>
            </div>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { AuthProvider } from "./auth";
import { ErrorBoundary } from "./components/error-boundary";
import { App } from "./App";
import { applyTheme, readTheme, useTheme } from "./lib/theme";
import "./i18n";
import "./index.css";

// Apply before the first paint so light mode never flashes dark.
applyTheme(readTheme());

function ThemedToaster() {
  const { theme } = useTheme();
  return <Toaster theme={theme} richColors closeButton position="top-right" />;
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 10_000,
    },
  },
});

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <BrowserRouter>
          <ErrorBoundary>
            <App />
          </ErrorBoundary>
          <ThemedToaster />
        </BrowserRouter>
      </AuthProvider>
    </QueryClientProvider>
  </React.StrictMode>,
);

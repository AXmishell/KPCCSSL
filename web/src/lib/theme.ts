import * as React from "react";

export type Theme = "light" | "dark";

// The suffix keeps preferences written by older builds (which defaulted to
// dark) from pinning the UI to dark after the default changed to light.
const STORAGE_KEY = "kpcc_theme_v2";
const THEME_EVENT = "kpcc:theme-change";

export function readStoredTheme(): Theme | null {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return value === "light" || value === "dark" ? value : null;
  } catch {
    return null;
  }
}

export function readTheme(): Theme {
  return readStoredTheme() ?? "light";
}

export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  root.style.colorScheme = theme;
}

function storeTheme(theme: Theme) {
  try {
    window.localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // Private mode or storage disabled: still apply for this session.
  }
  applyTheme(theme);
  window.dispatchEvent(new CustomEvent<Theme>(THEME_EVENT, { detail: theme }));
}

/** Keep the document theme and every mounted consumer in sync. */
export function useTheme() {
  const [theme, setTheme] = React.useState<Theme>(readTheme);

  React.useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  React.useEffect(() => {
    const syncStorage = () => setTheme(readTheme());
    const syncEvent = (event: Event) => setTheme((event as CustomEvent<Theme>).detail);
    window.addEventListener("storage", syncStorage);
    window.addEventListener(THEME_EVENT, syncEvent);
    return () => {
      window.removeEventListener("storage", syncStorage);
      window.removeEventListener(THEME_EVENT, syncEvent);
    };
  }, []);

  const update = React.useCallback((next: Theme) => {
    setTheme(next);
    storeTheme(next);
  }, []);

  return { theme, setTheme: update };
}

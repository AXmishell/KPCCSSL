import * as React from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Search } from "lucide-react";
import { cn } from "@/lib/utils";

type Option = {
  value: string;
  label: string;
  disabled?: boolean;
};

type Placement = {
  left: number;
  top: number;
  width: number;
  maxHeight: number;
  absolute: boolean;
};

function textOf(node: React.ReactNode): string {
  if (node === null || node === undefined || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (React.isValidElement(node)) {
    return textOf((node.props as { children?: React.ReactNode }).children);
  }
  return "";
}

function collectOptions(children: React.ReactNode): Option[] {
  const options: Option[] = [];
  const visit = (nodes: React.ReactNode) => {
    React.Children.forEach(nodes, (child) => {
      if (!React.isValidElement(child)) return;
      if (child.type === "optgroup") {
        visit((child.props as React.OptgroupHTMLAttributes<HTMLOptGroupElement>).children);
        return;
      }
      if (child.type !== "option") return;
      const props = child.props as React.OptionHTMLAttributes<HTMLOptionElement>;
      options.push({
        value: String(props.value ?? ""),
        label: props.label ?? (textOf(props.children) || String(props.value ?? "")),
        disabled: props.disabled,
      });
    });
  };
  visit(children);
  return options;
}

/**
 * Native select wrapped in a custom listbox so the trigger and the option list
 * can be themed. The real <select> stays in the DOM (visually hidden) so React
 * Hook Form, form submission and onChange handlers keep working unchanged.
 * Pass `searchable` for long lists to show a filter input in the popup.
 */
export type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement> & {
  searchable?: boolean;
  searchPlaceholder?: string;
};

export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  function Select(
    {
      className,
      children,
      disabled,
      id,
      value,
      defaultValue,
      searchable = false,
      searchPlaceholder = "Search…",
      "aria-label": ariaLabel,
      "aria-labelledby": ariaLabelledby,
      "aria-describedby": ariaDescribedby,
      "aria-invalid": ariaInvalid,
      ...props
    },
    forwardedRef,
  ) {
    const selectRef = React.useRef<HTMLSelectElement | null>(null);
    const triggerRef = React.useRef<HTMLButtonElement | null>(null);
    const listRef = React.useRef<HTMLDivElement | null>(null);
    const searchRef = React.useRef<HTMLInputElement | null>(null);
    const typeahead = React.useRef({ text: "", at: 0 });

    const listId = React.useId();
    const options = React.useMemo(() => collectOptions(children), [children]);
    const controlled = value !== undefined;
    const [uncontrolled, setUncontrolled] = React.useState<string>(() => String(defaultValue ?? ""));
    const [open, setOpen] = React.useState(false);
    const [query, setQuery] = React.useState("");
    const [active, setActive] = React.useState(-1);
    const [placement, setPlacement] = React.useState<Placement | null>(null);
    const [container, setContainer] = React.useState<HTMLElement | null>(null);

    const visible = React.useMemo(() => {
      const needle = query.trim().toLowerCase();
      if (!searchable || !needle) return options;
      return options.filter(
        (option) => option.label.toLowerCase().includes(needle) || option.value.toLowerCase().includes(needle),
      );
    }, [options, query, searchable]);

    const current = controlled ? String(value ?? "") : uncontrolled;
    const selectedIndex = options.findIndex((option) => option.value === current);
    const selected = selectedIndex >= 0 ? options[selectedIndex] : undefined;

    React.useEffect(() => {
      if (controlled) return;
      const element = selectRef.current;
      if (element && element.value !== uncontrolled) setUncontrolled(element.value);
    });

    const setRefs = React.useCallback(
      (node: HTMLSelectElement | null) => {
        selectRef.current = node;
        if (typeof forwardedRef === "function") forwardedRef(node);
        else if (forwardedRef) forwardedRef.current = node;
      },
      [forwardedRef],
    );

    const measure = React.useCallback(() => {
      const trigger = triggerRef.current;
      if (!trigger) return;
      const dialog = trigger.closest('[role="dialog"]') as HTMLElement | null;
      const rect = trigger.getBoundingClientRect();
      const bounds = dialog?.getBoundingClientRect();

      const left = dialog && bounds ? rect.left - bounds.left + dialog.scrollLeft : rect.left;
      const top = dialog && bounds ? rect.top - bounds.top + dialog.scrollTop : rect.top;
      const spaceBelow = ((dialog && bounds ? bounds.bottom : window.innerHeight) - rect.bottom) - 8;
      const spaceAbove = (rect.top - (dialog && bounds ? bounds.top : 0)) - 8;
      // Keep the popup height aligned to option rows so a half-cut row is
      // never visible at the bottom edge.
      const rowHeight = 32;
      const chrome = 8 + (searchable ? 40 : 0);
      const wanted = options.length * rowHeight + chrome;
      const openUp = spaceBelow < Math.min(wanted, 200) && spaceAbove > spaceBelow;
      const available = Math.min(320, Math.max(96, openUp ? spaceAbove : spaceBelow));
      const rows = Math.max(1, Math.floor((available - chrome) / rowHeight));
      const maxHeight = chrome + rows * rowHeight;
      const height = Math.min(wanted, maxHeight);

      setContainer(dialog ?? document.body);
      setPlacement({
        left,
        top: openUp ? top - height - 4 : top + rect.height + 4,
        width: rect.width,
        maxHeight,
        absolute: Boolean(dialog),
      });
    }, [options.length, searchable]);

    const openMenu = React.useCallback(() => {
      if (disabled) return;
      if (!controlled && selectRef.current) setUncontrolled(selectRef.current.value);
      setQuery("");
      setActive(selectedIndex);
      setOpen(true);
    }, [controlled, disabled, selectedIndex]);

    const closeMenu = React.useCallback((refocus = false) => {
      setOpen(false);
      setQuery("");
      setPlacement(null);
      if (refocus) triggerRef.current?.focus();
    }, []);

    React.useEffect(() => {
      if (open && searchable) searchRef.current?.focus();
    }, [open, searchable]);

    const commit = React.useCallback(
      (next: string) => {
        const element = selectRef.current;
        if (element) {
          // Bypass React's value tracker so the synthetic change event is
          // observed by onChange handlers and by React Hook Form.
          const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
          setter?.call(element, next);
          element.dispatchEvent(new Event("change", { bubbles: true }));
        }
        setUncontrolled(next);
        closeMenu(true);
      },
      [closeMenu],
    );

    React.useLayoutEffect(() => {
      if (open) measure();
    }, [open, measure]);

    React.useEffect(() => {
      if (!open) return;
      const onPointerDown = (event: PointerEvent) => {
        const target = event.target as Node;
        if (listRef.current?.contains(target) || triggerRef.current?.contains(target)) return;
        closeMenu();
      };
      const onScroll = (event: Event) => {
        if (listRef.current && event.target instanceof Node && listRef.current.contains(event.target)) return;
        closeMenu();
      };
      document.addEventListener("pointerdown", onPointerDown, true);
      window.addEventListener("scroll", onScroll, true);
      window.addEventListener("resize", onScroll);
      return () => {
        document.removeEventListener("pointerdown", onPointerDown, true);
        window.removeEventListener("scroll", onScroll, true);
        window.removeEventListener("resize", onScroll);
      };
    }, [open, closeMenu]);

    React.useEffect(() => {
      if (!open || active < 0) return;
      document.getElementById(`${listId}-${active}`)?.scrollIntoView?.({ block: "nearest" });
    }, [open, active, listId]);

    const onKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
      if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        if (!open) {
          openMenu();
          return;
        }
        if (event.key === "ArrowDown") setActive((index) => Math.min(visible.length - 1, index + 1));
        else if (event.key === "ArrowUp") setActive((index) => Math.max(0, index - 1));
        else {
          const option = visible[active];
          if (option && !option.disabled) commit(option.value);
        }
        return;
      }
      if (event.key === "Escape" && open) {
        event.preventDefault();
        event.stopPropagation();
        closeMenu(true);
        return;
      }
      if (event.key === "Home" && open) {
        event.preventDefault();
        setActive(0);
        return;
      }
      if (event.key === "End" && open) {
        event.preventDefault();
        setActive(visible.length - 1);
        return;
      }
      if (event.key === "Tab" && open) closeMenu();
      if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey) {
        const now = Date.now();
        const text = now - typeahead.current.at < 500 ? typeahead.current.text + event.key : event.key;
        typeahead.current = { text, at: now };
        const index = visible.findIndex((option) => option.label.toLowerCase().startsWith(text.toLowerCase()));
        if (index >= 0) {
          if (!open) openMenu();
          setActive(index);
        }
      }
    };

    const onSearchKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setActive((index) => Math.min(visible.length - 1, index + 1));
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        setActive((index) => Math.max(0, index - 1));
      } else if (event.key === "Enter") {
        event.preventDefault();
        const option = visible[active];
        if (option && !option.disabled) commit(option.value);
      } else if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        closeMenu(true);
      } else if (event.key === "Tab") {
        closeMenu();
      }
    };

    return (
      <div className={cn("relative w-full", className)}>
        <select
          {...props}
          ref={setRefs}
          id={`${id ?? listId}-native`}
          disabled={disabled}
          value={value}
          defaultValue={defaultValue}
          className="sr-only"
          tabIndex={-1}
          aria-hidden="true"
        >
          {children}
        </select>
        <button
          ref={triggerRef}
          type="button"
          id={id}
          disabled={disabled}
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={open ? listId : undefined}
          aria-activedescendant={open && active >= 0 ? `${listId}-${active}` : undefined}
          aria-label={ariaLabel}
          aria-labelledby={ariaLabelledby}
          aria-describedby={ariaDescribedby}
          aria-invalid={ariaInvalid}
          onClick={() => (open ? closeMenu() : openMenu())}
          onKeyDown={onKeyDown}
          className={cn(
            "flex h-9 w-full items-center justify-between gap-2 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors",
            "hover:border-ring/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            open && "border-ring ring-1 ring-ring/30",
            "disabled:cursor-not-allowed disabled:opacity-50",
          )}
        >
          <span className={cn("min-w-0 flex-1 truncate text-left", !selected && "text-muted-foreground")}>
            {selected?.label ?? "\u00a0"}
          </span>
          <ChevronDown
            aria-hidden="true"
            className={cn("h-4 w-4 shrink-0 text-muted-foreground transition-transform duration-150", open && "rotate-180")}
          />
        </button>
        {open &&
          placement &&
          container &&
          createPortal(
            <div
              ref={listRef}
              data-select-open="true"
              style={{
                position: placement.absolute ? "absolute" : "fixed",
                left: placement.left,
                top: placement.top,
                width: placement.width,
                maxHeight: placement.maxHeight,
              }}
              className="pointer-events-auto z-[100] overflow-y-auto overscroll-contain rounded-md border border-border bg-card p-1 text-card-foreground shadow-xl ring-1 ring-black/5 scrollbar-thin animate-fade-in"
            >
              {searchable && (
                <div className="sticky top-0 z-10 -mx-1 -mt-1 mb-1 border-b border-border bg-card p-1">
                  <div className="relative">
                    <Search
                      aria-hidden="true"
                      className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
                    />
                    <input
                      ref={searchRef}
                      type="text"
                      value={query}
                      placeholder={searchPlaceholder}
                      aria-label={searchPlaceholder}
                      onChange={(event) => {
                        setQuery(event.target.value);
                        setActive(0);
                      }}
                      onKeyDown={onSearchKeyDown}
                      className="h-8 w-full rounded-sm border border-input bg-background pl-7 pr-2 text-sm text-foreground outline-none transition-colors placeholder:text-muted-foreground focus:border-ring focus:ring-1 focus:ring-ring"
                    />
                  </div>
                </div>
              )}
              <div role="listbox" id={listId} aria-labelledby={id}>
                {visible.map((option, index) => (
                  <div
                    key={`${option.value}-${index}`}
                    id={`${listId}-${index}`}
                    role="option"
                    aria-selected={option.value === current}
                    aria-disabled={option.disabled || undefined}
                    onMouseEnter={() => setActive(index)}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => {
                      if (!option.disabled) commit(option.value);
                    }}
                    className={cn(
                      "flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm",
                      active === index && "bg-accent text-accent-foreground",
                      option.value === current && "font-medium",
                      option.disabled && "cursor-not-allowed opacity-50",
                    )}
                  >
                    <span className="flex-1 truncate">{option.label}</span>
                    {option.value === current && <Check aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-primary" />}
                  </div>
                ))}
              </div>
              {visible.length === 0 && <div className="px-2 py-1.5 text-sm text-muted-foreground">—</div>}
            </div>,
            container,
          )}
      </div>
    );
  },
);
Select.displayName = "Select";

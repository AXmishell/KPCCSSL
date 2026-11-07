import { NavLink } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

const tabs = [
  { to: "/certificates", labelKey: "nav.certificates", end: true },
  { to: "/workflows", labelKey: "nav.workflows", end: false },
];

/** Section tabs shared by the certificate list and the workflows page. */
export function CertSectionTabs() {
  const { t } = useTranslation();

  return (
    <div className="mb-4 flex w-fit gap-1 rounded-lg border border-border bg-muted/40 p-1">
      {tabs.map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          end={tab.end}
          className={({ isActive }) =>
            cn(
              "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
              isActive
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )
          }
        >
          {t(tab.labelKey)}
        </NavLink>
      ))}
    </div>
  );
}

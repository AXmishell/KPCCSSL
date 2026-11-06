import * as React from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ExternalLink, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/auth";
import { qk, useDNS, useDNSProviders } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import type { DNSCredential, DNSProvider } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState, ErrorBox, PageHeader, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";

export function DNSCredentialsPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { data, isLoading } = useDNS();
  const { data: providers } = useDNSProviders();
  const qc = useQueryClient();
  const [editing, setEditing] = React.useState<DNSCredential | null>(null);
  const [open, setOpen] = React.useState(false);
  const isAdmin = user?.role === "admin";

  const providerName = (id: string) => providers?.items.find((p) => p.id === id)?.name ?? id;

  return (
    <div>
      <PageHeader
        title={t("dns.title")}
        subtitle={t("dns.subtitle")}
        actions={
          isAdmin && (
            <Button
              onClick={() => {
                setEditing(null);
                setOpen(true);
              }}
            >
              <Plus className="h-4 w-4" /> {t("dns.add")}
            </Button>
          )
        }
      />
      {isLoading ? null : !data?.items.length ? (
        <EmptyState description={t("dns.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("dns.provider")}</Th>
              <Th>{t("dns.fieldsConfigured")}</Th>
              <Th>{t("common.status")}</Th>
              <Th>{t("deploy.lastRun")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {(data?.items ?? []).map((d) => (
              <tr key={d.id}>
                <Td className="font-medium">{d.name}</Td>
                <Td>{providerName(d.provider)}</Td>
                <Td>
                  <div className="flex flex-wrap gap-1">
                    {d.configured_fields.map((f) => (
                      <Badge key={f} variant="outline" className="font-mono text-[10px]">
                        {f}
                      </Badge>
                    ))}
                  </div>
                </Td>
                <Td>
                  <Badge variant={statusVariant(d.status)}>{t(`status.${d.status}`)}</Badge>
                  {d.last_error && <p className="mt-1 max-w-[16rem] truncate text-xs text-destructive">{d.last_error}</p>}
                </Td>
                <Td className="text-muted-foreground">{d.last_used_at ? formatDateTime(d.last_used_at) : t("common.never")}</Td>
                <Td className="text-right">
                  {isAdmin && (
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={async () => {
                          try {
                            await request(`/dns-credentials/${d.id}/verify`, { method: "POST", body: {} });
                            toast.success(t("dns.verified"));
                            void qc.invalidateQueries();
                          } catch (err) {
                            toast.error(errorMessage(err));
                          }
                        }}
                      >
                        {t("dns.verify")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setEditing(d);
                          setOpen(true);
                        }}
                      >
                        {t("common.edit")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={async () => {
                          if (!window.confirm(t("dns.deleteConfirm", { name: d.name }))) return;
                          try {
                            await request(`/dns-credentials/${d.id}`, { method: "DELETE" });
                            toast.success(t("dns.deleted"));
                            void qc.invalidateQueries();
                          } catch (err) {
                            toast.error(errorMessage(err));
                          }
                        }}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </div>
                  )}
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}

      <DNSCredentialDialog
        open={open}
        onOpenChange={setOpen}
        editing={editing}
        providers={providers?.items ?? []}
      />
    </div>
  );
}

function DNSCredentialDialog({
  open,
  onOpenChange,
  editing,
  providers,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  editing: DNSCredential | null;
  providers: DNSProvider[];
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  const [provider, setProvider] = React.useState("dns_cf");
  const [config, setConfig] = React.useState<Record<string, string>>({});
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const selected = providers.find((p) => p.id === provider);

  React.useEffect(() => {
    if (open) {
      setName(editing?.name ?? "");
      setProvider(editing?.provider ?? "dns_cf");
      setConfig({});
      setError("");
    }
  }, [open, editing]);

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const body: Record<string, unknown> = { name, provider, config };
      if (editing) {
        const filled = Object.fromEntries(Object.entries(config).filter(([, v]) => v !== ""));
        if (Object.keys(filled).length === 0) delete body.config;
        await request(`/dns-credentials/${editing.id}`, { method: "PATCH", body });
        toast.success(t("dns.updated"));
      } else {
        await request("/dns-credentials", { method: "POST", body });
        toast.success(t("dns.created"));
      }
      await qc.invalidateQueries({ queryKey: qk.dns });
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{editing ? t("common.edit") : t("dns.add")}</DialogTitle>
          {selected && (selected.docs || selected.min_role) && (
            <DialogDescription>
              {selected.docs && (
                <a href={selected.docs} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
                  {t("dns.providerDocs")} <ExternalLink className="h-3 w-3" />
                </a>
              )}
              {selected.min_role && (
                <span className="mt-1 block">{t("dns.minRole")}: {selected.min_role}</span>
              )}
            </DialogDescription>
          )}
        </DialogHeader>
        <div className="space-y-4">
          {error && <ErrorBox message={error} />}
          <Field label={t("common.name")}>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t("dns.provider")}>
            <Select
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value);
                setConfig({});
              }}
              disabled={Boolean(editing)}
              searchable
              searchPlaceholder={t("common.searchPlaceholder")}
            >
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
          {(selected?.fields ?? []).map((field) => (
            <Field
              key={field.key}
              label={`${field.label}${field.required ? " *" : ""}`}
              hint={field.help || (editing ? t("dns.keepSecret") : undefined)}
            >
              <Input
                type={field.secret ? "password" : "text"}
                autoComplete="new-password"
                value={config[field.key] ?? ""}
                placeholder={editing && editing.configured_fields.includes(field.key) ? "••••••••" : ""}
                onChange={(e) => setConfig((c) => ({ ...c, [field.key]: e.target.value }))}
              />
            </Field>
          ))}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={submit} disabled={busy || !name || !provider}>
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

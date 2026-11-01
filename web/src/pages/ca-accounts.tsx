import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw } from "lucide-react";
import { useAuth } from "@/auth";
import { qk, useAction, useCA } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import type { CAAccount } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState, ErrorBox, PageHeader, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";

export function CAAccountsPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { data, isLoading } = useCA();
  const [open, setOpen] = React.useState(false);
  const isAdmin = user?.role === "admin";

  const register = useAction(
    (id: string) => request<{ job_id: string }>(`/ca-accounts/${id}/register`, { method: "POST", body: {} }),
    { success: t("ca.registerQueued") },
  );

  return (
    <div>
      <PageHeader
        title={t("ca.title")}
        subtitle={t("ca.subtitle")}
        actions={
          isAdmin && (
            <Button onClick={() => setOpen(true)}>
              <Plus className="h-4 w-4" /> {t("ca.add")}
            </Button>
          )
        }
      />
      {isLoading ? null : !data?.items.length ? (
        <EmptyState description={t("ca.empty")} action={isAdmin ? <Button onClick={() => setOpen(true)}>{t("ca.add")}</Button> : undefined} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("ca.directory")}</Th>
              <Th>{t("common.status")}</Th>
              <Th>{t("auth.email")}</Th>
              <Th>{t("common.createdAt")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {(data?.items ?? []).map((ca) => (
              <tr key={ca.id}>
                <Td>
                  <div className="font-medium">{ca.name}</div>
                  {ca.is_staging && <Badge variant="warning" className="mt-1">staging</Badge>}
                </Td>
                <Td className="max-w-[22rem] break-all font-mono text-[11px] text-muted-foreground">{ca.directory_url}</Td>
                <Td>
                  <Badge variant={statusVariant(ca.status)}>{t(`status.${ca.status}`)}</Badge>
                  {ca.last_error && <p className="mt-1 max-w-[16rem] truncate text-xs text-destructive" title={ca.last_error}>{ca.last_error}</p>}
                </Td>
                <Td className="text-muted-foreground">{ca.email ?? "—"}</Td>
                <Td className="text-muted-foreground">{formatDateTime(ca.created_at)}</Td>
                <Td className="text-right">
                  {isAdmin && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => register.mutate(ca.id)}
                      disabled={register.isPending}
                    >
                      <RefreshCw className="h-3.5 w-3.5" /> {t("ca.register")}
                    </Button>
                  )}
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <CreateCADialog open={open} onOpenChange={setOpen} />
    </div>
  );
}

function CreateCADialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { t } = useTranslation();
  const { data } = useCA();
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  const [directory, setDirectory] = React.useState("");
  const [staging, setStaging] = React.useState(false);
  const [email, setEmail] = React.useState("");
  const [eabKid, setEabKid] = React.useState("");
  const [eabHmac, setEabHmac] = React.useState("");
  const [tos, setTos] = React.useState(false);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const pickPreset = (id: string) => {
    const preset = data?.presets.find((p) => p.id === id);
    if (!preset) return;
    setDirectory(preset.directory_url);
    setStaging(preset.staging);
    if (!name) setName(preset.name);
  };

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      await request<CAAccount>("/ca-accounts", {
        method: "POST",
        body: { name, directory_url: directory, is_staging: staging, email, eab_kid: eabKid, eab_hmac_key: eabHmac, tos_accepted: tos },
      });
      toast.success(t("ca.created"));
      await qc.invalidateQueries({ queryKey: qk.ca });
      onOpenChange(false);
      setName("");
      setDirectory("");
      setEabKid("");
      setEabHmac("");
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
          <DialogTitle>{t("ca.add")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {error && <ErrorBox message={error} />}
          <Field label={t("ca.preset")}>
            <Select defaultValue="" onChange={(e) => pickPreset(e.target.value)}>
              <option value="">—</option>
              {(data?.presets ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("common.name")}>
            <Input value={name} onChange={(e) => setName(e.target.value)} required />
          </Field>
          <Field label={t("ca.directory")}>
            <Input value={directory} onChange={(e) => setDirectory(e.target.value)} placeholder="https://acme-v02.api.letsencrypt.org/directory" />
          </Field>
          <Field label={t("auth.email")} hint={t("ca.emailHint")}>
            <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <label className="flex items-center justify-between rounded-md border border-border p-3">
            <span className="text-sm">{t("ca.staging")}</span>
            <Switch checked={staging} onCheckedChange={setStaging} aria-label={t("ca.staging")} />
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("ca.eabKid")}>
              <Input value={eabKid} onChange={(e) => setEabKid(e.target.value)} autoComplete="off" />
            </Field>
            <Field label={t("ca.eabHmac")}>
              <Input value={eabHmac} onChange={(e) => setEabHmac(e.target.value)} type="password" autoComplete="new-password" />
            </Field>
          </div>
          <p className="text-xs text-muted-foreground">{t("ca.eabHint")}</p>
          <label className="flex items-center justify-between rounded-md border border-border p-3">
            <span className="text-sm">{t("ca.tosAccepted")}</span>
            <Switch checked={tos} onCheckedChange={setTos} aria-label={t("ca.tosAccepted")} />
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={submit} disabled={busy || !name || !directory || !tos}>{t("common.create")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

import * as React from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { KeyRound, Plus, Send, Trash2 } from "lucide-react";
import { useAuth } from "@/auth";
import { qk, useDeploymentCredentials } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import type { DeploymentCredential, DeploymentCredentialType } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState, ErrorBox, PageHeader, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";

const TYPES: DeploymentCredentialType[] = ["tencent", "aliyun", "qiniu", "baota", "1panel", "ssh"];

export function DeploymentCredentialsPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const qc = useQueryClient();
  const { data } = useDeploymentCredentials();
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<DeploymentCredential | null>(null);
  const isAdmin = user?.role === "admin";

  return (
    <div>
      <PageHeader
        title={t("cred.title")}
        subtitle={t("cred.subtitle")}
        actions={
          isAdmin && (
            <Button
              onClick={() => {
                setEditing(null);
                setOpen(true);
              }}
            >
              <Plus className="h-4 w-4" /> {t("cred.add")}
            </Button>
          )
        }
      />
      {!data?.items.length ? (
        <EmptyState description={t("cred.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("common.type")}</Th>
              <Th>{t("dns.fieldsConfigured")}</Th>
              <Th>{t("cred.updatedAt")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.items.map((c) => (
              <tr key={c.id}>
                <Td className="font-medium">
                  <span className="inline-flex items-center gap-2">
                    <KeyRound className="h-4 w-4 text-muted-foreground" />
                    {c.name}
                  </span>
                  {Object.keys(c.public_config).length > 0 && (
                    <div className="mt-1 text-xs text-muted-foreground">
                      {Object.entries(c.public_config)
                        .map(([k, v]) => `${k}=${v}`)
                        .join(" · ")}
                    </div>
                  )}
                </Td>
                <Td>{t(`cred.types.${c.type}`, { defaultValue: c.type })}</Td>
                <Td>
                  <div className="flex flex-wrap gap-1">
                    {c.configured_fields.map((f) => (
                      <Badge key={f} variant="outline" className="font-mono text-[10px]">
                        {f}
                      </Badge>
                    ))}
                  </div>
                </Td>
                <Td className="text-muted-foreground">{formatDateTime(c.updated_at)}</Td>
                <Td className="text-right">
                  {isAdmin && (
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={async () => {
                          try {
                            await request(`/deployment-credentials/${c.id}/test`, { method: "POST", body: {} });
                            toast.success(t("cred.testOk"));
                          } catch (err) {
                            toast.error(errorMessage(err));
                          }
                        }}
                      >
                        <Send className="h-3.5 w-3.5" /> {t("deploy.test")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setEditing(c);
                          setOpen(true);
                        }}
                      >
                        {t("common.edit")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={async () => {
                          if (!window.confirm(t("cred.deleteConfirm", { name: c.name }))) return;
                          try {
                            await request(`/deployment-credentials/${c.id}`, { method: "DELETE" });
                            toast.success(t("cred.deleted"));
                            void qc.invalidateQueries({ queryKey: qk.deploymentCredentials });
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
      <CredentialDialog open={open} onOpenChange={setOpen} editing={editing} />
    </div>
  );
}

function CredentialDialog({
  open,
  onOpenChange,
  editing,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  editing: DeploymentCredential | null;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  const [type, setType] = React.useState<DeploymentCredentialType>("tencent");
  const [cfg, setCfg] = React.useState<Record<string, string>>({});
  const [ignoreTLS, setIgnoreTLS] = React.useState(false);
  const [ignoreHostKey, setIgnoreHostKey] = React.useState(false);
  const [authMode, setAuthMode] = React.useState("key");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setName(editing?.name ?? "");
    setType(editing?.type ?? "tencent");
    const pub = editing?.public_config ?? {};
    const initial: Record<string, string> = {};
    for (const [k, v] of Object.entries(pub)) initial[k] = String(v ?? "");
    setCfg(initial);
    setIgnoreTLS(Boolean(pub.ignore_tls));
    setIgnoreHostKey(Boolean(pub.insecure_ignore_host_key));
    setAuthMode(editing ? (editing.configured_fields.includes("private_key") ? "key" : "password") : "key");
    setError("");
  }, [open, editing]);

  const buildConfig = (): Record<string, unknown> => {
    const config: Record<string, unknown> = {};
    switch (type) {
      case "tencent":
        config.secret_id = cfg.secret_id ?? "";
        config.secret_key = cfg.secret_key ?? "";
        break;
      case "aliyun":
        config.access_key_id = cfg.access_key_id ?? "";
        config.access_key_secret = cfg.access_key_secret ?? "";
        break;
      case "qiniu":
        config.access_key = cfg.access_key ?? "";
        config.access_secret = cfg.access_secret ?? "";
        break;
      case "baota":
        config.url = cfg.url ?? "";
        config.api_key = cfg.api_key ?? "";
        config.ignore_tls = ignoreTLS;
        break;
      case "1panel":
        config.url = cfg.url ?? "";
        config.api_key = cfg.api_key ?? "";
        config.version = cfg.version || "v1";
        config.ignore_tls = ignoreTLS;
        break;
      case "ssh":
        config.host = cfg.host ?? "";
        config.port = Number(cfg.port) || 22;
        config.user = cfg.user ?? "";
        config.host_key_sha256 = cfg.host_key_sha256 ?? "";
        config.insecure_ignore_host_key = ignoreHostKey;
        if (authMode === "key") {
          config.private_key = cfg.private_key ?? "";
          config.passphrase = cfg.passphrase ?? "";
        } else {
          config.password = cfg.password ?? "";
        }
        break;
    }
    return Object.fromEntries(
      Object.entries(config).filter(([, v]) => !(typeof v === "string" && v === "")),
    );
  };

  const test = async () => {
    setTesting(true);
    setError("");
    try {
      if (editing) {
        await request(`/deployment-credentials/${editing.id}/test`, { method: "POST", body: {} });
      } else {
        await request("/deployment-credentials/test", { method: "POST", body: { type, config: buildConfig() } });
      }
      toast.success(t("cred.testOk"));
    } catch (err) {
      const msg = errorMessage(err);
      setError(`${t("deploy.testFailed")}: ${msg}`);
      toast.error(msg);
    } finally {
      setTesting(false);
    }
  };

  const submit = async () => {
    setBusy(true);
    setError("");
    const config = buildConfig();
    try {
      if (editing) {
        await request(`/deployment-credentials/${editing.id}`, {
          method: "PATCH",
          body: { name, type, config },
        });
        toast.success(t("cred.updated"));
      } else {
        await request("/deployment-credentials", {
          method: "POST",
          body: { name, type, config },
        });
        toast.success(t("cred.created"));
      }
      await qc.invalidateQueries({ queryKey: qk.deploymentCredentials });
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setCfg((c) => ({ ...c, [k]: e.target.value }));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{editing ? t("cred.edit") : t("cred.add")}</DialogTitle>
          <DialogDescription>{t("cred.hint")}</DialogDescription>
        </DialogHeader>
        <div className="-m-1 max-h-[70vh] space-y-4 overflow-y-auto p-1">
          {error && <ErrorBox message={error} />}
          <Field label={t("common.name")}>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t("common.type")}>
            <Select
              value={type}
              onChange={(e) => {
                setType(e.target.value as DeploymentCredentialType);
                setCfg({});
                setIgnoreTLS(false);
                setIgnoreHostKey(false);
              }}
              disabled={Boolean(editing)}
            >
              {TYPES.map((value) => (
                <option key={value} value={value}>{t(`cred.types.${value}`)}</option>
              ))}
            </Select>
          </Field>

          {(type === "tencent" || type === "aliyun" || type === "qiniu") && (
            <>
              <Field label={type === "qiniu" ? t("deploy.accessKey") : t("deploy.secretId")}>
                <Input type="password" autoComplete="new-password" value={cfg.secret_id ?? cfg.access_key_id ?? cfg.access_key ?? ""} onChange={set(type === "tencent" ? "secret_id" : type === "aliyun" ? "access_key_id" : "access_key")} />
              </Field>
              <Field label={type === "qiniu" ? t("deploy.accessSecret") : t("deploy.secretKey")}>
                <Input type="password" autoComplete="new-password" value={cfg.secret_key ?? cfg.access_key_secret ?? cfg.access_secret ?? ""} onChange={set(type === "tencent" ? "secret_key" : type === "aliyun" ? "access_key_secret" : "access_secret")} />
              </Field>
            </>
          )}

          {(type === "baota" || type === "1panel") && (
            <>
              <Field label={t("deploy.panelUrl")} hint={t("deploy.panelUrlHint")}>
                <Input value={cfg.url ?? ""} onChange={set("url")} placeholder="https://panel.example.com:8888" />
              </Field>
              <Field label={t("deploy.apiKey")}>
                <Input type="password" autoComplete="new-password" value={cfg.api_key ?? ""} onChange={set("api_key")} />
              </Field>
              {type === "1panel" && (
                <Field label={t("deploy.version")}>
                  <Select value={cfg.version || "v1"} onChange={set("version")}>
                    <option value="v1">v1</option>
                    <option value="v2">v2</option>
                  </Select>
                </Field>
              )}
              <Toggle label={t("deploy.ignoreTls")} checked={ignoreTLS} onCheckedChange={setIgnoreTLS} />
            </>
          )}

          {type === "ssh" && (
            <>
              <div className="grid grid-cols-3 gap-3">
                <Field label={t("deploy.host")} className="col-span-2">
                  <Input value={cfg.host ?? ""} onChange={set("host")} placeholder="10.0.0.10" />
                </Field>
                <Field label={t("deploy.port")}>
                  <Input value={cfg.port ?? "22"} onChange={set("port")} placeholder="22" />
                </Field>
              </div>
              <Field label={t("deploy.user")}>
                <Input value={cfg.user ?? ""} onChange={set("user")} placeholder="root" />
              </Field>
              <Field label={t("deploy.authMode")}>
                <Select value={authMode} onChange={(e) => setAuthMode(e.target.value)}>
                  <option value="key">{t("deploy.privateKey")}</option>
                  <option value="password">{t("deploy.password")}</option>
                </Select>
              </Field>
              {authMode === "key" ? (
                <>
                  <Field label={t("deploy.privateKey")} hint={t("deploy.privateKeyHint")}>
                    <textarea
                      value={cfg.private_key ?? ""}
                      onChange={(e) => setCfg((c) => ({ ...c, private_key: e.target.value }))}
                      rows={4}
                      className="flex w-full rounded-md border border-border bg-background px-3 py-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    />
                  </Field>
                  <Field label={t("deploy.passphrase")} hint={t("common.optional")}>
                    <Input type="password" autoComplete="new-password" value={cfg.passphrase ?? ""} onChange={set("passphrase")} />
                  </Field>
                </>
              ) : (
                <Field label={t("deploy.password")}>
                  <Input type="password" autoComplete="new-password" value={cfg.password ?? ""} onChange={set("password")} />
                </Field>
              )}
              <Field label={t("deploy.hostKey")} hint={t("deploy.hostKeyHint")}>
                <Input value={cfg.host_key_sha256 ?? ""} onChange={set("host_key_sha256")} placeholder="SHA256:..." />
              </Field>
              <Toggle label={t("deploy.ignoreHostKey")} checked={ignoreHostKey} onCheckedChange={setIgnoreHostKey} />
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button variant="outline" onClick={test} disabled={busy || testing}>
            {testing ? t("deploy.testing") : t("deploy.test")}
          </Button>
          <Button onClick={submit} disabled={busy || testing || !name}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Toggle({
  label,
  checked,
  onCheckedChange,
}: {
  label: string;
  checked: boolean;
  onCheckedChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border border-border px-3 py-2">
      <span className="text-sm">{label}</span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  );
}

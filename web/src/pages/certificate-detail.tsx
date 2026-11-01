import * as React from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Download,
  FileDown,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
  ShieldOff,
  Trash2,
  Zap,
} from "lucide-react";
import { useAuth } from "@/auth";
import { download, errorMessage, request } from "@/api/client";
import { qk, useCertEvents, useCertificate, useDeployments, useRevisions } from "@/api/queries";
import type { CertInput, Deployment } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, Select } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { BackLink, EmptyState, ErrorBox, PageHeader, Spinner, Table, Tabs, Td, Th } from "@/components/ui/misc";
import { daysUntil, formatDate, formatDateTime, fromNow } from "@/lib/format";

export function CertificateDetailPage() {
  const { id = "" } = useParams();
  const { t, i18n } = useTranslation();
  const { user } = useAuth();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data, isLoading, error } = useCertificate(id);
  const { data: revisions } = useRevisions(id);
  const { data: events } = useCertEvents(id);
  const { data: deployments } = useDeployments(id);
  const [tab, setTab] = React.useState("overview");
  const [revokeOpen, setRevokeOpen] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [pfxOpen, setPfxOpen] = React.useState(false);
  const [deployOpen, setDeployOpen] = React.useState(false);
  const [busy, setBusy] = React.useState("");

  const cert = data?.certificate;
  const canEdit = user?.role === "admin" || user?.role === "operator";
  const revisionID = cert?.current_revision_id ?? "";

  React.useEffect(() => {
    if (!revisionID) return;
    void qc.invalidateQueries({ queryKey: qk.revisions(id) });
    void qc.invalidateQueries({ queryKey: qk.events(id) });
    void qc.invalidateQueries({ queryKey: qk.deployments(id) });
  }, [revisionID, id, qc]);

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: qk.cert(id) });
    void qc.invalidateQueries({ queryKey: qk.revisions(id) });
    void qc.invalidateQueries({ queryKey: qk.events(id) });
    void qc.invalidateQueries({ queryKey: qk.jobs("") });
  };

  const act = async (action: string, path: string, body: unknown, success: string) => {
    setBusy(action);
    try {
      await request(path, { method: "POST", body, headers: { "Idempotency-Key": `web-${action}-${id}-${Date.now()}` } });
      toast.success(success);
      invalidate();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
    }
  };

  const toggleRenew = async (enabled: boolean) => {
    if (!cert) return;
    const body: CertInput = {
      name: cert.name,
      primary_domain: cert.primary_domain,
      san: cert.san.slice(1),
      key_type: cert.key_type,
      ca_account_id: cert.ca_account_id,
      challenge_type: cert.challenge_type,
      dns_credential_id: cert.dns_credential_id ?? undefined,
      webroot: cert.webroot ?? undefined,
      options: cert.options,
      auto_renew: enabled,
      version: cert.version,
    };
    try {
      await request(`/certificates/${id}`, { method: "PATCH", body });
      toast.success(t("common.saved"));
      invalidate();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner className="h-6 w-6" />
      </div>
    );
  }
  if (error || !cert) {
    return <ErrorBox message={errorMessage(error) || t("errors.notFound")} />;
  }

  const days = daysUntil(cert.next_renew_at);

  return (
    <div>
      <div className="mb-2">
        <BackLink to="/certificates">{t("cert.title")}</BackLink>
      </div>
      <PageHeader
        title={cert.name}
        subtitle={cert.primary_domain}
        actions={
          canEdit && (
            <>
              <Button
                variant="outline"
                onClick={() => act("issue", `/certificates/${id}/issue`, { force: false }, t("cert.issued"))}
                disabled={Boolean(busy)}
              >
                {busy === "issue" ? <Loader2 className="h-4 w-4 animate-spin" /> : <Zap className="h-4 w-4" />}
                {cert.status === "draft" || cert.status === "failed" ? t("cert.issue") : t("cert.reissue")}
              </Button>
              <Button
                variant="outline"
                onClick={() => act("renew", `/certificates/${id}/renew`, { force: false }, t("cert.renewQueued"))}
                disabled={Boolean(busy) || !cert.current_revision_id}
              >
                {busy === "renew" ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                {t("cert.renew")}
              </Button>
              <Button
                variant="outline"
                onClick={() => setRevokeOpen(true)}
                disabled={Boolean(busy) || !cert.current_revision_id || cert.status === "revoked"}
              >
                <ShieldOff className="h-4 w-4" /> {t("cert.revoke")}
              </Button>
              {user?.role === "admin" && (
                <Button variant="ghost" size="icon" onClick={() => setDeleteOpen(true)} title={t("cert.delete")}>
                  <Trash2 className="h-4 w-4 text-destructive" />
                </Button>
              )}
            </>
          )
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Badge variant={statusVariant(cert.status)}>{t(`status.${cert.status}`)}</Badge>
        {cert.next_renew_at && (
          <Badge variant={days !== null && days <= 30 ? "warning" : "muted"}>
            {t("cert.nextRenew")}: {formatDate(cert.next_renew_at)}
          </Badge>
        )}
        {cert.san.map((name) => (
          <Badge key={name} variant="outline" className="font-mono text-[11px]">
            {name}
          </Badge>
        ))}
      </div>

      {cert.last_error_message && (
        <div className="mb-4">
          <ErrorBox message={`${cert.last_error_code ?? "E_UNKNOWN"}: ${cert.last_error_message}`} />
        </div>
      )}

      <Tabs
        value={tab}
        onValueChange={setTab}
        tabs={[
          { value: "overview", label: t("cert.overview") },
          { value: "revisions", label: `${t("cert.revisions")} (${revisions?.items.length ?? 0})` },
          { value: "timeline", label: t("cert.timeline") },
          { value: "deployments", label: t("cert.deployment") },
        ]}
      />

      {tab === "overview" && (
        <div className="grid gap-6 lg:grid-cols-3">
          <Card className="lg:col-span-2">
            <CardHeader>
              <CardTitle>{t("cert.details")}</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-4 text-sm sm:grid-cols-2">
              <Info label={t("cert.ca")} value={data?.ca_account?.name ?? "—"} />
              <Info label={t("cert.challenge")} value={cert.challenge_type} />
              <Info label={t("cert.keyType")} value={cert.key_type} />
              <Info label={t("cert.dnsCredential")} value={data?.dns_credential?.name ?? "—"} />
              <Info label={t("common.createdAt")} value={formatDateTime(cert.created_at)} />
              <Info label={t("common.updatedAt")} value={formatDateTime(cert.updated_at)} />
              <div className="flex items-center justify-between rounded-md border border-border p-3 sm:col-span-2">
                <span>{t("cert.autoRenew")}</span>
                <Switch checked={cert.auto_renew} disabled={!canEdit} onCheckedChange={toggleRenew} aria-label={t("cert.autoRenew")} />
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{data?.current_revision ? t("cert.currentRevision") : t("cert.noRevision")}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              {data?.current_revision ? (
                <>
                  <Info label={t("cert.expires")} value={formatDateTime(data.current_revision.not_after)} />
                  <Info label={t("cert.issuedAt")} value={formatDateTime(data.current_revision.issued_at)} />
                  <Info label={t("cert.serial")} value={data.current_revision.serial || "—"} mono />
                  <div>
                    <p className="text-muted-foreground">{t("cert.fingerprint")}</p>
                    <p className="break-all font-mono text-[11px]">{data.current_revision.fingerprint_sha256}</p>
                  </div>
                  <div className="flex flex-wrap gap-2 pt-2">
                    <Button size="sm" variant="outline" onClick={() => download(`/certificates/${id}/download?format=pem`)}>
                      <Download className="h-3.5 w-3.5" /> PEM
                    </Button>
                    {canEdit && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={async () => {
                          if (!window.confirm(t("cert.privateKeyConfirm"))) return;
                          try {
                            await download(`/certificates/${id}/download?format=zip`, undefined, {
                              headers: { "X-Confirm-Private-Key": "yes" },
                            });
                          } catch (err) {
                            toast.error(errorMessage(err));
                          }
                        }}
                      >
                        <FileDown className="h-3.5 w-3.5" /> ZIP
                      </Button>
                    )}
                    {canEdit && (
                      <Button size="sm" variant="outline" onClick={() => setPfxOpen(true)}>
                        PFX
                      </Button>
                    )}
                    {canEdit && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={async () => {
                          if (!window.confirm(t("cert.privateKeyConfirm"))) return;
                          try {
                            await download(`/certificates/${id}/download?format=pem&key=true`, undefined, {
                              headers: { "X-Confirm-Private-Key": "yes" },
                            });
                          } catch (err) {
                            toast.error(errorMessage(err));
                          }
                        }}
                      >
                        <KeyRound className="h-3.5 w-3.5" /> {t("cert.privateKey")}
                      </Button>
                    )}
                  </div>
                </>
              ) : (
                <p className="text-muted-foreground">{t("cert.noRevision")}</p>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {tab === "revisions" && (
        <Table>
          <thead>
            <tr>
              <Th>{t("cert.serial")}</Th>
              <Th>{t("cert.issuedAt")}</Th>
              <Th>{t("cert.expires")}</Th>
              <Th>{t("cert.fingerprint")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {(revisions?.items ?? []).map((rev) => (
              <tr key={rev.id}>
                <Td className="font-mono text-xs">{rev.serial}</Td>
                <Td>{formatDateTime(rev.issued_at)}</Td>
                <Td>{formatDateTime(rev.not_after)}</Td>
                <Td className="max-w-[18rem] truncate font-mono text-[11px]" title={rev.fingerprint_sha256}>
                  {rev.fingerprint_sha256}
                </Td>
                <Td className="text-right">
                  {canEdit && (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={async () => {
                        if (!window.confirm(t("cert.privateKeyConfirm"))) return;
                        try {
                          await download(`/certificates/${id}/download?format=zip&revision=${rev.id}`, undefined, {
                            headers: { "X-Confirm-Private-Key": "yes" },
                          });
                        } catch (err) {
                          toast.error(errorMessage(err));
                        }
                      }}
                    >
                      ZIP
                    </Button>
                  )}
                </Td>
              </tr>
            ))}
            {!revisions?.items.length && (
              <tr>
                <Td className="py-8 text-center text-muted-foreground" />
                <Td /> <Td /> <Td /> <Td />
              </tr>
            )}
          </tbody>
        </Table>
      )}

      {tab === "timeline" && (
        <Card>
          <CardContent className="p-5">
            {!events?.items.length ? (
              <EmptyState />
            ) : (
              <ol className="relative space-y-4 border-l border-border pl-5">
                {events.items.map((ev) => (
                  <li key={ev.id} className="relative">
                    <span className="absolute -left-[1.4rem] top-1.5 h-2 w-2 rounded-full bg-primary" />
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant="muted">{ev.type}</Badge>
                      <span className="text-xs text-muted-foreground" title={formatDateTime(ev.created_at)}>
                        {fromNow(ev.created_at, i18n.language)}
                      </span>
                      {ev.job_id && (
                        <Link to={`/jobs/${ev.job_id}`} className="text-xs text-primary hover:underline">
                          {t("common.details")}
                        </Link>
                      )}
                    </div>
                    {ev.message && <p className="mt-1 text-sm">{ev.message}</p>}
                  </li>
                ))}
              </ol>
            )}
          </CardContent>
        </Card>
      )}

      {tab === "deployments" && (
        <div className="space-y-3">
          {canEdit && (
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDeployOpen(true)}>
                <Plus className="h-4 w-4" /> {t("deploy.add")}
              </Button>
              <Button onClick={() => act("deploy", `/certificates/${id}/deploy`, {}, t("deploy.queued"))} disabled={Boolean(busy) || !cert.current_revision_id}>
                <Zap className="h-4 w-4" /> {t("deploy.run")}
              </Button>
            </div>
          )}
          {!deployments?.items.length ? (
            <EmptyState description={t("deploy.empty")} />
          ) : (
            <Table>
              <thead>
                <tr>
                  <Th>{t("common.name")}</Th>
                  <Th>{t("common.type")}</Th>
                  <Th>{t("common.status")}</Th>
                  <Th>{t("deploy.lastRun")}</Th>
                  <Th />
                </tr>
              </thead>
              <tbody>
                {deployments.items.map((d) => (
                  <tr key={d.id}>
                    <Td className="font-medium">{d.name}</Td>
                    <Td>{d.type}</Td>
                    <Td>
                      <Badge variant={d.enabled ? statusVariant(d.last_status ?? "ok") : "muted"}>
                        {d.enabled ? (d.last_status ?? "—") : t("common.disabled")}
                      </Badge>
                    </Td>
                    <Td className="text-muted-foreground">{d.last_run_at ? formatDateTime(d.last_run_at) : t("common.never")}</Td>
                    <Td className="text-right">
                      {canEdit && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={async () => {
                            if (!window.confirm(`${t("common.delete")} ${d.name}?`)) return;
                            try {
                              await request(`/deployments/${d.id}`, { method: "DELETE" });
                              toast.success(t("deploy.deleted"));
                              invalidate();
                            } catch (err) {
                              toast.error(errorMessage(err));
                            }
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </div>
      )}

      <RevokeDialog open={revokeOpen} onOpenChange={setRevokeOpen} domain={cert.primary_domain} onSubmit={(reason) => {
        setRevokeOpen(false);
        void act("revoke", `/certificates/${id}/revoke`, { reason }, t("cert.revoked"));
      }} />

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("cert.delete")}</DialogTitle>
            <DialogDescription>{t("cert.deleteConfirm", { domain: cert.primary_domain })}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)}>{t("common.cancel")}</Button>
            <Button
              variant="destructive"
              onClick={async () => {
                try {
                  await request(`/certificates/${id}`, { method: "DELETE" });
                  toast.success(t("cert.deleted"));
                  navigate("/certificates");
                } catch (err) {
                  toast.error(errorMessage(err));
                }
              }}
            >
              {t("common.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <PfxDialog open={pfxOpen} onOpenChange={setPfxOpen} onSubmit={async (password) => {
        try {
          await download(`/certificates/${id}/download?format=pfx`, { password }, {
            method: "POST",
            headers: { "X-Confirm-Private-Key": "yes" },
          });
          setPfxOpen(false);
        } catch (err) {
          toast.error(errorMessage(err));
        }
      }} />

      <DeploymentDialog open={deployOpen} onOpenChange={setDeployOpen} certId={id} onCreated={() => {
        setDeployOpen(false);
        void qc.invalidateQueries({ queryKey: qk.deployments(id) });
      }} />
    </div>
  );
}

function Info({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div>
      <p className="text-muted-foreground">{label}</p>
      <p className={mono ? "break-all font-mono text-xs" : "font-medium"}>{value}</p>
    </div>
  );
}

function RevokeDialog({
  open,
  onOpenChange,
  domain,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  domain: string;
  onSubmit: (reason: number) => void;
}) {
  const { t } = useTranslation();
  const [reason, setReason] = React.useState(1);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("cert.revoke")}</DialogTitle>
          <DialogDescription>{t("cert.revokeConfirm", { domain })}</DialogDescription>
        </DialogHeader>
        <Field label={t("cert.revokeReason")}>
          <Select value={reason} onChange={(e) => setReason(Number(e.target.value))}>
            <option value={0}>unspecified (0)</option>
            <option value={1}>keyCompromise (1)</option>
            <option value={4}>superseded (4)</option>
            <option value={5}>cessationOfOperation (5)</option>
          </Select>
        </Field>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button variant="destructive" onClick={() => onSubmit(reason)}>{t("cert.revoke")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PfxDialog({ open, onOpenChange, onSubmit }: { open: boolean; onOpenChange: (v: boolean) => void; onSubmit: (pw: string) => void }) {
  const { t } = useTranslation();
  const [password, setPassword] = React.useState("");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("cert.downloadPFX")}</DialogTitle>
          <DialogDescription>{t("cert.pfxPassword")}</DialogDescription>
        </DialogHeader>
        <Field label={t("auth.password")}>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} minLength={8} />
        </Field>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button disabled={password.length < 8} onClick={() => onSubmit(password)}>{t("common.download")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeploymentDialog({
  open,
  onOpenChange,
  certId,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  certId: string;
  onCreated: () => void;
}) {
  const { t } = useTranslation();
  const [type, setType] = React.useState<Deployment["type"]>("local_file");
  const [name, setName] = React.useState("");
  const [dir, setDir] = React.useState("");
  const [target, setTarget] = React.useState("nginx");
  const [url, setUrl] = React.useState("");
  const [secret, setSecret] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const submit = async () => {
    let config: Record<string, unknown>;
    if (type === "local_file") {
      config = {
        directory: dir,
        cert_file: "cert.pem",
        key_file: "privkey.pem",
        chain_file: "chain.pem",
        fullchain_file: "fullchain.pem",
        mode: "0640",
        backup: true,
      };
    } else if (type === "reload") {
      config = { target };
    } else {
      config = { url, secret };
    }
    setBusy(true);
    setError("");
    try {
      await request("/deployments", {
        method: "POST",
        body: { certificate_id: certId, name: name || type, type, config, enabled: true },
      });
      toast.success(t("deploy.created"));
      onCreated();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("deploy.add")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {error && <ErrorBox message={error} />}
          <Field label={t("common.name")}>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="nginx-prod" />
          </Field>
          <Field label={t("common.type")}>
            <Select value={type} onChange={(e) => setType(e.target.value as Deployment["type"])}>
              <option value="local_file">{t("deploy.local")}</option>
              <option value="reload">{t("deploy.reload")}</option>
              <option value="webhook">{t("deploy.webhook")}</option>
            </Select>
          </Field>
          {type === "local_file" && (
            <Field label={t("deploy.directory")}>
              <Input value={dir} onChange={(e) => setDir(e.target.value)} placeholder="/etc/nginx/certs" />
            </Field>
          )}
          {type === "reload" && (
            <Field label={t("deploy.target")}>
              <Select value={target} onChange={(e) => setTarget(e.target.value)}>
                <option value="nginx">nginx -s reload</option>
                <option value="systemd-nginx">systemctl reload nginx</option>
                <option value="systemd-apache">systemctl reload apache2</option>
                <option value="apache">apachectl graceful</option>
              </Select>
            </Field>
          )}
          {type === "webhook" && (
            <>
              <Field label={t("deploy.url")}>
                <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com/hook" />
              </Field>
              <Field label={t("deploy.secret")} hint={t("common.optional")}>
                <Input value={secret} onChange={(e) => setSecret(e.target.value)} />
              </Field>
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={submit} disabled={busy}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

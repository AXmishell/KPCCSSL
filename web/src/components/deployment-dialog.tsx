import * as React from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { errorMessage, request } from "@/api/client";
import { useDeploymentCredentials } from "@/api/queries";
import type { DeploymentCredentialType, DeploymentType } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/input";
import { ErrorBox } from "@/components/ui/misc";
import { Switch } from "@/components/ui/switch";

const TYPES: DeploymentType[] = [
  "local_file",
  "ssh",
  "baota_panel",
  "baota_site",
  "1panel_panel",
  "1panel_site",
  "tencent_cdn",
  "aliyun_cdn",
  "tencent_waf",
  "aliyun_waf",
  "tencent_cos",
  "tencent_teo",
  "aliyun_oss",
  "aliyun_dcdn",
  "qiniu_cdn",
  "reload",
  "webhook",
];

const RELOAD_TARGETS = ["none", "nginx", "systemd-nginx", "systemd-apache", "apache"];

function credentialTypeFor(type: DeploymentType): DeploymentCredentialType | null {
  if (type.startsWith("tencent_")) return "tencent";
  if (type.startsWith("aliyun_")) return "aliyun";
  if (type === "qiniu_cdn") return "qiniu";
  if (type.startsWith("baota_")) return "baota";
  if (type.startsWith("1panel_")) return "1panel";
  if (type === "ssh") return "ssh";
  return null;
}

export function DeploymentDialog({
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
  const { data: credentials } = useDeploymentCredentials();
  const [type, setType] = React.useState<DeploymentType>("local_file");
  const [name, setName] = React.useState("");
  const [credentialId, setCredentialId] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);

  // Local files.
  const [dir, setDir] = React.useState("");
  const [certFile, setCertFile] = React.useState("cert.pem");
  const [keyFile, setKeyFile] = React.useState("privkey.pem");
  const [chainFile, setChainFile] = React.useState("chain.pem");
  const [fullchainFile, setFullchainFile] = React.useState("fullchain.pem");
  const [mode, setMode] = React.useState("0640");
  const [backup, setBackup] = React.useState(true);

  // Reload / webhook.
  const [reloadTarget, setReloadTarget] = React.useState("nginx");
  const [url, setUrl] = React.useState("");
  const [secret, setSecret] = React.useState("");

  // Panels.
  const [siteNames, setSiteNames] = React.useState("");
  const [siteId, setSiteId] = React.useState("");

  // Cloud.
  const [region, setRegion] = React.useState("");
  const [domain, setDomain] = React.useState("");
  const [bucket, setBucket] = React.useState("");

  const credentialType = credentialTypeFor(type);
  const availableCredentials = React.useMemo(
    () => (credentials?.items ?? []).filter((c) => c.type === credentialType),
    [credentials, credentialType],
  );

  React.useEffect(() => {
    if (!credentialType) {
      setCredentialId("");
      return;
    }
    setCredentialId((prev) =>
      availableCredentials.some((c) => c.id === prev) ? prev : (availableCredentials[0]?.id ?? ""),
    );
  }, [credentialType, availableCredentials]);

  const isSSH = type === "ssh";
  const isBaota = type === "baota_panel" || type === "baota_site";
  const isOnePanel = type === "1panel_panel" || type === "1panel_site";
  const isTencent = type.startsWith("tencent_");
  const isAliyun = type.startsWith("aliyun_");
  const isQiniu = type === "qiniu_cdn";
  const needsBucket = type === "tencent_cos" || type === "aliyun_oss";
  const fileLike = type === "local_file" || isSSH;

  const buildConfig = (): Record<string, unknown> | null => {
    if (type === "local_file") {
      return {
        directory: dir,
        cert_file: certFile,
        key_file: keyFile,
        chain_file: chainFile,
        fullchain_file: fullchainFile,
        mode,
        backup,
      };
    }
    if (type === "reload") {
      return { target: reloadTarget };
    }
    if (type === "webhook") {
      return { url, secret };
    }
    if (!credentialType) return null;

    if (isSSH) {
      return {
        credential_id: credentialId,
        directory: dir,
        cert_file: certFile,
        key_file: keyFile,
        chain_file: chainFile,
        fullchain_file: fullchainFile,
        mode,
        reload: reloadTarget,
      };
    }
    if (isBaota) {
      return {
        credential_id: credentialId,
        site_names: type === "baota_site"
          ? siteNames.split(",").map((s) => s.trim()).filter(Boolean)
          : [],
      };
    }
    if (isOnePanel) {
      return {
        credential_id: credentialId,
        site_id: type === "1panel_site" ? siteId : "",
      };
    }
    if (isQiniu) {
      return { credential_id: credentialId, domain };
    }
    return { credential_id: credentialId, region, domain, bucket: needsBucket ? bucket : "" };
  };

  const missingCredential = Boolean(credentialType) && !credentialId;

  const test = async () => {
    const config = buildConfig();
    if (!config) {
      setError(t("deploy.configInvalid"));
      return;
    }
    setTesting(true);
    setError("");
    try {
      await request("/deployments/test", { method: "POST", body: { type, config } });
      toast.success(t("deploy.testOk"));
    } catch (err) {
      const msg = errorMessage(err);
      setError(`${t("deploy.testFailed")}: ${msg}`);
      toast.error(msg);
    } finally {
      setTesting(false);
    }
  };

  const submit = async () => {
    const config = buildConfig();
    if (!config) {
      setError(t("deploy.configInvalid"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await request("/deployments", {
        method: "POST",
        body: {
          certificate_id: certId,
          name: name || t(`deploy.types.${type}`),
          type,
          config,
          enabled: true,
        },
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
        <div className="max-h-[70vh] space-y-4 overflow-y-auto pr-1">
          {error && <ErrorBox message={error} />}
          <Field label={t("common.name")}>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t(`deploy.types.${type}`)}
            />
          </Field>
          <Field label={t("common.type")}>
            <Select value={type} onChange={(e) => setType(e.target.value as DeploymentType)}>
              {TYPES.map((value) => (
                <option key={value} value={value}>{t(`deploy.types.${value}`)}</option>
              ))}
            </Select>
          </Field>

          {credentialType && (
            <>
              <Field
                label={t("cred.select")}
                hint={availableCredentials.length ? t("cred.selectHint") : t("cred.noneHint")}
              >
                <Select
                  value={credentialId}
                  onChange={(e) => setCredentialId(e.target.value)}
                  disabled={!availableCredentials.length}
                >
                  {!availableCredentials.length && <option value="">{t("cred.none")}</option>}
                  {availableCredentials.map((c) => (
                    <option key={c.id} value={c.id}>{c.name}</option>
                  ))}
                </Select>
              </Field>
              <p className="-mt-2 text-xs text-muted-foreground">
                <Link to="/deployment-credentials" className="text-primary hover:underline">
                  {t("cred.manage")}
                </Link>
              </p>
            </>
          )}

          {fileLike && (
            <>
              <Field label={t("deploy.directory")}>
                <Input value={dir} onChange={(e) => setDir(e.target.value)} placeholder="/etc/nginx/certs" />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label={t("deploy.certFile")}>
                  <Input value={certFile} onChange={(e) => setCertFile(e.target.value)} />
                </Field>
                <Field label={t("deploy.keyFile")}>
                  <Input value={keyFile} onChange={(e) => setKeyFile(e.target.value)} />
                </Field>
                <Field label={t("deploy.chainFile")}>
                  <Input value={chainFile} onChange={(e) => setChainFile(e.target.value)} />
                </Field>
                <Field label={t("deploy.fullchainFile")}>
                  <Input value={fullchainFile} onChange={(e) => setFullchainFile(e.target.value)} />
                </Field>
              </div>
              <Field label={t("deploy.mode")}>
                <Input value={mode} onChange={(e) => setMode(e.target.value)} placeholder="0640" />
              </Field>
            </>
          )}

          {type === "local_file" && (
            <Toggle label={t("deploy.backup")} checked={backup} onCheckedChange={setBackup} />
          )}

          {isSSH && (
            <Field label={t("deploy.target")}>
              <Select value={reloadTarget} onChange={(e) => setReloadTarget(e.target.value)}>
                {RELOAD_TARGETS.map((value) => (
                  <option key={value} value={value}>{value === "none" ? "—" : value}</option>
                ))}
              </Select>
            </Field>
          )}

          {type === "reload" && (
            <Field label={t("deploy.target")}>
              <Select value={reloadTarget} onChange={(e) => setReloadTarget(e.target.value)}>
                {RELOAD_TARGETS.filter((v) => v !== "none").map((value) => (
                  <option key={value} value={value}>{value}</option>
                ))}
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

          {(isBaota || isOnePanel) && (
            <>
              {type === "baota_site" && (
                <Field label={t("deploy.siteNames")} hint={t("deploy.siteNamesHint")}>
                  <Input value={siteNames} onChange={(e) => setSiteNames(e.target.value)} placeholder="example.com,www.example.com" />
                </Field>
              )}
              {type === "1panel_site" && (
                <Field label={t("deploy.siteId")} hint={t("deploy.siteIdHint")}>
                  <Input value={siteId} onChange={(e) => setSiteId(e.target.value)} placeholder="1" />
                </Field>
              )}
            </>
          )}

          {(isTencent || isAliyun || isQiniu) && (
            <>
              {!isQiniu && (
                <Field label={t("deploy.region")}>
                  <Input
                    value={region}
                    onChange={(e) => setRegion(e.target.value)}
                    placeholder={isTencent ? "ap-beijing" : "cn-hangzhou"}
                  />
                </Field>
              )}
              {needsBucket && (
                <Field label={t("deploy.bucket")} hint={t("deploy.bucketHint")}>
                  <Input value={bucket} onChange={(e) => setBucket(e.target.value)} placeholder="my-bucket" />
                </Field>
              )}
              <Field
                label={needsBucket ? t("deploy.domainOss") : t("deploy.domain")}
                hint={needsBucket ? t("deploy.domainOssHint") : t("deploy.domainHint")}
              >
                <Input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="cdn.example.com" />
              </Field>
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button variant="outline" onClick={test} disabled={busy || testing || missingCredential}>
            {testing ? t("deploy.testing") : t("deploy.test")}
          </Button>
          <Button onClick={submit} disabled={busy || testing || missingCredential}>{t("common.save")}</Button>
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

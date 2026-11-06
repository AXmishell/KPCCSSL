import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { errorMessage, request } from "@/api/client";
import type { DeploymentType } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
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
  "reload",
  "webhook",
];

const RELOAD_TARGETS = ["none", "nginx", "systemd-nginx", "systemd-apache", "apache"];

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
  const [type, setType] = React.useState<DeploymentType>("local_file");
  const [name, setName] = React.useState("");
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

  // SSH.
  const [host, setHost] = React.useState("");
  const [port, setPort] = React.useState("22");
  const [user, setUser] = React.useState("root");
  const [authMode, setAuthMode] = React.useState("key");
  const [password, setPassword] = React.useState("");
  const [privateKey, setPrivateKey] = React.useState("");
  const [passphrase, setPassphrase] = React.useState("");
  const [hostKey, setHostKey] = React.useState("");
  const [ignoreHostKey, setIgnoreHostKey] = React.useState(false);

  // Panels.
  const [panelUrl, setPanelUrl] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [ignoreTls, setIgnoreTls] = React.useState(false);
  const [panelVersion, setPanelVersion] = React.useState("v1");
  const [siteNames, setSiteNames] = React.useState("");
  const [siteId, setSiteId] = React.useState("");

  // Cloud.
  const [secretId, setSecretId] = React.useState("");
  const [cloudSecret, setCloudSecret] = React.useState("");
  const [region, setRegion] = React.useState("");
  const [domain, setDomain] = React.useState("");

  const isSSH = type === "ssh";
  const isBaota = type === "baota_panel" || type === "baota_site";
  const isOnePanel = type === "1panel_panel" || type === "1panel_site";
  const isTencent = type === "tencent_cdn" || type === "tencent_waf";
  const isAliyun = type === "aliyun_cdn" || type === "aliyun_waf";
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
    if (type === "ssh") {
      return {
        host,
        port: Number(port) || 22,
        user,
        password: authMode === "password" ? password : "",
        private_key: authMode === "key" ? privateKey : "",
        passphrase: authMode === "key" ? passphrase : "",
        host_key_sha256: hostKey,
        insecure_ignore_host_key: ignoreHostKey,
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
        url: panelUrl,
        api_key: apiKey,
        ignore_tls: ignoreTls,
        site_names: type === "baota_site"
          ? siteNames.split(",").map((s) => s.trim()).filter(Boolean)
          : [],
      };
    }
    if (isOnePanel) {
      return {
        url: panelUrl,
        api_key: apiKey,
        version: panelVersion,
        ignore_tls: ignoreTls,
        site_id: type === "1panel_site" ? siteId : "",
      };
    }
    if (isTencent) {
      return { secret_id: secretId, secret_key: cloudSecret, region, domain };
    }
    if (isAliyun) {
      return { access_key_id: secretId, access_key_secret: cloudSecret, region, domain };
    }
    return null;
  };

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
            <>
              <div className="grid grid-cols-3 gap-3">
                <Field label={t("deploy.host")} className="col-span-2">
                  <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="10.0.0.10" />
                </Field>
                <Field label={t("deploy.port")}>
                  <Input value={port} onChange={(e) => setPort(e.target.value)} placeholder="22" />
                </Field>
              </div>
              <Field label={t("deploy.user")}>
                <Input value={user} onChange={(e) => setUser(e.target.value)} placeholder="root" />
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
                    <Textarea
                      value={privateKey}
                      onChange={(e) => setPrivateKey(e.target.value)}
                      rows={4}
                      className="font-mono text-xs"
                    />
                  </Field>
                  <Field label={t("deploy.passphrase")} hint={t("common.optional")}>
                    <Input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
                  </Field>
                </>
              ) : (
                <Field label={t("deploy.password")}>
                  <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
                </Field>
              )}
              <Field label={t("deploy.hostKey")} hint={t("deploy.hostKeyHint")}>
                <Input value={hostKey} onChange={(e) => setHostKey(e.target.value)} placeholder="SHA256:..." />
              </Field>
              <Toggle
                label={t("deploy.ignoreHostKey")}
                checked={ignoreHostKey}
                onCheckedChange={setIgnoreHostKey}
              />
            </>
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
              <Field label={t("deploy.panelUrl")} hint={t("deploy.panelUrlHint")}>
                <Input value={panelUrl} onChange={(e) => setPanelUrl(e.target.value)} placeholder="https://panel.example.com:8888" />
              </Field>
              <Field label={t("deploy.apiKey")}>
                <Input type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
              </Field>
              {isOnePanel && (
                <Field label={t("deploy.version")}>
                  <Select value={panelVersion} onChange={(e) => setPanelVersion(e.target.value)}>
                    <option value="v1">v1</option>
                    <option value="v2">v2</option>
                  </Select>
                </Field>
              )}
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
              <Toggle label={t("deploy.ignoreTls")} checked={ignoreTls} onCheckedChange={setIgnoreTls} />
            </>
          )}

          {(isTencent || isAliyun) && (
            <>
              <Field label={t("deploy.secretId")}>
                <Input value={secretId} onChange={(e) => setSecretId(e.target.value)} />
              </Field>
              <Field label={t("deploy.secretKey")}>
                <Input type="password" value={cloudSecret} onChange={(e) => setCloudSecret(e.target.value)} />
              </Field>
              <Field label={t("deploy.region")}>
                <Input
                  value={region}
                  onChange={(e) => setRegion(e.target.value)}
                  placeholder={isTencent ? "ap-beijing" : "cn-hangzhou"}
                />
              </Field>
              <Field label={t("deploy.domain")} hint={t("deploy.domainHint")}>
                <Input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="cdn.example.com" />
              </Field>
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button variant="outline" onClick={test} disabled={busy || testing}>
            {testing ? t("deploy.testing") : t("deploy.test")}
          </Button>
          <Button onClick={submit} disabled={busy || testing}>{t("common.save")}</Button>
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

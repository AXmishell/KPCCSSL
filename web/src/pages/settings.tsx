import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { useCA, useInfo, useSettings, useUsers } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/input";
import { PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { formatBytes } from "@/lib/format";
import { readTheme, useTheme, type Theme } from "@/lib/theme";

export function SettingsPage() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const { setTheme } = useTheme();
  const { data: settings } = useSettings();
  const { data: info } = useInfo();
  const { data: users } = useUsers();
  const { data: caData } = useCA();
  const [form, setForm] = React.useState<Record<string, string>>({});
  const [busy, setBusy] = React.useState(false);
  const [userOpen, setUserOpen] = React.useState(false);
  const [userBusy, setUserBusy] = React.useState(false);
  const [newUser, setNewUser] = React.useState({ email: "", password: "", role: "operator" });

  React.useEffect(() => {
    if (!settings) return;
    setForm({
      instance_name: String(settings.instance_name ?? "KPCCSSL"),
      default_ca_id: String(settings.default_ca_id ?? ""),
      renewal_notice_days: String(settings.renewal_notice_days ?? 30),
      retention_days: String(settings.retention_days ?? 30),
      theme: String(settings.theme ?? readTheme()),
    });
  }, [settings]);

  const save = async () => {
    setBusy(true);
    try {
      await request("/settings", {
        method: "PATCH",
        body: {
          instance_name: form.instance_name,
          default_ca_id: form.default_ca_id || null,
          renewal_notice_days: Number(form.renewal_notice_days || 30),
          retention_days: Number(form.retention_days || 30),
          theme: form.theme,
        },
      });
      setTheme(form.theme as Theme);
      toast.success(t("settings.saved"));
      void qc.invalidateQueries();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const createUser = async () => {
    setUserBusy(true);
    try {
      await request("/users", { method: "POST", body: newUser });
      toast.success(t("settings.userCreated"));
      setUserOpen(false);
      setNewUser({ email: "", password: "", role: "operator" });
      void qc.invalidateQueries({ queryKey: ["users"] });
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setUserBusy(false);
    }
  };

  const patchUser = async (id: string, body: Record<string, unknown>) => {
    try {
      await request(`/users/${id}`, { method: "PATCH", body });
      toast.success(t("settings.userUpdated"));
      void qc.invalidateQueries({ queryKey: ["users"] });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  if (!settings || !info) {
    return (
      <div className="flex justify-center py-20">
        <Spinner className="h-6 w-6" />
      </div>
    );
  }

  return (
    <div>
      <PageHeader title={t("settings.title")} subtitle={t("settings.subtitle")} />
      <div className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>{t("settings.title")}</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Field label={t("settings.instanceName")}>
              <Input value={form.instance_name} onChange={(e) => setForm((f) => ({ ...f, instance_name: e.target.value }))} />
            </Field>
            <Field label={t("settings.defaultCA")}>
              <Select value={form.default_ca_id} onChange={(e) => setForm((f) => ({ ...f, default_ca_id: e.target.value }))}>
                <option value="">—</option>
                {(caData?.items ?? []).map((ca) => (
                  <option key={ca.id} value={ca.id}>
                    {ca.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("settings.renewalNoticeDays")}>
              <Input
                type="number"
                min={1}
                max={90}
                value={form.renewal_notice_days}
                onChange={(e) => setForm((f) => ({ ...f, renewal_notice_days: e.target.value }))}
              />
            </Field>
            <Field label={t("settings.retentionDays")}>
              <Input
                type="number"
                min={1}
                max={3650}
                value={form.retention_days}
                onChange={(e) => setForm((f) => ({ ...f, retention_days: e.target.value }))}
              />
            </Field>
            <Field label={t("settings.theme")}>
              <Select
                value={form.theme}
                onChange={(e) => {
                  const next = e.target.value as Theme;
                  setForm((f) => ({ ...f, theme: next }));
                  setTheme(next);
                }}
              >
                <option value="light">{t("common.light")}</option>
                <option value="dark">{t("common.dark")}</option>
              </Select>
            </Field>
            <div className="flex items-end">
              <Button onClick={save} disabled={busy}>
                {t("common.save")}
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t("settings.systemInfo")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Info label={t("settings.appVersion")} value={info.version} />
            <Info label={t("settings.acmeVersion")} value={info.acme_version} />
            <Info label={t("settings.dataDir")} value={info.data_dir} mono />
            <Info label={t("settings.diskFree")} value={formatBytes(info.disk_free_bytes)} />
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{t("settings.fakeAcme")}</span>
              <Badge variant={info.fake_acme ? "warning" : "success"}>{info.fake_acme ? "ON" : "OFF"}</Badge>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-6">
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>{t("settings.users")}</CardTitle>
          <Button size="sm" onClick={() => setUserOpen(true)}>{t("settings.addUser")}</Button>
        </CardHeader>
        <CardContent>
          <Table>
            <thead>
              <tr>
                <Th>{t("auth.email")}</Th>
                <Th>{t("profile.role")}</Th>
                <Th>{t("profile.totp")}</Th>
                <Th>{t("common.status")}</Th>
                <Th>{t("common.actions")}</Th>
              </tr>
            </thead>
            <tbody>
              {(users?.items ?? []).map((u) => (
                <tr key={u.id}>
                  <Td className="font-medium">{u.email}</Td>
                  <Td>
                    <Badge variant="muted">{u.role}</Badge>
                  </Td>
                  <Td>{u.totp_enabled ? t("common.yes") : t("common.no")}</Td>
                  <Td>
                    <Badge variant={u.disabled ? "destructive" : "success"}>
                      {u.disabled ? t("common.disabled") : t("common.enabled")}
                    </Badge>
                  </Td>
                  <Td>
                    <div className="flex items-center gap-2">
                      <Select
                        className="h-7 w-28"
                        value={u.role}
                        onChange={(e) => void patchUser(u.id, { role: e.target.value })}
                      >
                        <option value="viewer">viewer</option>
                        <option value="operator">operator</option>
                        <option value="admin">admin</option>
                      </Select>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => void patchUser(u.id, { disabled: !u.disabled })}
                      >
                        {u.disabled ? t("common.enabled") : t("common.disabled")}
                      </Button>
                    </div>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </CardContent>
      </Card>

      <Dialog open={userOpen} onOpenChange={setUserOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("settings.addUser")}</DialogTitle>
          </DialogHeader>
          <Field label={t("auth.email")}>
            <Input value={newUser.email} onChange={(e) => setNewUser((u) => ({ ...u, email: e.target.value }))} />
          </Field>
          <Field label={t("auth.password")}>
            <Input type="password" value={newUser.password} onChange={(e) => setNewUser((u) => ({ ...u, password: e.target.value }))} />
          </Field>
          <Field label={t("profile.role")}>
            <Select value={newUser.role} onChange={(e) => setNewUser((u) => ({ ...u, role: e.target.value }))}>
              <option value="viewer">viewer</option>
              <option value="operator">operator</option>
              <option value="admin">admin</option>
            </Select>
          </Field>
          <DialogFooter>
            <Button variant="outline" onClick={() => setUserOpen(false)}>{t("common.cancel")}</Button>
            <Button disabled={userBusy || !newUser.email || !newUser.password} onClick={() => void createUser()}>
              {t("common.create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Info({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <p className="text-muted-foreground">{label}</p>
      <p className={mono ? "break-all font-mono text-xs" : "font-medium"}>{value}</p>
    </div>
  );
}

import * as React from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Plus, Send, Trash2 } from "lucide-react";
import { useAuth } from "@/auth";
import { qk, useNotifications, useSettings } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import type { NotificationChannel } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState, ErrorBox, PageHeader, Table, Td, Th } from "@/components/ui/misc";

export function NotificationsPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const qc = useQueryClient();
  const { data } = useNotifications();
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<NotificationChannel | null>(null);
  const isAdmin = user?.role === "admin";
  const { data: settings } = useSettings(isAdmin);
  const systemMailConfigured = settings?.mail_configured === true;

  const test = async (id: string) => {
    try {
      await request(`/notification-channels/${id}/test`, { method: "POST", body: {} });
      toast.success(t("notify.testSent"));
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  return (
    <div>
      <PageHeader
        title={t("notify.title")}
        subtitle={t("notify.subtitle")}
        actions={
          isAdmin && (
            <Button
              onClick={() => {
                setEditing(null);
                setOpen(true);
              }}
            >
              <Plus className="h-4 w-4" /> {t("notify.add")}
            </Button>
          )
        }
      />
      {!data?.items.length ? (
        <EmptyState description={t("notify.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("common.type")}</Th>
              <Th>{t("notify.events")}</Th>
              <Th>{t("common.status")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.items.map((ch) => (
              <tr key={ch.id}>
                <Td className="font-medium">{ch.name}</Td>
                <Td>{t(`notify.${ch.type}`, { defaultValue: ch.type })}</Td>
                <Td>
                  <div className="flex max-w-[24rem] flex-wrap gap-1">
                    {ch.events.map((e) => (
                      <Badge key={e} variant="outline" className="text-[10px]">
                        {e}
                      </Badge>
                    ))}
                  </div>
                </Td>
                <Td>
                  <div className="flex flex-col gap-1">
                    <Badge variant={ch.enabled ? "success" : "muted"}>{ch.enabled ? t("common.enabled") : t("common.disabled")}</Badge>
                    {ch.last_delivery_status && (
                      <span className={`text-[10px] ${ch.last_delivery_status === "failed" ? "text-destructive" : "text-muted-foreground"}`}>
                        {t("notify.lastDelivery", { status: ch.last_delivery_status })}
                      </span>
                    )}
                    {ch.last_delivery_error && (
                      <span className="max-w-[16rem] truncate text-[10px] text-destructive" title={ch.last_delivery_error}>
                        {ch.last_delivery_error}
                      </span>
                    )}
                  </div>
                </Td>
                <Td className="text-right">
                  {isAdmin && (
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" onClick={() => test(ch.id)}>
                        <Send className="h-3.5 w-3.5" /> {t("notify.test")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setEditing(ch);
                          setOpen(true);
                        }}
                      >
                        {t("common.edit")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={async () => {
                          if (!window.confirm(`${t("common.delete")} ${ch.name}?`)) return;
                          try {
                            await request(`/notification-channels/${ch.id}`, { method: "DELETE" });
                            toast.success(t("notify.deleted"));
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
      <NotificationDialog
        open={open}
        onOpenChange={setOpen}
        editing={editing}
        events={data?.events ?? []}
        systemMailConfigured={systemMailConfigured}
      />
    </div>
  );
}

function NotificationDialog({
  open,
  onOpenChange,
  editing,
  events,
  systemMailConfigured,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  editing: NotificationChannel | null;
  events: string[];
  systemMailConfigured: boolean;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  const [type, setType] = React.useState<"smtp" | "webhook" | "telegram" | "feishu" | "dingtalk" | "wecom">("smtp");
  const [enabled, setEnabled] = React.useState(true);
  const [selectedEvents, setSelectedEvents] = React.useState<string[]>(["*"]);
  const [cfg, setCfg] = React.useState<Record<string, string>>({});
  const [useSystemMail, setUseSystemMail] = React.useState(true);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    const pub = editing?.public_config ?? {};
    const initial: Record<string, string> = {};
    for (const [k, v] of Object.entries(pub)) {
      initial[k] = Array.isArray(v) ? v.join(", ") : String(v ?? "");
    }
    setName(editing?.name ?? "");
    setType(editing?.type ?? "smtp");
    setEnabled(editing?.enabled ?? true);
    setSelectedEvents(editing?.events ?? ["*"]);
    setCfg(initial);
    setUseSystemMail(
      editing ? (editing.type === "smtp" ? Boolean(pub.use_system_mail) || !String(pub.host ?? "") : false) : true,
    );
    setError("");
  }, [open, editing]);

  const toggleEvent = (event: string) => {
    setSelectedEvents((prev) => {
      if (event === "*") return prev.includes("*") ? [] : ["*"];
      const next = prev.filter((e) => e !== "*");
      return next.includes(event) ? next.filter((e) => e !== event) : [...next, event];
    });
  };

  const configured = (field: string) => Boolean(editing?.configured_fields?.includes(field));

  const buildConfig = (): Record<string, unknown> => {
    if (type === "smtp") {
      const config: Record<string, unknown> = {
        use_system_mail: useSystemMail,
        to: (cfg.to ?? "")
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      };
      if ((cfg.from ?? "").trim()) config.from = cfg.from.trim();
      if (!useSystemMail) {
        config.host = cfg.host ?? "";
        config.port = Number(cfg.port || 587);
        config.tls = cfg.tls ?? "starttls";
        config.username = cfg.username ?? "";
        if (cfg.password) config.password = cfg.password;
      }
      return config;
    }
    if (type === "webhook") {
      const config: Record<string, unknown> = { url: cfg.url ?? "" };
      if (cfg.secret) config.secret = cfg.secret;
      return config;
    }
    if (type === "telegram") {
      const config: Record<string, unknown> = { chat_id: cfg.chat_id ?? "" };
      if (cfg.bot_token) config.bot_token = cfg.bot_token;
      return config;
    }
    if (type === "wecom") {
      return { webhook_url: cfg.webhook_url ?? "" };
    }
    const config: Record<string, unknown> = { webhook_url: cfg.webhook_url ?? "" };
    if (cfg.secret) config.secret = cfg.secret;
    return config;
  };

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const config = buildConfig();
      if (editing) {
        await request(`/notification-channels/${editing.id}`, {
          method: "PATCH",
          body: { name, type, config, events: selectedEvents, enabled },
        });
        toast.success(t("notify.updated"));
      } else {
        await request("/notification-channels", {
          method: "POST",
          body: { name, type, config, events: selectedEvents, enabled },
        });
        toast.success(t("notify.created"));
      }
      await qc.invalidateQueries({ queryKey: qk.notifications });
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setCfg((c) => ({ ...c, [k]: e.target.value }));

  const changeSystemMail = (next: boolean) => {
    if (next === useSystemMail) return;
    const message = next ? t("notify.switchSystemConfirm") : t("notify.switchIndependentConfirm");
    if (!window.confirm(message)) return;
    setUseSystemMail(next);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{editing ? t("common.edit") : t("notify.add")}</DialogTitle>
          {editing && <DialogDescription>{t("notify.keepSecret")}</DialogDescription>}
        </DialogHeader>
        <div className="space-y-4">
          {error && <ErrorBox message={error} />}
          <Field label={t("common.name")}>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t("common.type")}>
            <Select value={type} onChange={(e) => setType(e.target.value as typeof type)} disabled={Boolean(editing)}>
              <option value="smtp">{t("notify.smtp")}</option>
              <option value="webhook">{t("notify.webhook")}</option>
              <option value="telegram">{t("notify.telegram")}</option>
              <option value="feishu">{t("notify.feishu")}</option>
              <option value="dingtalk">{t("notify.dingtalk")}</option>
              <option value="wecom">{t("notify.wecom")}</option>
            </Select>
          </Field>

          {type === "smtp" && (
            <>
              <label className="flex items-center justify-between gap-4 rounded-md border border-border p-3">
                <span>
                  <span className="text-sm">{t("notify.useSystemMail")}</span>
                  <span className="mt-1 block text-xs text-muted-foreground">{t("notify.useSystemMailHint")}</span>
                </span>
                <Switch checked={useSystemMail} onCheckedChange={changeSystemMail} aria-label={t("notify.useSystemMail")} />
              </label>
              {useSystemMail && !systemMailConfigured && (
                <p className="text-xs text-amber-600 dark:text-amber-400">{t("notify.systemMailNotConfigured")}</p>
              )}
              <div className="grid gap-3 sm:grid-cols-2">
                {!useSystemMail && (
                  <>
                    <Field label={t("notify.host")} className="sm:col-span-2">
                      <Input value={cfg.host ?? ""} onChange={set("host")} placeholder="smtp.example.com" />
                    </Field>
                    <Field label={t("notify.port")}>
                      <Input type="number" value={cfg.port ?? "587"} onChange={set("port")} />
                    </Field>
                    <Field label={t("notify.tls")}>
                      <Select value={cfg.tls ?? "starttls"} onChange={set("tls")}>
                        <option value="starttls">starttls</option>
                        <option value="tls">tls</option>
                        <option value="none">none</option>
                      </Select>
                    </Field>
                    <Field label={t("notify.username")}>
                      <Input value={cfg.username ?? ""} onChange={set("username")} autoComplete="off" />
                    </Field>
                    <Field label={t("auth.password")} hint={configured("password") ? t("notify.keepSecret") : undefined}>
                      <Input
                        type="password"
                        value={cfg.password ?? ""}
                        onChange={set("password")}
                        placeholder={configured("password") ? "••••••••" : ""}
                        autoComplete="new-password"
                      />
                    </Field>
                  </>
                )}
                <Field label={t("notify.from")} hint={useSystemMail ? t("notify.fromOptionalHint") : undefined}>
                  <Input value={cfg.from ?? ""} onChange={set("from")} placeholder="kpccssl@example.com" />
                </Field>
                <Field label={t("notify.to")}>
                  <Input value={cfg.to ?? ""} onChange={set("to")} placeholder="ops@example.com" />
                </Field>
              </div>
              {!useSystemMail && (cfg.tls ?? "starttls") === "none" && (cfg.username ?? "") !== "" && (
                <p className="text-xs text-destructive">{t("notify.insecureAuthHint")}</p>
              )}
            </>
          )}

          {type === "webhook" && (
            <>
              <Field label={t("deploy.url")}>
                <Input value={cfg.url ?? ""} onChange={set("url")} placeholder="https://example.com/hook" />
              </Field>
              <Field label={t("deploy.secret")} hint={configured("secret") ? t("notify.keepSecret") : t("common.optional")}>
                <Input
                  type="password"
                  value={cfg.secret ?? ""}
                  onChange={set("secret")}
                  placeholder={configured("secret") ? "••••••••" : ""}
                  autoComplete="new-password"
                />
              </Field>
            </>
          )}

          {type === "telegram" && (
            <>
              <Field label={t("notify.botToken")} hint={configured("bot_token") ? t("notify.keepSecret") : undefined}>
                <Input
                  type="password"
                  value={cfg.bot_token ?? ""}
                  onChange={set("bot_token")}
                  placeholder={configured("bot_token") ? "••••••••" : ""}
                  autoComplete="new-password"
                />
              </Field>
              <Field label={t("notify.chatId")}>
                <Input value={cfg.chat_id ?? ""} onChange={set("chat_id")} />
              </Field>
            </>
          )}

          {(type === "feishu" || type === "dingtalk" || type === "wecom") && (
            <>
              <Field label={t("notify.webhookUrl")} hint={t("notify.webhookUrlHint")}>
                <Input
                  value={cfg.webhook_url ?? ""}
                  onChange={set("webhook_url")}
                  placeholder={
                    type === "feishu"
                      ? "https://open.feishu.cn/open-apis/bot/v2/hook/..."
                      : type === "dingtalk"
                        ? "https://oapi.dingtalk.com/robot/send?access_token=..."
                        : "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
                  }
                />
              </Field>
              {type !== "wecom" && (
                <Field label={t("notify.signSecret")} hint={configured("secret") ? t("notify.keepSecret") : t("common.optional")}>
                  <Input
                    type="password"
                    value={cfg.secret ?? ""}
                    onChange={set("secret")}
                    placeholder={configured("secret") ? "••••••••" : ""}
                    autoComplete="new-password"
                  />
                </Field>
              )}
            </>
          )}

          <Field label={t("notify.events")} hint={t("notify.eventsHint")}>
            <div className="flex flex-wrap gap-2">
              {events.map((e) => (
                <button
                  key={e}
                  type="button"
                  onClick={() => toggleEvent(e)}
                  className={`rounded-full border px-2.5 py-1 text-xs ${
                    selectedEvents.includes(e) ? "border-primary bg-primary/10 text-primary" : "border-border text-muted-foreground"
                  }`}
                >
                  {e}
                </button>
              ))}
            </div>
          </Field>

          <label className="flex items-center justify-between rounded-md border border-border p-3">
            <span className="text-sm">{t("common.enabled")}</span>
            <Switch checked={enabled} onCheckedChange={setEnabled} aria-label={t("common.enabled")} />
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={submit} disabled={busy || !name}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Copy, KeyRound, Mail, ShieldCheck } from "lucide-react";
import { useAuth } from "@/auth";
import { useAction } from "@/api/queries";
import { ApiError, errorMessage, request } from "@/api/client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ErrorBox, PageHeader } from "@/components/ui/misc";

export function ProfilePage() {
  const { t } = useTranslation();
  const { user, refresh } = useAuth();
  const [current, setCurrent] = React.useState("");
  const [next, setNext] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [setupOpen, setSetupOpen] = React.useState(false);
  const [disableOpen, setDisableOpen] = React.useState(false);
  const [emailOpen, setEmailOpen] = React.useState(false);

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (next !== confirm) {
      setError(t("auth.confirmPassword"));
      return;
    }
    setBusy(true);
    try {
      await request("/me/password", { method: "POST", body: { current_password: current, new_password: next } });
      toast.success(t("profile.passwordChanged"));
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (!user) return null;

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title={t("profile.title")} subtitle={t("profile.subtitle")} />

      <Card className="mb-6">
        <CardContent className="flex items-center justify-between p-5">
          <div>
            <p className="text-sm text-muted-foreground">{t("auth.email")}</p>
            <p className="font-medium">{user.email}</p>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant="outline">{user.role}</Badge>
            <Button size="sm" variant="outline" onClick={() => setEmailOpen(true)}>
              <Mail className="h-4 w-4" /> {t("profile.changeEmail")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="mb-6">
        <CardHeader>
          <CardTitle>{t("profile.password")}</CardTitle>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={changePassword}>
            {error && <ErrorBox message={error} />}
            <Field label={t("auth.currentPassword")}>
              <Input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
            </Field>
            <Field label={t("auth.newPassword")} hint={t("auth.passwordHint")}>
              <Input type="password" autoComplete="new-password" minLength={12} value={next} onChange={(e) => setNext(e.target.value)} required />
            </Field>
            <Field label={t("auth.confirmPassword")}>
              <Input type="password" autoComplete="new-password" minLength={12} value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
            </Field>
            <Button type="submit" disabled={busy}>
              <KeyRound className="h-4 w-4" /> {t("common.save")}
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>{t("profile.totp")}</CardTitle>
            <CardDescription>{user.totp_enabled ? t("profile.totpEnabled") : t("profile.totpDisabled")}</CardDescription>
          </div>
          <Badge variant={user.totp_enabled ? "success" : "muted"}>
            {user.totp_enabled ? t("profile.totpEnabled") : t("profile.totpDisabled")}
          </Badge>
        </CardHeader>
        <CardContent>
          {user.totp_enabled ? (
            <Button variant="outline" onClick={() => setDisableOpen(true)}>
              {t("profile.disableTOTP")}
            </Button>
          ) : (
            <Button onClick={() => setSetupOpen(true)}>
              <ShieldCheck className="h-4 w-4" /> {t("profile.enableTOTP")}
            </Button>
          )}
        </CardContent>
      </Card>

      <SetupTOTPDialog
        open={setupOpen}
        onOpenChange={setSetupOpen}
        onDone={async () => {
          await refresh();
          setSetupOpen(false);
        }}
      />
      <DisableTOTPDialog open={disableOpen} onOpenChange={setDisableOpen} onDone={async () => {
        await refresh();
        setDisableOpen(false);
      }} />
      <ChangeEmailDialog open={emailOpen} onOpenChange={setEmailOpen} />
    </div>
  );
}

function ChangeEmailDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setEmail("");
    setPassword("");
    setError("");
  }, [open]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await request("/me/email", { method: "POST", body: { email, password } });
      toast.success(t("profile.emailChangeSent"));
      onOpenChange(false);
    } catch (err) {
      if (err instanceof ApiError && err.code === "E_MAIL_NOT_CONFIGURED") {
        setError(t("auth.mailNotConfigured"));
      } else if (err instanceof ApiError && err.code === "E_UNAUTHENTICATED") {
        setError(t("profile.wrongPassword"));
      } else {
        setError(errorMessage(err));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("profile.changeEmail")}</DialogTitle>
          <DialogDescription>{t("profile.changeEmailHint")}</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={submit}>
          {error && <ErrorBox message={error} />}
          <Field label={t("profile.newEmail")}>
            <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
          </Field>
          <Field label={t("auth.currentPassword")}>
            <Input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !email || !password}>
              {t("profile.sendVerification")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function SetupTOTPDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => Promise<void> }) {
  const { t } = useTranslation();
  const [secret, setSecret] = React.useState("");
  const [uri, setUri] = React.useState("");
  const [code, setCode] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState("");
  const [codes, setCodes] = React.useState<string[]>([]);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setSecret("");
    setUri("");
    setCode("");
    setPassword("");
    setError("");
    setCodes([]);
    request<{ secret: string; uri: string }>("/me/totp/setup", { method: "POST", body: {} })
      .then((r) => {
        setSecret(r.secret);
        setUri(r.uri);
      })
      .catch((err) => setError(errorMessage(err)));
  }, [open]);

  const enable = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await request<{ recovery_codes: string[] }>("/me/totp/enable", {
        method: "POST",
        body: { secret, code, password },
      });
      setCodes(res.recovery_codes);
      toast.success(t("profile.totpEnabledMsg"));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("profile.enableTOTP")}</DialogTitle>
          <DialogDescription>{t("profile.scanHint")}</DialogDescription>
        </DialogHeader>
        {codes.length > 0 ? (
          <div className="space-y-4">
            <p className="text-sm font-medium">{t("profile.recoveryCodes")}</p>
            <div className="grid grid-cols-2 gap-2 rounded-md border border-border bg-muted/40 p-3 font-mono text-sm">
              {codes.map((c) => (
                <span key={c}>{c}</span>
              ))}
            </div>
            <DialogFooter>
              <Button
                onClick={async () => {
                  await onDone();
                }}
              >
                {t("common.close")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <div className="space-y-4">
            {error && <ErrorBox message={error} />}
            {secret && (
              <div className="rounded-md border border-border bg-muted/40 p-3">
                <p className="text-xs text-muted-foreground">Secret</p>
                <div className="flex items-center justify-between gap-2">
                  <code className="break-all font-mono text-sm">{secret}</code>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                      void navigator.clipboard.writeText(secret);
                      toast.success(t("common.copied"));
                    }}
                  >
                    <Copy className="h-4 w-4" />
                  </Button>
                </div>
                <p className="mt-2 break-all text-[10px] text-muted-foreground">{uri}</p>
              </div>
            )}
            <Field label={t("profile.confirmEnable")}>
              <Input
                inputMode="numeric"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              />
            </Field>
            <Field label={t("auth.password")}>
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
            </Field>
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
              <Button onClick={enable} disabled={busy || code.length !== 6 || !password}>
                {t("common.confirm")}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function DisableTOTPDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => Promise<void> }) {
  const { t } = useTranslation();
  const [password, setPassword] = React.useState("");
  const [code, setCode] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const action = useAction(
    () => request("/me/totp/disable", { method: "POST", body: { password, code } }),
    { success: t("profile.totpDisabledMsg"), onDone: () => void onDone() },
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("profile.disableTOTP")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {error && <ErrorBox message={error} />}
          <Field label={t("auth.password")}>
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Field label={t("auth.totpCode")}>
            <Input inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
          </Field>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button
            variant="destructive"
            disabled={busy || !password || code.length !== 6}
            onClick={() => {
              setBusy(true);
              setError("");
              action.mutate(undefined, {
                onError: (err) => setError(errorMessage(err)),
                onSettled: () => setBusy(false),
              });
            }}
          >
            {t("common.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

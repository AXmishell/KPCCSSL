import * as React from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ShieldCheck } from "lucide-react";
import { useAuth } from "@/auth";
import { ApiError, errorMessage } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { ErrorBox } from "@/components/ui/misc";

export function LoginPage() {
  const { t } = useTranslation();
  const { login, user } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [totp, setTotp] = React.useState("");
  const [needsTOTP, setNeedsTOTP] = React.useState(false);
  const [useRecovery, setUseRecovery] = React.useState(false);
  const [recovery, setRecovery] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (user) navigate("/", { replace: true });
  }, [user, navigate]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(email, password, totp, useRecovery ? recovery : "");
      navigate("/", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === "E_TOTP_REQUIRED") {
        setNeedsTOTP(true);
        setError(t("auth.totpRequired"));
      } else if (err instanceof ApiError && err.code === "E_RATE_LIMIT") {
        setError(err.message);
      } else {
        setError(err instanceof ApiError && err.status === 401 ? t("auth.invalid") : errorMessage(err));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-full items-center justify-center p-4">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15">
            <ShieldCheck className="h-7 w-7 text-primary" />
          </div>
          <h1 className="text-2xl font-semibold">KPCCSSL</h1>
          <p className="text-sm text-muted-foreground">{t("tagline")}</p>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{t("auth.loginTitle")}</CardTitle>
            <CardDescription>{t("nav.certificates")} · ACME · DNS-01</CardDescription>
          </CardHeader>
          <CardContent>
            <form className="space-y-4" onSubmit={submit}>
              {error && <ErrorBox message={error} />}
              <Field label={t("auth.email")} htmlFor="email">
                <Input
                  id="email"
                  type="email"
                  autoComplete="username"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </Field>
              <Field label={t("auth.password")} htmlFor="password">
                <Input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </Field>
              {needsTOTP && !useRecovery && (
                <Field label={t("auth.totpCode")} htmlFor="totp">
                  <Input
                    id="totp"
                    inputMode="numeric"
                    pattern="[0-9]*"
                    maxLength={6}
                    autoComplete="one-time-code"
                    value={totp}
                    onChange={(e) => setTotp(e.target.value.replace(/\D/g, ""))}
                  />
                </Field>
              )}
              {useRecovery && (
                <Field label={t("auth.recoveryCode")} htmlFor="recovery">
                  <Input id="recovery" value={recovery} onChange={(e) => setRecovery(e.target.value)} />
                </Field>
              )}
              {needsTOTP && (
                <button
                  type="button"
                  className="text-xs text-muted-foreground underline-offset-2 hover:underline"
                  onClick={() => {
                    setUseRecovery((v) => !v);
                    setError("");
                  }}
                >
                  {useRecovery ? t("auth.useTOTP") : t("auth.useRecovery")}
                </button>
              )}
              <Button type="submit" className="w-full" disabled={busy}>
                {busy ? t("common.loading") : t("auth.login")}
              </Button>
            </form>
          </CardContent>
        </Card>
        <p className="mt-4 text-center text-xs text-muted-foreground">
          <Link to="/" className="hover:underline">
            KPCCSSL
          </Link>
        </p>
      </div>
    </div>
  );
}
